// Package mirror manages mirror generations on disk: it prepares work
// directories, validates and publishes them, and serves published content and
// the public directory listing over HTTP.
package mirror

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"mirrordock/internal/config"
	"mirrordock/internal/database"
	"mirrordock/internal/timefmt"
)

// Generation is one published copy of a source directory.
type Generation struct {
	ID          string            `json:"id"`
	Directory   string            `json:"directory"`
	PublishedAt timefmt.Time      `json:"published_at"`
	RetainUntil timefmt.Time      `json:"retain_until,omitzero"`
	Summary     *DirectorySummary `json:"summary,omitzero"`
}

// Publication describes the generation currently published for a source.
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

// Public serving capacity. Small files (metadata, signatures) have their own
// slots so large package downloads cannot starve repository clients.
const (
	largeDownloadSlots   = 256
	smallDownloadSlots   = 64
	smallDownloadBytes   = 256 << 10
	listingSlots         = 8
	metadataCacheEntries = 8192
	maxListedEntries     = 1000
	generationRetention  = 24 * time.Hour
)

// Manager manages work directories and published generations under Root and
// serves them.
type Manager struct {
	now    func() time.Time
	Root   string
	store  *database.Store
	logger *slog.Logger

	// mu serializes changes to publications and generations (reconcile,
	// publish, collect, Close); routeMu guards route leases, the table swap
	// and retired routes.
	mu        sync.Mutex
	routeMu   sync.RWMutex
	probeMu   sync.Mutex
	collectMu sync.Mutex
	storageMu sync.Mutex
	workMu    sync.Mutex

	// syncing holds the run IDs admitted by BeginSync; guarded by storageMu.
	syncing map[int64]bool
	clone   func(*os.File, string) error
	space   func(string) (DiskSpace, error)

	table   atomic.Pointer[routes]
	dests   atomic.Pointer[map[string]bool] // configured dests, routed or not
	retired []*opened

	// Semaphores bounding public serving: GET slots for files above and up
	// to smallDownloadBytes, directory listing renders, and open descriptors.
	largeDownloads chan struct{}
	smallDownloads chan struct{}
	listingRenders chan struct{}
	openFiles      chan struct{}

	// Download counters reported by Stats.
	activeDownloads atomic.Int64
	requests        atomic.Int64
	sentBytes       atomic.Int64

	cache       *metadataCache
	roots       *rootCache
	ListingInfo func(context.Context) map[string]SourceListingInfo
	issueMu     sync.Mutex
	issues      []SourceIssue
}

// New opens the mirror root and reconciles the stored publications with
// sources.
func New(ctx context.Context, params NewOptions) (*Manager, error) {
	root := params.Root
	store := params.Store
	sources := params.Sources
	logger := params.Logger

	if err := os.MkdirAll(filepath.Join(root, ".mirrordock", "work"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, ".mirrordock", "published"), 0o700); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	files, _ := DescriptorLimits()
	m := &Manager{
		now:            time.Now,
		logger:         logger,
		Root:           canonical,
		store:          store,
		largeDownloads: make(chan struct{}, largeDownloadSlots),
		smallDownloads: make(chan struct{}, smallDownloadSlots),
		listingRenders: make(chan struct{}, listingSlots),
		openFiles:      make(chan struct{}, files),
		cache:          newCache(metadataCacheEntries),
		syncing:        map[int64]bool{},
		clone:          cloneFile,
		space:          diskSpace,
	}
	m.table.Store(&routes{entries: map[string]*opened{}})
	m.roots, err = newRootCache(canonical, rootCacheLimit())
	if err != nil {
		return nil, err
	}
	if err := m.cleanupStaleTemporary(ctx); err != nil {
		return nil, errors.Join(err, m.Close())
	}
	m.ProbeReflink(ctx) // the probe caches an explicit supported/unsupported/error result
	ready := false
	defer func() {
		if !ready {
			if err := m.Close(); err != nil {
				logger.Error("mirror_open_cleanup_failed", "error", err)
			}
		}
	}()
	// One unusable destination must not keep every other mirror offline.
	// Per-source failures stay visible through Issues and the program log.
	if err := m.Reconcile(ctx, sources); err != nil && !onlySourceErrors(err) {
		return nil, err
	} else if err != nil {
		logger.Error("publication_reconcile_failed", "error", err)
	}

	ready = true
	return m, nil
}

// SourceError reports a publication that could not be restored or imported.
// Other configured sources are still reconciled.
type SourceError struct {
	SourceID string
	Err      error
}

// Error implements error.
func (e *SourceError) Error() string { return e.SourceID + ": " + e.Err.Error() }

// Unwrap returns the underlying error.
func (e *SourceError) Unwrap() error { return e.Err }

// SourceIssue is the latest reconciliation failure of one configured source.
// Code is the client's text key; Error is the English cause.
type SourceIssue struct {
	SourceID string `json:"source_id"`
	Code     string `json:"code"`
	Error    string `json:"error"`
}

// Reasons an existing dest directory cannot be imported as a publication.
var (
	ErrDestNotDirectory = errors.New("dest is occupied by a non-directory")
	ErrDestOutsideRoot  = errors.New("dest resolves outside the mirror root; import refused")
	ErrDestOrphan       = errors.New("dest points to an internal generation without a database record; restore the database backup")
)

// issueCode is the client's text key for a reconciliation failure.
func issueCode(err error) string {
	switch {
	case errors.Is(err, ErrDestNotDirectory):
		return "dest_not_directory"
	case errors.Is(err, ErrDestOutsideRoot):
		return "dest_outside_root"
	case errors.Is(err, ErrDestOrphan):
		return "dest_orphan_generation"
	}
	if _, ok := errors.AsType[*ValidationError](err); ok {
		return "import_invalid"
	}
	return "import_failed"
}

func onlySourceErrors(err error) bool {
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		_, ok := errors.AsType[*SourceError](err)
		return ok
	}
	for _, e := range joined.Unwrap() {
		if _, ok := e.(*SourceError); !ok {
			return false
		}
	}
	return true
}

// Issues returns the per-source failures left by the latest reconciliation.
func (m *Manager) Issues() []SourceIssue {
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	return slices.Clone(m.issues)
}

// Reconcile restores retained publications and imports existing directories for
// both startup and configuration changes. It serializes with publication/GC.
// A failing source is reported as a SourceError and does not stop the others.
func (m *Manager) Reconcile(ctx context.Context, sources []config.Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restrictLocked(sources)
	configured := map[string]bool{}
	for _, source := range sources {
		configured[source.ID] = true
	}
	records, err := m.store.Publications(ctx)
	if err != nil {
		return err
	}
	byID := map[string]Publication{}
	for _, document := range records {
		var p Publication
		if err = json.Unmarshal(document, &p); err != nil {
			return err
		}
		byID[p.SourceID] = p
	}
	var failures []error
	issues := []SourceIssue{}
	for _, s := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.reconcileSource(ctx, s, byID, configured); err != nil {
			if ctx.Err() != nil {
				return errors.Join(ctx.Err(), err)
			}
			failures = append(failures, &SourceError{SourceID: s.ID, Err: err})
			issues = append(issues, SourceIssue{SourceID: s.ID, Code: issueCode(err), Error: err.Error()})
		}
	}
	m.issueMu.Lock()
	m.issues = issues
	m.issueMu.Unlock()
	return errors.Join(failures...)
}

// adoptRemoved finds the publication of a removed source that served the
// same dest, so re-adding a source (even under a new ID) restores its route.
// Several removed IDs may retain this destination: prefer the one the
// current alias points to, otherwise the newest committed generation.
func (m *Manager) adoptRemoved(
	s config.Source,
	byID map[string]Publication,
	configured map[string]bool,
) (Publication, bool) {
	aliasTarget, _ := filepath.EvalSymlinks(filepath.Join(m.Root, filepath.FromSlash(s.Dest)))
	var chosen Publication
	found := false
	for _, previous := range byID {
		ownedElsewhere := configured[previous.SourceID]
		otherType := previous.Type != "" && previous.Type != s.Type
		if previous.Dest != s.Dest || ownedElsewhere || otherType {
			continue
		}
		matchesAlias := previous.Current.Directory == aliasTarget
		chosenMatchesAlias := chosen.Current.Directory == aliasTarget
		sameTime := previous.Current.PublishedAt.Time().Equal(chosen.Current.PublishedAt.Time())
		newer := previous.Current.PublishedAt.Time().After(chosen.Current.PublishedAt.Time()) ||
			(sameTime && previous.SourceID < chosen.SourceID)
		if !found || (matchesAlias && !chosenMatchesAlias) || (matchesAlias == chosenMatchesAlias && newer) {
			chosen, found = previous, true
		}
	}
	if found {
		chosen.SourceID = s.ID
	}
	return chosen, found
}

func (m *Manager) reconcileSource(
	ctx context.Context,
	s config.Source,
	byID map[string]Publication,
	configured map[string]bool,
) error {
	p, ok := byID[s.ID]
	oldDest := ""
	if ok && p.Dest != s.Dest {
		oldDest = p.Dest
		p.Dest = s.Dest
	}
	if !ok {
		p, ok = m.adoptRemoved(s, byID, configured)
	}
	// A changed source type keeps serving its retained content; Prepare starts
	// the new type from an empty tree instead of cloning the other layout.
	if live := m.table.Load().entries[s.Dest]; live != nil && live.Publication.SourceID == s.ID {
		return nil
	}
	if !ok {
		imported, found, err := m.importDirectory(ctx, s)
		if err != nil || !found {
			return err
		}
		p = imported
	}
	if p.Type == "" {
		p.Type = s.Type
	}
	if p.Current.Summary == nil {
		real, err := filepath.EvalSymlinks(p.Current.Directory)
		if err != nil || !config.Within(m.Root, real) {
			return errors.New("publication escaped mirror root")
		}
		summary, err := summarizeDirectory(ctx, p.Current.Directory, p.Type)
		if err != nil {
			return err
		}
		p.Current.Summary = summary
		if err = m.store.SavePublication(ctx, s.ID, p); err != nil {
			return err
		}
	}
	opened, err := m.openPublication(p)
	if err != nil {
		return err
	}
	if err = m.alias(p); err != nil {
		return err
	}
	if err = m.store.SavePublication(ctx, s.ID, p); err != nil {
		return err
	}
	m.installOpened(opened)
	if oldDest != "" { // Remove only our own stale alias, never user content.
		oldPath := filepath.Join(m.Root, filepath.FromSlash(oldDest))
		if target, err := filepath.EvalSymlinks(oldPath); err == nil && target == p.Current.Directory {
			if info, err := os.Lstat(oldPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(oldPath); err != nil {
					return err
				}
				if err := syncDir(filepath.Dir(oldPath)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// importDirectory turns an existing, non-empty dest directory into the first
// generation of s. It reports false when there is nothing to import.
func (m *Manager) importDirectory(ctx context.Context, s config.Source) (Publication, bool, error) {
	local := filepath.Join(m.Root, filepath.FromSlash(s.Dest))
	info, err := os.Stat(local)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Publication{}, false, nil
	case err != nil:
		return Publication{}, false, err
	case !info.IsDir():
		return Publication{}, false, ErrDestNotDirectory
	}
	real, err := filepath.EvalSymlinks(local)
	if err != nil || !config.Within(m.Root, real) {
		return Publication{}, false, ErrDestOutsideRoot
	}
	if config.Within(filepath.Join(m.Root, ".mirrordock"), real) {
		return Publication{}, false, ErrDestOrphan
	}
	// A pre-created empty destination is a placeholder, not a mirror.
	// The first successful publication replaces it with the alias.
	entries, err := os.ReadDir(real)
	if err != nil || len(entries) == 0 {
		return Publication{}, false, err
	}
	if err = ValidateSource(ctx, real, s); err != nil {
		return Publication{}, false, fmt.Errorf("import existing directory: %w", err)
	}
	generation := m.newGeneration(nil)
	// Normalize an imported directory into an immutable generation before
	// creating its public alias. Never subsequently copy into the live tree.
	if err = os.Rename(real, generation.Directory); err != nil {
		return Publication{}, false, err
	}
	p := Publication{SourceID: s.ID, Type: s.Type, Dest: s.Dest, Current: generation}
	if err = m.store.SavePublication(ctx, s.ID, p); err != nil {
		// A failed import must leave the original directory available for
		// the next startup rather than orphaning it in a private generation.
		return Publication{}, false, errors.Join(err, os.Rename(generation.Directory, real))
	}
	return p, true, nil
}

// newGeneration names a fresh private generation directory.
func (m *Manager) newGeneration(summary *DirectorySummary) Generation {
	id := uuid.New().String()
	return Generation{
		ID:          id,
		Directory:   filepath.Join(m.Root, ".mirrordock", "published", id),
		PublishedAt: timefmt.From(m.now()),
		Summary:     summary,
	}
}

// openPublication builds the routing entry of p after checking that the
// current generation can be opened inside the mirror root.
func (m *Manager) openPublication(p Publication) (*opened, error) {
	route := &opened{Publication: p}
	for _, generation := range append([]Generation{p.Current}, p.Previous...) {
		rel, err := m.generationPath(generation)
		if err != nil {
			return nil, err
		}
		if generation.ID == p.Current.ID {
			root, err := m.openGeneration(generation)
			if err != nil {
				return nil, err
			}
			if err := root.Close(); err != nil {
				return nil, err
			}
		}
		route.generations = append(route.generations, generation)
		route.paths = append(route.paths, rel)
	}
	return route, nil
}

func (m *Manager) generationPath(generation Generation) (string, error) {
	rel, err := filepath.Rel(m.Root, generation.Directory)
	if err != nil || !config.Within(m.Root, generation.Directory) {
		return "", errors.New("publication escaped mirror root")
	}
	return rel, nil
}

// Publication validation uses a transient root; requests use the bounded cache.
func (m *Manager) openGeneration(generation Generation) (*os.Root, error) {
	rel, err := m.generationPath(generation)
	if err != nil {
		return nil, err
	}
	return m.roots.anchor.OpenRoot(rel)
}

// installOpened publishes route under its dest in a copied routing table.
func (m *Manager) installOpened(route *opened) {
	next := &routes{entries: maps.Clone(m.table.Load().entries)}
	next.entries[route.Publication.Dest] = route
	m.install(next)
}

func (m *Manager) register(p Publication) error {
	route, err := m.openPublication(p)
	if err != nil {
		return err
	}
	m.installOpened(route)
	return nil
}

// install takes only the routing lock; SQLite writes and filesystem work never
// block requests holding this lock. Retired generations stay leased by readers.
func (m *Manager) install(next *routes) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	old := m.table.Swap(next)
	for dest, route := range old.entries {
		if next.entries[dest] == route {
			continue
		}
		if route.refs != 0 {
			m.retired = append(m.retired, route)
		}
	}
}

// release ends one request lease of route.
func (m *Manager) release(route *opened) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	route.refs--
	if route.refs == 0 {
		m.retired = slices.DeleteFunc(m.retired, func(retired *opened) bool { return retired == route })
	}
}

func (m *Manager) alias(p Publication) error {
	dest := filepath.Join(m.Root, filepath.FromSlash(p.Dest))
	if filepath.Clean(dest) == filepath.Clean(p.Current.Directory) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(dest); err == nil && info.Mode()&os.ModeSymlink == 0 {
		// Only an empty placeholder directory may be replaced; os.Remove never
		// deletes a non-empty directory, so user content stays untouched.
		if !info.IsDir() || os.Remove(dest) != nil {
			return fmt.Errorf("publication link is occupied by a non-link file or directory: %s", dest)
		}
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
	defer f.Close() // Read-only descriptor; closing cannot discard buffered writes.
	return f.Sync()
}

// Work returns the work directory of run id.
func (m *Manager) Work(id int64) string {
	return filepath.Join(m.Root, ".mirrordock", "work", fmt.Sprint(id))
}

// Prepare creates the work directory of run id for source s and returns its
// path.
func (m *Manager) Prepare(ctx context.Context, s config.Source, id int64) (string, error) {
	work, _, err := m.PrepareReuse(ctx, s, id)
	return work, err
}

// PrepareReuse also reports the earlier run whose kept work seeded this one.
func (m *Manager) PrepareReuse(ctx context.Context, s config.Source, id int64) (string, int64, error) {
	if check := m.ProbeReflink(ctx); check.Status != "supported" {
		return "", 0, errors.New(check.Detail)
	}
	if err := m.CheckSpace(); err != nil {
		return "", 0, err
	}
	work := m.Work(id)
	marker := filepath.Join(work, ".mirrordock-prepared")
	if info, err := os.Stat(marker); err == nil && info.Mode().IsRegular() {
		return work, 0, nil
	}
	if _, err := os.Lstat(work); errors.Is(err, os.ErrNotExist) {
		reused, err := m.adoptWork(ctx, s, id)
		if err != nil || reused > 0 {
			return work, reused, err
		}
	} else if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return "", 0, err
	}
	baseGeneration := ""
	if route := m.table.Load().entries[s.Dest]; route != nil && route.Publication.Type == s.Type {
		baseGeneration = route.Publication.Current.ID
		if err := m.copyTree(ctx, route.Publication.Current.Directory, work); err != nil {
			return "", 0, err
		}
	}
	fingerprint, err := treeFingerprint(ctx, work)
	if err != nil {
		return "", 0, err
	}
	prepared, err := json.Marshal(preparation{Fingerprint: fingerprint, BaseGeneration: baseGeneration})
	if err != nil {
		return "", 0, err
	}
	if err := config.AtomicWrite(marker, prepared, 0o600); err != nil {
		return "", 0, err
	}
	return work, 0, nil
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
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case d.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if current, err := os.Readlink(target); err == nil && current == link {
				return nil
			}
			if err = os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			if _, err := os.Lstat(target); err == nil {
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
			defer in.Close() // Read-only descriptor; closing cannot discard buffered writes.
			if err := m.clone(in, target); err != nil {
				removeErr := os.Remove(target)
				if errors.Is(removeErr, os.ErrNotExist) {
					removeErr = nil
				}
				err = errors.Join(err, removeErr)
				return fmt.Errorf("reflink clone of %s failed (no full-copy fallback): %w", rel, err)
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

// Publish validates the work directory of run id and publishes it as the new
// generation of s.
func (m *Manager) Publish(ctx context.Context, s config.Source, id int64) (publication Publication, resultErr error) {
	if err := ctx.Err(); err != nil {
		return Publication{}, err
	}
	work := m.Work(id)
	if err := ValidateSource(ctx, work, s); err != nil {
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
	// Publication renames the completed work tree within the same filesystem
	// and needs no new space, so a low-space condition must not discard it.
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
	g := m.newGeneration(summary)
	p := Publication{SourceID: s.ID, Type: s.Type, Dest: s.Dest, Current: g}
	if old := m.table.Load().entries[s.Dest]; old != nil {
		previous := old.Publication.Current
		previous.RetainUntil = timefmt.From(m.now().Add(generationRetention))
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
			if err := os.Rename(g.Directory, work); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("restore unpublished work: %w", err))
			}
		}
	}()
	if err := syncDir(filepath.Dir(g.Directory)); err != nil {
		return p, err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	if err = config.AtomicWrite(filepath.Join(g.Directory, ".mirrordock-manifest.json"), b, 0o600); err != nil {
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
	rollbackAlias := func() error {
		if old != nil {
			return m.alias(old.Publication)
		}
		err := os.Remove(filepath.Join(m.Root, filepath.FromSlash(s.Dest)))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err = ctx.Err(); err != nil {
		return p, errors.Join(err, rollbackAlias())
	}
	if err = m.store.SavePublication(ctx, s.ID, p); err != nil {
		return p, errors.Join(err, rollbackAlias())
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
			return fmt.Errorf("publication contains a non-regular file: %s", p)
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

// Publications returns the publications currently served.
func (m *Manager) Publications() []Publication {
	out := []Publication{}
	for _, route := range m.table.Load().entries {
		out = append(out, route.Publication)
	}
	return out
}

// Restrict limits serving to the destinations of sources.
func (m *Manager) Restrict(sources []config.Source) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restrictLocked(sources)
}

func (m *Manager) restrictLocked(sources []config.Source) {
	allowed := map[string]string{}
	dests := map[string]bool{}
	for _, s := range sources {
		allowed[s.Dest] = s.ID
		dests[s.Dest] = true
	}
	m.dests.Store(&dests)
	next := &routes{entries: map[string]*opened{}}
	for dest, route := range m.table.Load().entries {
		if allowed[dest] == route.Publication.SourceID {
			next.entries[dest] = route
		}
	}
	m.install(next)
}

// Stats returns download, request and byte counters with the download capacity.
func (m *Manager) Stats() map[string]int64 {
	return map[string]int64{
		"active":            m.activeDownloads.Load(),
		"requests":          m.requests.Load(),
		"sent_bytes":        m.sentBytes.Load(),
		"capacity":          int64(cap(m.largeDownloads) + cap(m.smallDownloads)),
		"large_capacity":    int64(cap(m.largeDownloads)),
		"metadata_capacity": int64(cap(m.smallDownloads)),
	}
}

// Close stops serving every generation and closes their roots.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.install(&routes{entries: map[string]*opened{}})
	return m.roots.close()
}

// Busy reports that large downloads use more than three quarters of their
// slots; queued synchronizations then wait.
func (m *Manager) Busy() bool { return m.activeDownloads.Load() > int64(cap(m.largeDownloads)*3/4) }

// Collect is Collect with a context that can stop the removal.
func (m *Manager) Collect(ctx context.Context, now time.Time) error {
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
	expired := func(g Generation) bool { return !g.RetainUntil.IsZero() && !now.Before(g.RetainUntil.Time()) }
	for _, route := range m.table.Load().entries {
		p := route.Publication
		p.Previous = slices.DeleteFunc(slices.Clone(p.Previous), expired)
		if len(p.Previous) == len(route.Publication.Previous) {
			continue
		}
		if err := m.store.SavePublication(ctx, p.SourceID, p); err != nil {
			return err
		}
		if err := m.register(p); err != nil {
			return err
		}
	}
	m.routeMu.RLock()
	live := map[string]bool{}
	for _, route := range m.table.Load().entries {
		for _, generation := range route.generations {
			live[generation.ID] = true
		}
	}
	for _, route := range m.retired {
		for _, generation := range route.generations {
			live[generation.ID] = true
		}
	}
	m.routeMu.RUnlock()
	// Deleting configuration removes routes, never its persistent publication.
	// Protect all durable pointers, including sources no longer configured.
	records, err := m.store.Publications(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		var p Publication
		if err = json.Unmarshal(record, &p); err != nil {
			return err
		}
		live[p.Current.ID] = true
		for _, generation := range p.Previous {
			live[generation.ID] = true
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
		if !collecting && now.Sub(st.ModTime()) < generationRetention {
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

// removeBatch is the number of entries removed between cooperative yields.
const removeBatch = 128

// removeGeneration deletes a private tree through os.Root, so a symbolic
// link inside it can never redirect the removal outside dir.
func removeGeneration(ctx context.Context, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close() // Read-only descriptor; closing cannot discard buffered writes.
	var walk func(string) error
	walk = func(relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := root.Chmod(relative, 0o700); err != nil {
			return err
		}
		directory, err := root.Open(relative)
		if err != nil {
			return err
		}
		defer directory.Close() // Read-only descriptor; closing cannot discard buffered writes.
		for {
			entries, readErr := directory.ReadDir(removeBatch)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return readErr
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
			if len(entries) == removeBatch {
				timer := time.NewTimer(time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
			if errors.Is(readErr, io.EOF) {
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

// NewOptions groups the inputs to New.
type NewOptions struct {
	Root    string
	Store   *database.Store
	Sources []config.Source
	Logger  *slog.Logger
}
