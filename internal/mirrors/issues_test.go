package mirrors

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"mirrordock/internal/config"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPackagesOnlyReposyncPublishes(t *testing.T) {
	m, source := testManager(t)
	source.Type = "reposync"
	source.RepoSync.DownloadMetadata = new(false)
	work, err := m.Prepare(t.Context(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "fixture.rpm"), []byte("package"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(t.Context(), source, 1); err != nil {
		t.Fatal(err)
	}
	source.RepoSync.DownloadMetadata = new(true)
	if _, err := m.Prepare(t.Context(), source, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(t.Context(), source, 2); err == nil {
		t.Fatal("complete repository accepted without metadata")
	}
}

func TestAPTCompressedAndPartialIndexes(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) {
		t.Helper()
		name = filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	packages := []byte("Package: fixture\nFilename: pool/fixture.deb\nSize: 3\n\n")
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(packages); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	// Debian format requires hashes for the absent uncompressed form, too.
	release := fmt.Sprintf("Suite: stable\nSHA256:\n %x %d main/binary-amd64/Packages\n %x %d main/binary-amd64/Packages.gz\n %x 3 main/binary-arm64/Packages.xz\n", sha256.Sum256(packages), len(packages), sha256.Sum256(compressed.Bytes()), compressed.Len(), sha256.Sum256([]byte("arm")))
	write("dists/stable/InRelease", []byte(release))
	write("dists/stable/Release", []byte("SHA256:\n invalid invalid missing\n")) // not revalidated
	write("dists/stable/main/binary-amd64/Packages.gz", compressed.Bytes())
	write("pool/fixture.deb", []byte("deb"))
	write("assets/Release", bytes.Repeat([]byte("x"), 9<<20)) // unrelated same-name file
	if err := Validate(t.Context(), dir, "rsync"); err != nil {
		t.Fatal(err)
	}
	write("pool/fixture.deb", []byte("truncated"))
	if err := Validate(t.Context(), dir, "rsync"); err == nil {
		t.Fatal("missing/corrupt referenced package accepted")
	}
}

func TestReconcileRestoresRenamesAndImports(t *testing.T) {
	m, source := testManager(t)
	work, err := m.Prepare(t.Context(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "asset"), []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	pub, err := m.Publish(t.Context(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	m.Restrict(nil)
	replacement := source
	replacement.ID = "replacement"
	if err := m.Reconcile(t.Context(), []config.Source{replacement}); err != nil {
		t.Fatal(err)
	}
	check := func(dest string) {
		t.Helper()
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest("GET", "/"+dest+"/asset", nil))
		if w.Code != 200 || w.Body.String() != "first" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	check(source.Dest)
	replacement.Dest = "renamed"
	if err := m.Reconcile(t.Context(), []config.Source{replacement}); err != nil {
		t.Fatal(err)
	}
	check("renamed")
	if _, err := os.Lstat(filepath.Join(m.Root, source.Dest)); !os.IsNotExist(err) {
		t.Fatal("stale alias survived", err)
	}
	if m.Publications()[0].Current.ID != pub.Current.ID {
		t.Fatal("dest change lost base generation")
	}
	imported := config.Source{ID: "imported", Type: "rsync", Dest: "existing"}
	if err := os.Mkdir(filepath.Join(m.Root, "existing"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Root, "existing", "asset"), []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(t.Context(), []config.Source{replacement, imported}); err != nil {
		t.Fatal(err)
	}
	check("existing")
	reopened, err := New(m.Root, m.store, []config.Source{replacement, imported})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.Publications()) != 2 {
		t.Fatal("publications not restored")
	}
}

func TestStartupAdoptsDeletedSourcePublication(t *testing.T) {
	m, s := testManager(t)
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "file"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := m.Publish(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	s.ID = "new-id"
	next, err := New(m.Root, m.store, []config.Source{s})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.Publications()[0].Current.ID != p.Current.ID {
		t.Fatal("lost retained generation")
	}
}

func TestStartupCleansOnlyReservedTemporaryDirectories(t *testing.T) {
	m, source := testManager(t)
	removed := []string{"work/.discard-7-1", "work/.reflink-probe-fixture", "published/.reflink-probe-fixture"}
	retained := []string{"work/unknown", "work/dnf-private-fixture", "work/12345", "published/unknown"}
	for _, name := range append(append([]string{}, removed...), retained...) {
		dir := filepath.Join(m.Root, ".mirrordock", filepath.FromSlash(name))
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	next, err := New(m.Root, m.store, []config.Source{source})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	for _, name := range removed {
		if _, err := os.Stat(filepath.Join(m.Root, ".mirrordock", filepath.FromSlash(name))); !os.IsNotExist(err) {
			t.Fatal(name, err)
		}
	}
	for _, name := range retained {
		if _, err := os.Stat(filepath.Join(m.Root, ".mirrordock", filepath.FromSlash(name), "file")); err != nil {
			t.Fatal(name, err)
		}
	}
}

func TestReconcileIsolatesBrokenDestinations(t *testing.T) {
	m, source := testManager(t)
	for _, name := range []string{"placeholder", "existing"} {
		if err := os.Mkdir(filepath.Join(m.Root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(m.Root, "existing", "asset"), []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Root, "occupied"), []byte("user file"), 0644); err != nil {
		t.Fatal(err)
	}
	placeholder := config.Source{ID: "placeholder", Type: "rsync", Dest: "placeholder"}
	occupied := config.Source{ID: "occupied", Type: "rsync", Dest: "occupied"}
	existing := config.Source{ID: "existing", Type: "rsync", Dest: "existing"}
	sources := []config.Source{source, occupied, placeholder, existing}
	err := m.Reconcile(t.Context(), sources)
	if err == nil || !onlySourceErrors(err) {
		t.Fatal("broken destination was not reported per source", err)
	}
	if issues := m.Issues(); len(issues) != 1 || issues[0].SourceID != "occupied" {
		t.Fatal(issues)
	}
	// Sources after the broken one are still imported.
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/existing/asset", nil))
	if w.Code != 200 || w.Body.String() != "first" {
		t.Fatal("later source was skipped", w.Code)
	}
	reopened, err := New(m.Root, m.store, sources)
	if err != nil {
		t.Fatal("startup failed for one broken destination", err)
	}
	reopened.Close()
	// The empty placeholder is replaced by the first publication alias.
	work, err := m.Prepare(t.Context(), placeholder, 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "asset"), []byte("synced"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(t.Context(), placeholder, 7); err != nil {
		t.Fatal("placeholder blocked publication", err)
	}
	if st, err := os.Lstat(filepath.Join(m.Root, "placeholder")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("placeholder was not replaced by alias", err)
	}
	if data, err := os.ReadFile(filepath.Join(m.Root, "occupied")); err != nil || string(data) != "user file" {
		t.Fatal("user content changed", err)
	}
}

func TestSourceTypeChangeKeepsServingButStartsFresh(t *testing.T) {
	m, source := testManager(t)
	work, err := m.Prepare(t.Context(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "rsync-only"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(t.Context(), source, 1); err != nil {
		t.Fatal(err)
	}
	retyped := source
	retyped.Type = "github_release"
	reopened, err := New(m.Root, m.store, []config.Source{retyped})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if p := reopened.Publications(); len(p) != 1 || p[0].Type != "rsync" {
		t.Fatal("retained generation was relabeled", p)
	}
	w := httptest.NewRecorder()
	reopened.ServeHTTP(w, httptest.NewRequest("GET", "/"+source.Dest+"/rsync-only", nil))
	if w.Code != 200 {
		t.Fatal("old content unavailable before the first new publication", w.Code)
	}
	work, err = reopened.Prepare(t.Context(), retyped, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "rsync-only")); !os.IsNotExist(err) {
		t.Fatal("other source type cloned into new work tree", err)
	}
}

func TestPublishIgnoresLowSpace(t *testing.T) {
	m, source := testManager(t)
	work, err := m.Prepare(t.Context(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "asset"), []byte("done"), 0644); err != nil {
		t.Fatal(err)
	}
	m.space = func(string) (DiskSpace, error) { return DiskSpace{Total: 10 << 30, Available: 1}, nil }
	if _, err := m.Publish(t.Context(), source, 1); err != nil {
		t.Fatal("completed work discarded by low-space check", err)
	}
}
