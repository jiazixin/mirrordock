package mirrors

import (
	"context"
	"io"
	"mirrordock/internal/config"
	"mirrordock/internal/database"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestImportDatabaseFailureRestoresOriginalDirectory(t *testing.T) {
	dir := t.TempDir()
	store, err := database.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec("CREATE TRIGGER reject_publication BEFORE INSERT ON publications BEGIN SELECT RAISE(ABORT,'fixture failure'); END"); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "mirror")
	local := filepath.Join(root, "repo")
	if err := os.MkdirAll(local, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "package.rpm"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	manager, err := New(root, store, []config.Source{{ID: "repo", Type: "rsync", Dest: "repo"}})
	if err == nil {
		manager.Close()
		t.Fatal("import should fail")
	}
	data, readErr := os.ReadFile(filepath.Join(local, "package.rpm"))
	if readErr != nil || string(data) != "original" {
		t.Fatal("original mirror orphaned", readErr)
	}
	if _, err := store.DB.Exec("DROP TRIGGER reject_publication"); err != nil {
		t.Fatal(err)
	}
	manager, err = New(root, store, []config.Source{{ID: "repo", Type: "rsync", Dest: "repo"}})
	if err != nil {
		t.Fatal("next startup could not import", err)
	}
	defer manager.Close()
}

func testManager(t *testing.T) (*Manager, config.Source) {
	t.Helper()
	dir := t.TempDir()
	store, err := database.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s := config.Source{ID: "repo", Name: "Test", Type: "rsync", Dest: "repo"}
	m, err := New(filepath.Join(dir, "mirror"), store, []config.Source{s})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m, s
}
func TestPublishedDownloadAndIsolation(t *testing.T) {
	m, s := testManager(t)
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(work, "file.rpm"), []byte("0123456789"), 0644)
	os.WriteFile(filepath.Join(work, ".treeinfo"), []byte("legal"), 0644)
	os.WriteFile(filepath.Join(work, "a.part"), []byte("private"), 0644)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(work, "escape"))
	if _, err = m.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m)
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/repo/file.rpm", nil)
	req.Header.Set("Range", "bytes=2-5")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 206 || string(b) != "2345" {
		t.Fatal(res.StatusCode, string(b))
	}
	etag := res.Header.Get("ETag")
	req, _ = http.NewRequest("GET", server.URL+"/repo/file.rpm", nil)
	req.Header.Set("If-None-Match", etag)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 304 {
		t.Fatal(res.StatusCode)
	}
	for _, p := range []string{"/repo/a.part", "/repo/escape", "/.mirrordock/", "/repo/%2e%2e/secret"} {
		res, err = http.Get(server.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatalf("%s: %d", p, res.StatusCode)
		}
	}
	res, _ = http.Get(server.URL + "/repo/.treeinfo")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("legal metadata inaccessible")
	}
}
func TestSaturatedDownloadsPreserveMetadataAndHEAD(t *testing.T) {
	m, s := testManager(t)
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(work, "repomd.txt"), []byte("metadata"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(work, "large.rpm"), make([]byte, 300<<10), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	for range cap(m.gates) {
		m.gates <- struct{}{}
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
	m, s := testManager(t)
	for i := range 2 {
		work, err := m.Prepare(t.Context(), s, int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			os.WriteFile(filepath.Join(work, "old.rpm"), []byte("old"), 0644)
		} else {
			os.Remove(filepath.Join(work, "old.rpm"))
			os.WriteFile(filepath.Join(work, "new.rpm"), []byte("new"), 0644)
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
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "repodata"), 0755)
	primary := `<metadata><package><location href="missing.rpm"/><size package="3"/></package></metadata>`
	os.WriteFile(filepath.Join(dir, "repodata", "primary.xml"), []byte(primary), 0644)
	doc := `<repomd><data type="primary"><location href="repodata/primary.xml"/><size>` + fmtInt(len(primary)) + `</size></data></repomd>`
	os.WriteFile(filepath.Join(dir, "repodata", "repomd.xml"), []byte(doc), 0644)
	if err := Validate(context.Background(), dir, "reposync"); err == nil || !strings.Contains(err.Error(), "缺失") {
		t.Fatal(err)
	}
}
func fmtInt(n int) string { return strconv.Itoa(n) }

func TestCollectionRetainsActiveReader(t *testing.T) {
	m, s := testManager(t)
	work, _ := m.Prepare(t.Context(), s, 1)
	os.WriteFile(filepath.Join(work, "old.rpm"), []byte("old"), 0644)
	old, err := m.Publish(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	m.routeMu.Lock()
	lease := m.table.Load().entries[s.Dest]
	lease.refs++
	m.routeMu.Unlock()
	work, _ = m.Prepare(t.Context(), s, 2)
	os.Remove(filepath.Join(work, "old.rpm"))
	os.WriteFile(filepath.Join(work, "new.rpm"), []byte("new"), 0644)
	if _, err = m.Publish(t.Context(), s, 2); err != nil {
		t.Fatal(err)
	}
	if err = m.Collect(time.Now().Add(25 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	f, err := lease.roots[0].Open("old.rpm")
	if err != nil {
		t.Fatal("active old root closed", err)
	}
	f.Close()
	if _, err = os.Stat(old.Current.Directory); err != nil {
		t.Fatal("active generation removed", err)
	}
	m.release(lease)
	if err = m.Collect(time.Now().Add(25 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(old.Current.Directory); !os.IsNotExist(err) {
		t.Fatal("expired generation retained", err)
	}
}

func BenchmarkPublicHTTP(b *testing.B) {
	dir := b.TempDir()
	store, err := database.Open(filepath.Join(dir, "data"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	s := config.Source{ID: "bench", Dest: "repo", Type: "rsync"}
	m, err := New(filepath.Join(dir, "mirror"), store, []config.Source{s})
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
	b.SetBytes(size)
	b.ReportAllocs()
	for b.Loop() {
		res, err := client.Get(server.URL + "/repo/file.rpm")
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
	m, s := testManager(t)
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("package"), 0644)
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
	m, s := testManager(t)
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(work, "one.rpm"), []byte("one"), 0644)
	os.WriteFile(filepath.Join(work, "two.rpm"), []byte("two"), 0644)
	if _, err = m.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	next := m.Work(2)
	os.MkdirAll(next, 0755)
	os.WriteFile(filepath.Join(next, "one.rpm"), []byte("partial"), 0644)
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
	m, s := testManager(t)
	work, _ := m.Prepare(t.Context(), s, 1)
	os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("package"), 0644)
	p, err := m.Publish(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	m.Restrict(nil)
	if err = m.Collect(time.Now().Add(48 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(p.Current.Directory, "pkg.rpm")); err != nil {
		t.Fatal("removing configuration destroyed published files", err)
	}
}

func TestAliasFailureCannotPublish(t *testing.T) {
	m, s := testManager(t)
	work, _ := m.Prepare(t.Context(), s, 1)
	os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("package"), 0644)
	os.Mkdir(filepath.Join(m.Root, s.Dest), 0755)
	if _, err := m.Publish(t.Context(), s, 1); err == nil {
		t.Fatal("occupied alias accepted")
	}
	if len(m.Publications()) != 0 {
		t.Fatal("failed publication became visible")
	}
	if _, err := os.Stat(filepath.Join(work, "pkg.rpm")); err != nil {
		t.Fatal("failed staging not preserved", err)
	}
}

func TestFailedCommitRestoresAliasAndStaging(t *testing.T) {
	m, s := testManager(t)
	work, _ := m.Prepare(t.Context(), s, 1)
	os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("old"), 0644)
	if _, err := m.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	work, _ = m.Prepare(t.Context(), s, 2)
	os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("new"), 0644)
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
