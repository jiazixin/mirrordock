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

type metadataKey struct {
	generation, path string
	size, modified   int64
}
type metadata struct {
	key                     metadataKey
	etag, mime, disposition string
}
type metadataCache struct {
	mu    sync.Mutex
	limit int
	items map[metadataKey]*list.Element
	lru   *list.List
}

func newCache(limit int) *metadataCache {
	return &metadataCache{limit: limit, items: map[metadataKey]*list.Element{}, lru: list.New()}
}
func (c *metadataCache) get(generation, p string, st os.FileInfo) metadata {
	key := metadataKey{generation, p, st.Size(), st.ModTime().UnixNano()}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.items[key]; e != nil {
		c.lru.MoveToFront(e)
		return e.Value.(metadata)
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s:%s:%d:%d", generation, p, key.size, key.modified))
	v := metadata{key: key, etag: `"` + hex.EncodeToString(sum[:]) + `"`, mime: mime.TypeByExtension(path.Ext(p)), disposition: mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(p)})}
	if v.mime == "" {
		v.mime = "application/octet-stream"
	}
	c.items[key] = c.lru.PushFront(v)
	if c.lru.Len() > c.limit {
		e := c.lru.Back()
		delete(c.items, e.Value.(metadata).key)
		c.lru.Remove(e)
	}
	return v
}
func hidden(p string) bool {
	for seg := range strings.SplitSeq(p, "/") {
		if strings.HasPrefix(seg, ".mirrordock") || seg == ".rsync-partial" || seg == ".~tmp~" || strings.HasSuffix(seg, ".part") || seg == ".." {
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
	p := strings.TrimPrefix(clean, "/")
	table := m.table.Load()
	var entry *opened
	var rel string
	m.routeMu.Lock()
	table = m.table.Load()
	entry = table.entries[p]
	if entry == nil {
		for i, c := range p {
			if c == '/' {
				if o := table.entries[p[:i]]; o != nil {
					entry = o
					rel = p[i+1:]
					break
				}
			}
		}
	}
	if entry != nil {
		entry.refs++
	}
	m.routeMu.Unlock()
	if entry == nil {
		m.virtual(w, r, p, table)
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
	var file *os.File
	var st os.FileInfo
	generation := entry.Publication.Current.ID
	for i, g := range entry.generations {
		if r.Context().Err() != nil {
			return
		}
		root, release, err := m.roots.borrow(entry.paths[i])
		if err != nil {
			continue
		}
		f, err := root.OpenFile(rel, os.O_RDONLY|unix.O_NONBLOCK, 0)
		release()
		if err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			continue
		}
		file = f
		st = info
		generation = g.ID
		break
	}
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
	if !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	if r.Method == "GET" {
		pool := m.gates
		if st.Size() <= 256<<10 {
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
	if strings.Count(r.Header.Get("Range"), ",") >= 16 {
		r = r.Clone(r.Context())
		r.Header = r.Header.Clone()
		r.Header.Del("Range")
	}
	// The raw os.File and underlying ReaderFrom are deliberately preserved.
	// No body-transforming reader or compression middleware enters this path.
	http.ServeContent(&countWriter{ResponseWriter: w, manager: m}, r, path.Base(rel), st.ModTime(), file)
}

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
func (m *Manager) virtual(w http.ResponseWriter, r *http.Request, p string, table *routes) {
	if r.Method != "HEAD" {
		if !m.acquireListing(w) {
			return
		}
		defer func() { <-m.listingGates }()
	}
	children := map[string]bool{}
	prefix := p
	if prefix != "" {
		prefix += "/"
	}
	for dest := range table.entries {
		if strings.HasPrefix(dest, prefix) {
			tail := strings.TrimPrefix(dest, prefix)
			first, _, _ := strings.Cut(tail, "/")
			children[first] = true
		}
	}
	if len(children) == 0 && p != "" {
		http.NotFound(w, r)
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, r.URL.EscapedPath()+"/", 301)
		return
	}
	if r.Method == "HEAD" {
		renderListing(w, r, nil, false)
		return
	}
	items := []listingItem{}
	var infos map[string]SourceListingInfo
	if m.ListingInfo != nil {
		for name := range children {
			if table.entries[prefix+name] != nil {
				infos = m.ListingInfo(r.Context())
				break
			}
		}
	}
	for name := range children {
		item := newListingItem(name, r.URL.EscapedPath()+url.PathEscape(name)+"/", true, nil)
		if entry := table.entries[prefix+name]; entry != nil {
			item = projectItem(item, entry.Publication, infos[entry.Publication.SourceID])
		}
		items = append(items, item)
	}
	renderListing(w, r, items, false)
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
	// Bound retained entries; private files do not consume the public quota.
	entries := []os.DirEntry{}
	for len(entries) <= 1000 {
		if r.Context().Err() != nil {
			return
		}
		batch, err := f.ReadDir(128)
		if err != nil && err != io.EOF {
			http.Error(w, "directory unavailable", 500)
			return
		}
		for _, entry := range batch {
			if !hidden(entry.Name()) {
				entries = append(entries, entry)
			}
		}
		if err == io.EOF {
			break
		}
	}
	more := len(entries) > 1000
	if more {
		entries = entries[:1000]
	}

	items := []listingItem{}
	var publication Publication
	if len(publications) > 0 {
		publication = publications[0].Publication
	}
	for _, e := range entries {
		if hidden(e.Name()) {
			continue
		}
		u := r.URL.EscapedPath() + url.PathEscape(e.Name())
		if e.IsDir() {
			u += "/"
		}
		// DirEntry.Info uses lstat: listing metadata never follows symbolic links.
		info, _ := e.Info()
		item := newListingItem(e.Name(), u, e.IsDir(), info)
		if publication.Type == "github_release" && strings.Trim(r.URL.Path, "/") == publication.Dest && e.IsDir() {
			if publication.Current.Summary != nil {
				if release, known := publication.Current.Summary.Releases[e.Name()]; known {
					item.Bytes, item.Size = release.Bytes, fileSize(release.Bytes)
				}
			}
		}
		items = append(items, item)
	}
	renderListing(w, r, items, more)
}
