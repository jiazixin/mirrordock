package mirrors

import (
	"cmp"
	"errors"
	"golang.org/x/sys/unix"
	"io/fs"
	"mirrordock/internal/config"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

// Everything placed directly in the mirror root outside the configured dests
// and the private .mirrordock area is public as it is. It is served live from
// the root without generations: a file rewritten in place may be read while
// it changes, so content should be replaced by rename.

// staticGeneration scopes cached metadata of live mirror-root files; their
// size and modification time keep the ETag current.
const staticGeneration = "static"

// maxStaticLinks bounds symbolic links followed while resolving one path.
const maxStaticLinks = 40

var errStaticEscape = errors.New("path leaves the mirror root")

// staticBlocked reports whether a symlink-free path inside the mirror root
// belongs to MirrorDock instead: private names, the routes of the service
// itself and every configured dest, including ones that are not published.
func (m *Manager) staticBlocked(name string) bool {
	if name == "." {
		return false
	}
	first, _, _ := strings.Cut(name, "/")
	if hidden(name) || config.ReservedTopLevel(first) {
		return true
	}
	dests := m.dests.Load()
	if dests == nil {
		return false
	}
	for i, character := range name {
		if character == '/' && (*dests)[name[:i]] {
			return true
		}
	}
	return (*dests)[name]
}

// resolveStatic follows symbolic links itself so that no step of name passes
// through a blocked path, such as an alias into a publication generation.
// Absolute links and links leaving the root are rejected, as os.Root does. It
// returns the symlink-free path and the lstat of its final component.
func (m *Manager) resolveStatic(name string) (string, os.FileInfo, error) {
	pending := strings.Split(name, "/")
	resolved := []string{}
	links := 0
	for len(pending) > 0 {
		segment := pending[0]
		pending = pending[1:]
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(resolved) == 0 {
				return "", nil, errStaticEscape
			}
			resolved = resolved[:len(resolved)-1]
			continue
		}
		candidate := path.Join(strings.Join(resolved, "/"), segment)
		if m.staticBlocked(candidate) {
			return "", nil, fs.ErrNotExist
		}
		info, err := m.roots.anchor.Lstat(candidate)
		if err != nil {
			return "", nil, err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			resolved = append(resolved, segment)
			continue
		}
		if links++; links > maxStaticLinks {
			return "", nil, errors.New("too many symbolic links")
		}
		target, err := m.roots.anchor.Readlink(candidate)
		if err != nil {
			return "", nil, err
		}
		if target == "" || path.IsAbs(target) {
			return "", nil, errStaticEscape
		}
		// A relative target is resolved from the directory holding the link.
		pending = append(strings.Split(target, "/"), pending...)
	}
	clean := "."
	if len(resolved) > 0 {
		clean = strings.Join(resolved, "/")
	}
	info, err := m.roots.anchor.Lstat(clean)
	if err != nil {
		return "", nil, err
	}
	return clean, info, nil
}

// static serves a path outside every published dest from the mirror root.
// Directories above published dests also list those dests as projects.
func (m *Manager) static(w http.ResponseWriter, r *http.Request, name string, table *routes) {
	routed := map[string]bool{}
	prefix := name
	if prefix != "" {
		prefix += "/"
	}
	for dest := range table.entries {
		if tail, ok := strings.CutPrefix(dest, prefix); ok {
			first, _, _ := strings.Cut(tail, "/")
			routed[first] = true
		}
	}
	resolved, info, err := m.resolveStatic(cmp.Or(name, "."))
	onDisk := err == nil && (info.IsDir() || info.Mode().IsRegular())
	if !onDisk && len(routed) == 0 && name != "" {
		http.NotFound(w, r)
		return
	}
	directory := !onDisk || info.IsDir() || len(routed) > 0
	if directory && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, r.URL.EscapedPath()+"/", http.StatusMovedPermanently)
		return
	}
	var file *os.File
	if onDisk {
		// Bound descriptors before opening, as for published files.
		select {
		case m.openFiles <- struct{}{}:
			defer func() { <-m.openFiles }()
		default:
			w.Header().Set("Retry-After", "3")
			http.Error(w, "file capacity reached", http.StatusServiceUnavailable)
			return
		}
		// O_NONBLOCK keeps a FIFO swapped in after resolution from blocking;
		// the identity check rejects any entry replaced since its lstat.
		file, err = m.roots.anchor.OpenFile(resolved, os.O_RDONLY|unix.O_NONBLOCK, 0)
		if err == nil {
			defer file.Close()
			if st, statErr := file.Stat(); statErr != nil || !os.SameFile(info, st) {
				file = nil
			} else {
				info = st
			}
		} else {
			file = nil
		}
		if file == nil && len(routed) == 0 && name != "" {
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, no-cache")
	if !directory {
		m.serveFile(w, r, file, info, staticGeneration, name)
		return
	}
	if r.Method == "HEAD" {
		renderListing(w, r, nil, false)
		return
	}
	if !m.acquireListing(w) {
		return
	}
	defer func() { <-m.listingRenders }()
	items := []listingItem{}
	var infos map[string]SourceListingInfo
	for child := range routed {
		item := newListingItem(child, r.URL.EscapedPath()+url.PathEscape(child)+"/", true, nil)
		if entry := table.entries[prefix+child]; entry != nil {
			if infos == nil && m.ListingInfo != nil {
				infos = m.ListingInfo(r.Context())
			}
			item = projectItem(item, entry.Publication, infos[entry.Publication.SourceID])
		}
		items = append(items, item)
	}
	more := false
	if file != nil && info.IsDir() {
		var listed []listingItem
		// Published projects are always listed; files share the remaining quota.
		limit := max(0, maxListedEntries-len(items))
		listed, more, err = readListing(r, file, limit, func(entry os.DirEntry) (listingItem, bool) {
			if routed[entry.Name()] {
				return listingItem{}, false
			}
			return m.staticItem(r, resolved, entry)
		})
		if err != nil {
			if r.Context().Err() == nil {
				http.Error(w, "directory unavailable", 500)
			}
			return
		}
		items = append(items, listed...)
	}
	renderListing(w, r, items, more)
}

// staticItem lists one entry of the symlink-free directory dir. Symbolic
// links are shown as their targets; entries that cannot be served are left out.
func (m *Manager) staticItem(r *http.Request, dir string, entry os.DirEntry) (listingItem, bool) {
	name := entry.Name()
	full := name
	if dir != "." {
		full = dir + "/" + name
	}
	if m.staticBlocked(full) {
		return listingItem{}, false
	}
	info, err := entry.Info()
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		_, info, err = m.resolveStatic(full)
	}
	if err != nil || !(info.IsDir() || info.Mode().IsRegular()) {
		return listingItem{}, false
	}
	link := r.URL.EscapedPath() + url.PathEscape(name)
	if info.IsDir() {
		link += "/"
	}
	return newListingItem(name, link, info.IsDir(), info), true
}
