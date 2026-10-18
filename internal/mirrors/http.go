package mirrors

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
)

// metadataKey identifies one file version inside an immutable generation.
type metadataKey struct {
	generation, path string
	size, modified   int64
}

// metadata holds the response headers derived from a file's identity, so the
// download path neither hashes contents nor formats headers repeatedly.
type metadata struct {
	key                     metadataKey
	etag, mime, disposition string
}

// metadataCache is a bounded LRU of response metadata.
type metadataCache struct {
	mu    sync.Mutex
	limit int
	items map[metadataKey]*list.Element
	lru   *list.List
}

func newCache(limit int) *metadataCache {
	return &metadataCache{limit: limit, items: map[metadataKey]*list.Element{}, lru: list.New()}
}

func (c *metadataCache) get(generation, name string, info os.FileInfo) metadata {
	key := metadataKey{generation, name, info.Size(), info.ModTime().UnixNano()}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.items[key]; element != nil {
		c.lru.MoveToFront(element)
		return element.Value.(metadata)
	}
	// The ETag is derived from the file identity, never from its contents.
	sum := sha256.Sum256(fmt.Appendf(nil, "%s:%s:%d:%d", generation, name, key.size, key.modified))
	entry := metadata{
		key:         key,
		etag:        `"` + hex.EncodeToString(sum[:]) + `"`,
		mime:        mime.TypeByExtension(path.Ext(name)),
		disposition: mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}),
	}
	if entry.mime == "" {
		entry.mime = "application/octet-stream"
	}
	c.items[key] = c.lru.PushFront(entry)
	if c.lru.Len() > c.limit {
		oldest := c.lru.Back()
		delete(c.items, oldest.Value.(metadata).key)
		c.lru.Remove(oldest)
	}
	return entry
}

// hidden reports whether any segment of a slash path is private: internal
// markers, rsync and download staging files, or a parent reference.
func hidden(name string) bool {
	for segment := range strings.SplitSeq(name, "/") {
		private := strings.HasPrefix(segment, ".mirrordock") ||
			segment == ".rsync-partial" ||
			segment == ".~tmp~" ||
			strings.HasSuffix(segment, ".part") ||
			segment == ".."
		if private {
			return true
		}
	}
	return false
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	m.requests.Add(1)
	decoded := r.URL.Path
	if strings.ContainsAny(decoded, "\\\x00") || hidden(decoded) {
		http.NotFound(w, r)
		return
	}
	clean := path.Clean("/" + strings.TrimPrefix(decoded, "/"))
	if clean != strings.TrimSuffix(decoded, "/") && !(clean == "/" && decoded == "/") {
		http.NotFound(w, r)
		return
	}
	requested := strings.TrimPrefix(clean, "/")
	// Lease the publication so a concurrent swap cannot retire its roots
	// while this request is served. Destinations never nest, so the first
	// matching prefix is the only one.
	var entry *opened
	var rel string
	m.routeMu.Lock()
	table := m.table.Load()
	entry = table.entries[requested]
	if entry == nil {
		for i, character := range requested {
			if character != '/' {
				continue
			}
			if candidate := table.entries[requested[:i]]; candidate != nil {
				entry = candidate
				rel = requested[i+1:]
				break
			}
		}
	}
	if entry != nil {
		entry.refs++
	}
	m.routeMu.Unlock()
	if entry == nil {
		m.static(w, r, requested, table)
		return
	}
	defer m.release(entry)
	if rel == "" {
		rel = "."
	}
	if hidden(rel) {
		http.NotFound(w, r)
		return
	}
	// Bound descriptors before opening files, including HEAD and directory
	// requests. This also bounds HTTP/2 streams sharing a single connection.
	select {
	case m.fileGates <- struct{}{}:
		defer func() { <-m.fileGates }()
	default:
		w.Header().Set("Retry-After", "3")
		http.Error(w, "file capacity reached", http.StatusServiceUnavailable)
		return
	}
	// The current generation is tried first; retained older generations
	// still serve files that clients resolved from previous metadata.
	file, st, generation := m.openInGenerations(r, entry, rel)
	if file == nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, no-cache")
	if st.IsDir() {
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.EscapedPath()+"/", 301)
			return
		}
		m.directory(w, r, file, entry)
		return
	}
	m.serveFile(w, r, file, st, generation, rel)
}

// serveFile sends a regular file opened inside a generation, or inside the
// mirror root for static content; generation scopes its cached metadata.
func (m *Manager) serveFile(w http.ResponseWriter, r *http.Request, file *os.File, st os.FileInfo, generation, rel string) {
	if !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if r.Method == "GET" {
		pool := m.gates
		if st.Size() <= smallDownloadBytes {
			pool = m.metadataGates
		}
		select {
		case pool <- struct{}{}:
			defer func() { <-pool }()
		default:
			w.Header().Set("Retry-After", "3")
			http.Error(w, "download capacity reached", 503)
			return
		}
		m.active.Add(1)
		defer m.active.Add(-1)
	}
	meta := m.cache.get(generation, rel, st)
	w.Header().Set("ETag", meta.etag)
	w.Header().Set("Content-Type", meta.mime)
	w.Header().Set("Content-Disposition", meta.disposition)
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	if strings.Count(r.Header.Get("Range"), ",") >= maxRangeSeparators {
		r = r.Clone(r.Context())
		r.Header = r.Header.Clone()
		r.Header.Del("Range")
	}
	// The raw os.File and underlying ReaderFrom are deliberately preserved.
	// No body-transforming reader or compression middleware enters this path.
	http.ServeContent(&countWriter{ResponseWriter: w, manager: m}, r, path.Base(rel), st.ModTime(), file)
}

// openInGenerations opens rel in the first generation that has it. The root
// is borrowed only while opening; os.Root keeps symbolic links inside it.
// O_NONBLOCK prevents a FIFO from blocking the request.
func (m *Manager) openInGenerations(r *http.Request, entry *opened, rel string) (*os.File, os.FileInfo, string) {
	for i, generation := range entry.generations {
		if r.Context().Err() != nil {
			return nil, nil, ""
		}
		root, release, err := m.roots.borrow(entry.paths[i])
		if err != nil {
			continue
		}
		file, err := root.OpenFile(rel, os.O_RDONLY|unix.O_NONBLOCK, 0)
		release()
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			continue
		}
		return file, info, generation.ID
	}
	return nil, nil, ""
}

// maxRangeSeparators drops Range headers with more parts, which would cost
// many small reads for one request; the full file is served instead.
const maxRangeSeparators = 16

type countWriter struct {
	http.ResponseWriter
	manager *Manager
}

var contentBuffers = sync.Pool{New: func() any { return new(make([]byte, 32<<10)) }}

func (w *countWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.manager.bytes.Add(int64(n))
	return n, err
}

func (w *countWriter) ReadFrom(r io.Reader) (int64, error) {
	var n int64
	var err error
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err = rf.ReadFrom(r)
	} else {
		buffer := contentBuffers.Get().(*[]byte)
		n, err = io.CopyBuffer(struct{ io.Writer }{w.ResponseWriter}, r, *buffer)
		contentBuffers.Put(buffer)
	}
	w.manager.bytes.Add(n)
	return n, err
}

func (m *Manager) directory(w http.ResponseWriter, r *http.Request, f *os.File, publications ...*opened) {
	if r.Method == "HEAD" {
		renderListing(w, r, nil, false)
		return
	}
	if !m.acquireListing(w) {
		return
	}
	defer func() { <-m.listingGates }()
	var publication Publication
	if len(publications) > 0 {
		publication = publications[0].Publication
	}
	releaseRoot := publication.Type == "github_release" && strings.Trim(r.URL.Path, "/") == publication.Dest
	items, more, err := readListing(r, f, maxListedEntries, func(entry os.DirEntry) (listingItem, bool) {
		if hidden(entry.Name()) {
			return listingItem{}, false
		}
		link := r.URL.EscapedPath() + url.PathEscape(entry.Name())
		if entry.IsDir() {
			link += "/"
		}
		// DirEntry.Info uses lstat: listing metadata never follows symbolic links.
		info, _ := entry.Info()
		item := newListingItem(entry.Name(), link, entry.IsDir(), info)
		if releaseRoot && entry.IsDir() && publication.Current.Summary != nil {
			if release, known := publication.Current.Summary.Releases[entry.Name()]; known {
				item.Bytes, item.Size = release.Bytes, fileSize(release.Bytes)
			}
		}
		return item, true
	})
	if err != nil {
		if r.Context().Err() == nil {
			http.Error(w, "directory unavailable", 500)
		}
		return
	}
	renderListing(w, r, items, more)
}

// readListing maps the entries of f to at most limit items, reading until one
// more is found; entries that item rejects do not consume the public quota.
func readListing(r *http.Request, f *os.File, limit int, item func(os.DirEntry) (listingItem, bool)) ([]listingItem, bool, error) {
	items := []listingItem{}
	for len(items) <= limit {
		if err := r.Context().Err(); err != nil {
			return nil, false, err
		}
		batch, err := f.ReadDir(128)
		if err != nil && err != io.EOF {
			return nil, false, err
		}
		for _, entry := range batch {
			if listed, ok := item(entry); ok {
				items = append(items, listed)
			}
		}
		if err == io.EOF {
			break
		}
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	return items, more, nil
}
