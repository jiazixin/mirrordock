package mirror

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mirrordock/internal/config"
	"mirrordock/internal/database"
)

func TestImportDatabaseFailureRestoresOriginalDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := database.Open(t.Context(), filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "CREATE TRIGGER reject_publication BEFORE INSERT ON publications BEGIN SELECT RAISE(ABORT,'fixture failure'); END"}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "mirror")
	local := filepath.Join(root, "repo")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "package.rpm"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := []config.Source{{ID: "repo", Type: "rsync", Dest: "repo"}}
	manager, err := New(t.Context(), NewOptions{Root: root, Store: store, Sources: sources, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal("a per-source import failure must not stop startup", err)
	}
	defer manager.Close()
	if issues := manager.Issues(); len(issues) != 1 || issues[0].SourceID != "repo" {
		t.Fatal("import failure was not reported", issues)
	}
	data, readErr := os.ReadFile(filepath.Join(local, "package.rpm"))
	if readErr != nil || string(data) != "original" {
		t.Fatal("original mirror orphaned", readErr)
	}
	if _, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "DROP TRIGGER reject_publication"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(t.Context(), sources); err != nil {
		t.Fatal("next reconciliation could not import", err)
	}
	if len(manager.Issues()) != 0 || len(manager.Publications()) != 1 {
		t.Fatal("successful import left stale state", manager.Issues())
	}
}

func testManager(t *testing.T) (*Manager, config.Source) {
	t.Helper()
	dir := t.TempDir()
	store, err := database.Open(t.Context(), filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s := config.Source{ID: "repo", Name: "Test", Type: "rsync", Dest: "repo"}
	m, err := New(t.Context(), NewOptions{Root: filepath.Join(dir, "mirror"), Store: store, Sources: []config.Source{s}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m, s
}

func TestPublishedDownloadAndIsolation(t *testing.T) {
	t.Parallel()
	m, s := testManager(t)
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "file.rpm"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".treeinfo"), []byte("legal"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "a.part"), []byte("private"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(work, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m)
	defer server.Close()
	req, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/repo/file.rpm", nil)
	req.Header.Set("Range", "bytes=2-5")
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 206 || string(b) != "2345" {
		t.Fatal(res.StatusCode, string(b))
	}
	etag := res.Header.Get("ETag")
	req, _ = http.NewRequestWithContext(t.Context(), "GET", server.URL+"/repo/file.rpm", nil)
	req.Header.Set("If-None-Match", etag)
	res, err = (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 304 {
		t.Fatal(res.StatusCode)
	}
	for _, p := range []string{"/repo/a.part", "/repo/escape", "/.mirrordock/", "/repo/%2e%2e/secret"} {
		res, err = getTestResponse(t, server.URL+p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatalf("%s: %d", p, res.StatusCode)
		}
	}
	res, err = getTestResponse(t, server.URL+"/repo/.treeinfo")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("legal metadata inaccessible")
	}
}

func TestSaturatedDownloadsPreserveMetadataAndHEAD(t *testing.T) {
	t.Parallel()
	m, s := testManager(t)
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(work, "repomd.txt"), []byte("metadata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(work, "large.rpm"), make([]byte, 300<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	for range cap(m.largeDownloads) {
		m.largeDownloads <- struct{}{}
	}
	for _, check := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/repo/large.rpm", http.StatusServiceUnavailable},
		{"GET", "/repo/repomd.txt", http.StatusOK},
		{"HEAD", "/repo/large.rpm", http.StatusOK},
	} {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(check.method, check.path, nil))
		if w.Code != check.status {
			t.Fatalf("%s %s: got %d, want %d", check.method, check.path, w.Code, check.status)
		}
		if check.status == http.StatusServiceUnavailable && w.Header().Get("Retry-After") == "" {
			t.Fatal("saturated download must specify Retry-After")
		}
	}
}

func TestPublishKeepsOldFile(t *testing.T) {
	t.Parallel()
	m, s := testManager(t)
	for i := range 2 {
		work, err := m.Prepare(t.Context(), s, int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := os.WriteFile(filepath.Join(work, "old.rpm"), []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Remove(filepath.Join(work, "old.rpm")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, "new.rpm"), []byte("new"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = m.Publish(t.Context(), s, int64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/repo/old.rpm", nil))
	if w.Code != 200 || w.Body.String() != "old" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestMissingReferencedPackageRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "repodata"), 0o755); err != nil {
		t.Fatal(err)
	}
	primary := `<metadata><package><location href="missing.rpm"/><size package="3"/></package></metadata>`
	if err := os.WriteFile(filepath.Join(dir, "repodata", "primary.xml"), []byte(primary), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := `<repomd><data type="primary"><location href="repodata/primary.xml"/><size>` + fmtInt(len(primary)) + `</size></data></repomd>`
	if err := os.WriteFile(filepath.Join(dir, "repodata", "repomd.xml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(t.Context(), dir, "reposync"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatal(err)
	}
}

func fmtInt(n int) string { return strconv.Itoa(n) }

func TestCollectionRetainsActiveReader(t *testing.T) {
	t.Parallel()
	m, s := testManager(t)
	work, _ := m.Prepare(t.Context(), s, 1)
	if err := os.WriteFile(filepath.Join(work, "old.rpm"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := m.Publish(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	m.routeMu.Lock()
	lease := m.table.Load().entries[s.Dest]
	lease.refs++
	m.routeMu.Unlock()
	work, _ = m.Prepare(t.Context(), s, 2)
	if err := os.Remove(filepath.Join(work, "old.rpm")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "new.rpm"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Publish(t.Context(), s, 2); err != nil {
		t.Fatal(err)
	}
	if err = m.Collect(t.Context(), time.Now().Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	root, err := m.openGeneration(lease.generations[0])
	if err != nil {
		t.Fatal(err)
	}
	f, err := root.Open("old.rpm")
	root.Close()
	if err != nil {
		t.Fatal("active old root closed", err)
	}
	f.Close()
	if _, err = os.Stat(old.Current.Directory); err != nil {
		t.Fatal("active generation removed", err)
	}
	m.release(lease)
	if err = m.Collect(t.Context(), time.Now().Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(old.Current.Directory); !os.IsNotExist(err) {
		t.Fatal("expired generation retained", err)
	}
}

func BenchmarkPublicHTTP(b *testing.B) {
	dir := b.TempDir()
	store, err := database.Open(b.Context(), filepath.Join(dir, "data"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	s := config.Source{ID: "bench", Dest: "repo", Type: "rsync"}
	m, err := New(b.Context(), NewOptions{Root: filepath.Join(dir, "mirror"), Store: store, Sources: []config.Source{s}, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		b.Fatal(err)
	}
	defer m.Close()
	work, err := m.Prepare(b.Context(), s, 1)
	if err != nil {
		b.Fatal(err)
	}
	const size = 8 << 20
	f, err := os.Create(filepath.Join(work, "file.rpm"))
	if err != nil {
		b.Fatal(err)
	}
	if err = f.Truncate(size); err != nil {
		b.Fatal(err)
	}
	f.Close()
	if _, err = m.Publish(b.Context(), s, 1); err != nil {
		b.Fatal(err)
	}
	server := httptest.NewServer(m)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	b.SetBytes(size)
	b.ReportAllocs()
	for b.Loop() {
		req, err := http.NewRequestWithContext(b.Context(), "GET", server.URL+"/repo/file.rpm", nil)
		if err != nil {
			b.Fatal(err)
		}
		res, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		n, err := io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || n != size {
			b.Fatal(n, err, res.StatusCode)
		}
	}
}

func TestHTTPDownloadDoesNotRequireSQLite(t *testing.T) {
	t.Parallel()
	m, s := testManager(t)
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("package"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	if err = m.store.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/repo/pkg.rpm", nil))
	if w.Code != 200 || w.Body.String() != "package" {
		t.Fatal("download depends on database", w.Code, w.Body.String())
	}
}

func TestPrepareResumesIncompleteCopy(t *testing.T) {
	t.Parallel()
	m, s := testManager(t)
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "one.rpm"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "two.rpm"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	next := m.Work(2)
	if err := os.MkdirAll(next, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(next, "one.rpm"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	resumed, err := m.Prepare(t.Context(), s, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(resumed, "two.rpm"))
	if err != nil || string(b) != "two" {
		t.Fatal("incomplete preparation accepted", err)
	}
	b, err = os.ReadFile(filepath.Join(resumed, "one.rpm"))
	if err != nil || string(b) != "one" {
		t.Fatal("partial preparation not repaired", err)
	}
}

func TestRemovedSourceFilesSurviveCollection(t *testing.T) {
	t.Parallel()
	m, s := testManager(t)
	work, _ := m.Prepare(t.Context(), s, 1)
	if err := os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("package"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := m.Publish(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	m.Restrict(nil)
	if err = m.Collect(t.Context(), time.Now().Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(p.Current.Directory, "pkg.rpm")); err != nil {
		t.Fatal("removing configuration destroyed published files", err)
	}
}

func TestAliasFailureCannotPublish(t *testing.T) {
	t.Parallel()
	m, s := testManager(t)
	work, _ := m.Prepare(t.Context(), s, 1)
	if err := os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("package"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An empty placeholder may be replaced, but user content never is.
	if err := os.Mkdir(filepath.Join(m.Root, s.Dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Root, s.Dest, "user-file"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(t.Context(), s, 1); err == nil {
		t.Fatal("occupied alias accepted")
	}
	if data, err := os.ReadFile(filepath.Join(m.Root, s.Dest, "user-file")); err != nil || string(data) != "user" {
		t.Fatal("user content changed", err)
	}
	if len(m.Publications()) != 0 {
		t.Fatal("failed publication became visible")
	}
	if _, err := os.Stat(filepath.Join(work, "pkg.rpm")); err != nil {
		t.Fatal("failed staging not preserved", err)
	}
}

func TestFailedCommitRestoresAliasAndStaging(t *testing.T) {
	t.Parallel()
	m, s := testManager(t)
	work, _ := m.Prepare(t.Context(), s, 1)
	if err := os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	work, _ = m.Prepare(t.Context(), s, 2)
	if err := os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.store.Close()
	if _, err := m.Publish(t.Context(), s, 2); err == nil {
		t.Fatal("failed commit returned success")
	}
	b, err := os.ReadFile(filepath.Join(m.Root, s.Dest, "pkg.rpm"))
	if err != nil || string(b) != "old" {
		t.Fatal("failed commit changed alias", string(b), err)
	}
	b, err = os.ReadFile(filepath.Join(work, "pkg.rpm"))
	if err != nil || string(b) != "new" {
		t.Fatal("failed commit lost staging", string(b), err)
	}
}

func getTestResponse(t *testing.T, target string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET", target, nil)
	if err != nil {
		return nil, err
	}
	return (&http.Client{Timeout: 5 * time.Second}).Do(req)
}
