package mirrors

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mirrordock/internal/config"
	"mirrordock/internal/database"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"
)

type Generation struct {
	ID          string            `json:"id"`
	Directory   string            `json:"directory"`
	PublishedAt time.Time         `json:"published_at"`
	RetainUntil time.Time         `json:"retain_until,omitzero"`
	Summary     *DirectorySummary `json:"summary,omitzero"`
}
type Publication struct {
	SourceID string       `json:"source_id"`
	Type     string       `json:"type"`
	Dest     string       `json:"dest"`
	Current  Generation   `json:"current"`
	Previous []Generation `json:"previous"`
}
type opened struct {
	Publication Publication
	generations []Generation
	paths       []string
	refs        int
}
type routes struct{ entries map[string]*opened }
type Manager struct {
	probeMu       sync.Mutex
	collectMu     sync.Mutex
	storageMu     sync.Mutex
	syncing       map[int64]bool
	clone         func(*os.File, string) error
	space         func(string) (DiskSpace, error)
	mu            sync.Mutex
	routeMu       sync.RWMutex
	Root          string
	store         *database.Store
	table         atomic.Pointer[routes]
	retired       []*opened
	active        atomic.Int64
	gates         chan struct{}
	metadataGates chan struct{}
	listingGates  chan struct{}
	fileGates     chan struct{}
	requests      atomic.Int64
	bytes         atomic.Int64
	cache         *metadataCache
	roots         *rootCache
	ListingInfo   func(string) SourceListingInfo
}

func New(root string, store *database.Store, sources []config.Source) (*Manager, error) {
	if err := os.MkdirAll(filepath.Join(root, ".mirrordock", "work"), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, ".mirrordock", "published"), 0700); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	m := &Manager{Root: canonical, store: store, gates: make(chan struct{}, 256), metadataGates: make(chan struct{}, 64), listingGates: make(chan struct{}, 8), cache: newCache(8192), syncing: map[int64]bool{}, clone: cloneFile, space: diskSpace}
	files, _ := DescriptorLimits()
	m.fileGates = make(chan struct{}, files)
	m.table.Store(&routes{entries: map[string]*opened{}})
	m.roots, err = newRootCache(canonical, rootCacheLimit())
	if err != nil {
		return nil, err
	}
	_ = m.ProbeReflink(context.Background())
	ready := false
	defer func() {
		if !ready {
			_ = m.Close()
		}
	}()
	records, err := store.Publications()
	if err != nil {
		return nil, err
	}
	byID := map[string]Publication{}
	for _, b := range records {
		var p Publication
		if err = json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		byID[p.SourceID] = p
	}
	for _, s := range sources {
		p, ok := byID[s.ID]
		if ok && p.Dest != s.Dest {
			continue
		}
		if !ok {
			local := filepath.Join(root, filepath.FromSlash(s.Dest))
			if st, e := os.Stat(local); e == nil && st.IsDir() {
				real, e := filepath.EvalSymlinks(local)
				if e != nil || !config.Within(canonical, real) {
					return nil, fmt.Errorf("unsafe import %s", s.ID)
				}
				if config.Within(filepath.Join(canonical, ".mirrordock"), real) {
					return nil, fmt.Errorf("%s 指向内部发布代但没有对应数据库记录，请恢复数据库备份", s.ID)
				}
				if e = Validate(context.Background(), real, s.Type); e != nil {
					return nil, fmt.Errorf("import %s: %w", s.ID, e)
				}
				g := Generation{ID: uuid.New().String(), PublishedAt: time.Now().UTC()}
				g.Directory = filepath.Join(canonical, ".mirrordock", "published", g.ID)
				// Normalize an imported directory into an immutable generation before
				// creating its public alias. Never subsequently copy into the live tree.
				if e = os.Rename(real, g.Directory); e != nil {
					return nil, e
				}
				p = Publication{SourceID: s.ID, Dest: s.Dest, Current: g}
				if e = store.SavePublication(s.ID, p); e != nil {
					// A failed import must leave the original directory available for
					// the next startup rather than orphaning it in a private generation.
					return nil, errors.Join(e, os.Rename(g.Directory, real))
				}
			} else {
				continue
			}
		}
		p.Type = s.Type
		if p.Current.Summary == nil {
			real, e := filepath.EvalSymlinks(p.Current.Directory)
			if e != nil || !config.Within(m.Root, real) {
				return nil, errors.New("publication escaped mirror root")
			}
			p.Current.Summary, err = summarizeDirectory(context.Background(), p.Current.Directory, s.Type)
			if err != nil {
				return nil, err
			}
			if err = store.SavePublication(s.ID, p); err != nil {
				return nil, err
			}
		}
		if err = m.register(p); err != nil {
			return nil, err
		}
		if err = m.alias(p); err != nil {
			return nil, err
		}
	}
	ready = true
	return m, nil
}
func (m *Manager) openPublication(p Publication) (*opened, error) {
	o := &opened{Publication: p}
	for _, g := range append([]Generation{p.Current}, p.Previous...) {
		rel, err := m.generationPath(g)
		if err != nil {
			return nil, err
		}
		if g.ID == p.Current.ID {
			root, err := m.openGeneration(g)
			if err != nil {
				return nil, err
			}
			if err := root.Close(); err != nil {
				return nil, err
			}
		}
		o.generations = append(o.generations, g)
		o.paths = append(o.paths, rel)
	}
	return o, nil
}

func (m *Manager) generationPath(g Generation) (string, error) {
	rel, err := filepath.Rel(m.Root, g.Directory)
	if err != nil || !config.Within(m.Root, g.Directory) {
		return "", errors.New("publication escaped mirror root")
	}
	return rel, nil
}

// Publication validation uses a transient root; requests use the bounded cache.
func (m *Manager) openGeneration(g Generation) (*os.Root, error) {
	rel, err := m.generationPath(g)
	if err != nil {
		return nil, err
	}
	return m.roots.anchor.OpenRoot(rel)
}
func (m *Manager) installOpened(o *opened) {
	old := m.table.Load()
	next := &routes{entries: make(map[string]*opened, len(old.entries)+1)}
	for k, v := range old.entries {
		next.entries[k] = v
	}
	next.entries[o.Publication.Dest] = o
	m.install(next)
}
func (m *Manager) register(p Publication) error {
	o, err := m.openPublication(p)
	if err != nil {
		return err
	}
	m.installOpened(o)
	return nil
}

// install takes only the routing lock; SQLite writes and filesystem work never
// block requests holding this lock. Retired generations stay leased by readers.
func (m *Manager) install(next *routes) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	old := m.table.Swap(next)
	for dest, o := range old.entries {
		if next.entries[dest] == o {
			continue
		}
		if o.refs != 0 {
			m.retired = append(m.retired, o)
		}
	}
}
func (m *Manager) release(o *opened) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	o.refs--
	if o.refs == 0 && slices.Contains(m.retired, o) {
		m.retired = slices.DeleteFunc(m.retired, func(v *opened) bool { return v == o })
	}
}
func (m *Manager) alias(p Publication) error {
	dest := filepath.Join(m.Root, filepath.FromSlash(p.Dest))
	if filepath.Clean(dest) == filepath.Clean(p.Current.Directory) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	if st, err := os.Lstat(dest); err == nil && st.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("发布链接被非链接文件/目录占用: %s", dest)
	}
	rel, err := filepath.Rel(filepath.Dir(dest), p.Current.Directory)
	if err != nil {
		return err
	}
	tmp := dest + ".publish-" + uuid.New().String()
	if err = os.Symlink(rel, tmp); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err = os.Rename(tmp, dest); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dest))
}
func syncDir(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (m *Manager) Work(id int64) string {
	return filepath.Join(m.Root, ".mirrordock", "work", fmt.Sprint(id))
}
func (m *Manager) Prepare(ctx context.Context, s config.Source, id int64) (string, error) {
	if check := m.ProbeReflink(ctx); check.Status != "supported" {
		return "", errors.New(check.Detail)
	}
	if err := m.CheckSpace(); err != nil {
		return "", err
	}
	work := m.Work(id)
	marker := filepath.Join(work, ".mirrordock-prepared")
	if st, err := os.Stat(marker); err == nil && st.Mode().IsRegular() {
		return work, nil
	}
	if err := os.MkdirAll(work, 0755); err != nil {
		return "", err
	}
	baseGeneration := ""
	if p := m.table.Load().entries[s.Dest]; p != nil {
		baseGeneration = p.Publication.Current.ID
		if err := m.copyTree(ctx, p.Publication.Current.Directory, work); err != nil {
			return "", err
		}
	}
	fingerprint, err := treeFingerprint(ctx, work)
	if err != nil {
		return "", err
	}
	prepared, err := json.Marshal(preparation{Fingerprint: fingerprint, BaseGeneration: baseGeneration})
	if err != nil {
		return "", err
	}
	if err := config.AtomicWrite(marker, prepared, 0600); err != nil {
		return "", err
	}
	return work, nil
}
func (m *Manager) copyTree(ctx context.Context, src, dest string) error {
	links := map[string]string{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == ".mirrordock-prepared" || rel == ".mirrordock-manifest.json" {
			return nil
		}
		target := filepath.Join(dest, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0700)
		case d.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if current, e := os.Readlink(target); e == nil && current == link {
				return nil
			}
			if err = os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			if _, e := os.Lstat(target); e == nil {
				if err = os.Remove(target); err != nil {
					return err
				}
			}
			identity := hardlinkIdentity(info)
			if first := links[identity]; identity != "" && first != "" {
				// Link only to another clone inside work, never to published data.
				return os.Link(first, target)
			}
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			if err := m.clone(in, target); err != nil {
				_ = os.Remove(target)
				return fmt.Errorf("reflink 克隆失败（未回退完整复制）%s：%w", rel, err)
			}
			if identity != "" {
				links[identity] = target
			}
			if err := os.Chmod(target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chtimes(target, info.ModTime(), info.ModTime())
		default:
			return nil
		}
	})
	if err != nil {
		return err
	}
	// Restore directory metadata after all children are created. The root's
	// timestamp is intentionally excluded from no-change detection: private
	// preparation markers change it without changing the public mirror.
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if err := os.Chmod(target, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chtimes(target, info.ModTime(), info.ModTime())
	})
}
func (m *Manager) Publish(ctx context.Context, s config.Source, id int64) (Publication, error) {
	if err := ctx.Err(); err != nil {
		return Publication{}, err
	}
	work := m.Work(id)
	if err := Validate(ctx, work, s.Type); err != nil {
		return Publication{}, err
	}
	if old := m.table.Load().entries[s.Dest]; old != nil {
		baseline := readPreparation(work)
		if baseline.Fingerprint != "" && baseline.BaseGeneration == old.Publication.Current.ID {
			fingerprint, err := treeFingerprint(ctx, work)
			if err != nil {
				return Publication{}, err
			}
			if fingerprint == baseline.Fingerprint {
				if s.Type == "github_release" {
					if err := m.refreshReleaseSummary(ctx, s, work, old.Publication.Current.ID); err != nil {
						return Publication{}, err
					}
				}
				if err := m.RemoveWork(ctx, id); err != nil {
					return Publication{}, err
				}
				return m.table.Load().entries[s.Dest].Publication, nil
			}
		}
	}
	if err := m.CheckSpace(); err != nil {
		return Publication{}, err
	}
	summary, err := summarizeDirectory(ctx, work, s.Type)
	if err != nil {
		return Publication{}, err
	}
	if err := flushTree(ctx, work); err != nil {
		return Publication{}, err
	}
	if err := ctx.Err(); err != nil {
		return Publication{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	g := Generation{ID: uuid.New().String(), PublishedAt: time.Now().UTC(), Summary: summary}
	g.Directory = filepath.Join(m.Root, ".mirrordock", "published", g.ID)
	p := Publication{SourceID: s.ID, Type: s.Type, Dest: s.Dest, Current: g}
	if old := m.table.Load().entries[s.Dest]; old != nil {
		previous := old.Publication.Current
		previous.RetainUntil = time.Now().UTC().Add(24 * time.Hour)
		p.Previous = append([]Generation{previous}, old.Publication.Previous...)
	}
	// Until the durable pointer commits, requests retain the previous roots.
	// Any failure restores the alias and keeps staging data resumable.
	if err := os.Rename(work, g.Directory); err != nil {
		return p, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Rename(g.Directory, work)
		}
	}()
	if err := syncDir(filepath.Dir(g.Directory)); err != nil {
		return p, err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	if err = config.AtomicWrite(filepath.Join(g.Directory, ".mirrordock-manifest.json"), b, 0600); err != nil {
		return p, err
	}
	opened, err := m.openPublication(p)
	if err != nil {
		return p, err
	}
	old := m.table.Load().entries[s.Dest]
	if err = m.alias(p); err != nil {
		return p, err
	}
	rollbackAlias := func() {
		if old != nil {
			_ = m.alias(old.Publication)
		} else {
			_ = os.Remove(filepath.Join(m.Root, filepath.FromSlash(s.Dest)))
		}
	}
	if err = ctx.Err(); err != nil {
		rollbackAlias()
		return p, err
	}
	if err = m.store.SavePublicationContext(ctx, s.ID, p); err != nil {
		rollbackAlias()
		return p, err
	}
	committed = true
	m.installOpened(opened)
	return p, nil
}

// Sync data before committing a durable publication pointer. Hashing package
// bodies is unnecessary here; directory entries are synced after their files.
func flushTree(ctx context.Context, dir string) error {
	dirs := []string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("发布包含非常规文件: %s", p)
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		return errors.Join(f.Sync(), f.Close())
	})
	if err != nil {
		return err
	}
	for _, p := range slices.Backward(dirs) {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = syncDir(p); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) Publications() []Publication {
	out := []Publication{}
	for _, p := range m.table.Load().entries {
		out = append(out, p.Publication)
	}
	return out
}
func (m *Manager) Restrict(sources []config.Source) {
	m.mu.Lock()
	defer m.mu.Unlock()
	allowed := map[string]string{}
	for _, s := range sources {
		allowed[s.Dest] = s.ID
	}
	next := &routes{entries: map[string]*opened{}}
	for dest, o := range m.table.Load().entries {
		if allowed[dest] == o.Publication.SourceID {
			next.entries[dest] = o
		}
	}
	m.install(next)
}
func (m *Manager) Stats() map[string]int64 {
	return map[string]int64{"active": m.active.Load(), "requests": m.requests.Load(), "bytes": m.bytes.Load(), "capacity": int64(cap(m.gates) + cap(m.metadataGates)), "file_capacity": int64(cap(m.gates)), "metadata_capacity": int64(cap(m.metadataGates))}
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.install(&routes{entries: map[string]*opened{}})
	return m.roots.close()
}

// Collect expires logical fallbacks and then removes only private, unreferenced
// generations. Active requests keep their open roots, including old fallbacks.
func (m *Manager) Collect(now time.Time) error { return m.CollectContext(context.Background(), now) }
func (m *Manager) Busy() bool                  { return m.active.Load() > int64(cap(m.gates)*3/4) }
func (m *Manager) CollectContext(ctx context.Context, now time.Time) error {
	m.collectMu.Lock()
	defer m.collectMu.Unlock()
	m.mu.Lock()
	locked := true
	defer func() {
		if locked {
			m.mu.Unlock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, o := range m.table.Load().entries {
		p := o.Publication
		p.Previous = slices.DeleteFunc(slices.Clone(p.Previous), func(g Generation) bool { return !g.RetainUntil.IsZero() && !now.Before(g.RetainUntil) })
		if len(p.Previous) == len(o.Publication.Previous) {
			continue
		}
		if err := m.store.SavePublication(p.SourceID, p); err != nil {
			return err
		}
		if err := m.register(p); err != nil {
			return err
		}
	}
	m.routeMu.RLock()
	live := map[string]bool{}
	for _, o := range m.table.Load().entries {
		for _, g := range o.generations {
			live[g.ID] = true
		}
	}
	for _, o := range m.retired {
		for _, g := range o.generations {
			live[g.ID] = true
		}
	}
	m.routeMu.RUnlock()
	// Deleting configuration removes routes, never its persistent publication.
	// Protect all durable pointers, including sources no longer configured.
	records, err := m.store.Publications()
	if err != nil {
		return err
	}
	for _, record := range records {
		var p Publication
		if err = json.Unmarshal(record, &p); err != nil {
			return err
		}
		live[p.Current.ID] = true
		for _, g := range p.Previous {
			live[g.ID] = true
		}
	}
	dir := filepath.Join(m.Root, ".mirrordock", "published")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	m.mu.Unlock()
	locked = false
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return err
		}
		collecting := strings.HasPrefix(entry.Name(), ".gc-")
		if live[entry.Name()] || !entry.IsDir() || (strings.HasPrefix(entry.Name(), ".") && !collecting) {
			continue
		}
		st, err := entry.Info()
		if err != nil {
			return err
		}
		if !collecting && now.Sub(st.ModTime()) < 24*time.Hour {
			continue
		}
		target := filepath.Join(dir, entry.Name())
		if rel, err := filepath.Rel(m.Root, target); err == nil {
			m.roots.forget(rel)
		}
		if !collecting {
			garbage := filepath.Join(dir, ".gc-"+entry.Name())
			if err = os.Rename(target, garbage); err != nil {
				return err
			}
			target = garbage
		}
		if err = removeGeneration(ctx, target); err != nil {
			return err
		}
	}
	return nil
}

func removeGeneration(ctx context.Context, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	var walk func(string) error
	walk = func(relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := root.Chmod(relative, 0700); err != nil {
			return err
		}
		f, err := root.Open(relative)
		if err != nil {
			return err
		}
		defer f.Close()
		for {
			entries, e := f.ReadDir(128)
			if e != nil && !errors.Is(e, io.EOF) {
				return e
			}
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				name := filepath.Join(relative, entry.Name())
				if entry.IsDir() {
					if err := walk(name); err != nil {
						return err
					}
				}
				if err := root.Remove(name); err != nil {
					return err
				}
			}
			// Yield between full batches so reclamation remains cooperative
			// with downloads and can stop promptly during service shutdown.
			if len(entries) == 128 {
				timer := time.NewTimer(time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
			if errors.Is(e, io.EOF) {
				break
			}
		}
		return nil
	}
	if err = walk("."); err != nil {
		return err
	}
	if err = root.Close(); err != nil {
		return err
	}
	return os.Remove(dir)
}
