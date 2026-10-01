package mirrors

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"golang.org/x/sys/unix"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
)

type metadata struct{ key, etag, mime string }
type metadataCache struct {
	mu    sync.Mutex
	limit int
	items map[string]*list.Element
	lru   *list.List
}

func newCache(limit int) *metadataCache {
	return &metadataCache{limit: limit, items: map[string]*list.Element{}, lru: list.New()}
}
func (c *metadataCache) get(generation, p string, st os.FileInfo) metadata {
	key := fmt.Sprintf("%s:%s:%d:%d", generation, p, st.Size(), st.ModTime().UnixNano())
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.items[key]; e != nil {
		c.lru.MoveToFront(e)
		return e.Value.(metadata)
	}
	sum := sha256.Sum256([]byte(key))
	v := metadata{key: key, etag: `"` + hex.EncodeToString(sum[:]) + `"`, mime: mime.TypeByExtension(path.Ext(p))}
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

type listingItem struct {
	Name, URL string
	Dir       bool
}

var listing = template.Must(template.New("listing").Parse(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><link rel="icon" type="image/x-icon" href="/favicon.ico?v=2"><title>MirrorDock · {{.Path}}</title><style>body{font:15px Georgia,"PingFang SC",serif;color:#20304b;background:#f7f9fc;margin:5% auto;max-width:960px;padding:24px}h1{font-size:28px}a{color:#315bea;text-decoration:none}li{padding:12px 0;border-bottom:1px solid #dfe6f1}ul{list-style:none;padding:0}small{color:#7f8da5}</style><small>MIRRORDOCK / PUBLIC MIRROR</small><h1>{{.Path}}</h1><ul>{{range .Items}}<li><a href="{{.URL}}">{{.Name}}{{if .Dir}}/{{end}}</a></li>{{end}}</ul><small>{{if .More}}目录条目超过上限，请通过明确文件路径下载。{{else}}已发布镜像 · 支持 HEAD / Range{{end}}</small></html>`))

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
	var file *os.File
	var st os.FileInfo
	generation := entry.Publication.Current.ID
	for i, root := range entry.roots {
		f, err := root.OpenFile(rel, os.O_RDONLY|unix.O_NONBLOCK, 0)
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
		generation = entry.generations[i].ID
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
		m.directory(w, r, file)
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
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(rel)}))
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
	items := []listingItem{}
	for name := range children {
		items = append(items, listingItem{Name: name, URL: r.URL.EscapedPath() + url.PathEscape(name) + "/", Dir: true})
	}
	renderListing(w, r, items, false)
}
func (m *Manager) directory(w http.ResponseWriter, r *http.Request, f *os.File) {
	entries, err := f.ReadDir(1001)
	if err != nil && err != io.EOF {
		http.Error(w, "directory unavailable", 500)
		return
	}
	more := len(entries) > 1000
	if more {
		entries = entries[:1000]
	}
	items := []listingItem{}
	for _, e := range entries {
		if hidden(e.Name()) {
			continue
		}
		items = append(items, listingItem{Name: e.Name(), URL: r.URL.EscapedPath() + url.PathEscape(e.Name()), Dir: e.IsDir()})
	}
	renderListing(w, r, items, more)
}
func renderListing(w http.ResponseWriter, r *http.Request, items []listingItem, more bool) {
	slices.SortFunc(items, func(a, b listingItem) int { return strings.Compare(a.Name, b.Name) })
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	if r.Method == "HEAD" {
		return
	}
	_ = listing.Execute(w, struct {
		Path  string
		Items []listingItem
		More  bool
	}{r.URL.Path, items, more})
}
