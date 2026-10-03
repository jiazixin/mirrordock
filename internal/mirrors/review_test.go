package mirrors

import (
	"encoding/json/v2"
	"errors"
	"github.com/google/go-github/v88/github"
	"io"
	"mirrordock/internal/config"
	syncer "mirrordock/internal/sync"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubReuseKeepsGenerationAndRefreshesSummary(t *testing.T) {
	m, s := testManager(t)
	var changed atomic.Bool
	s.Type = "github_release"
	s.GitHubRelease = config.GitHubRelease{Repository: "o/r"}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases":
			io.WriteString(w, `[{"id":1,"tag_name":"v1","published_at":"2026-01-01T00:00:00Z"}]`)
		case "/repos/o/r/releases/1/assets":
			if changed.Load() {
				io.WriteString(w, `[{"id":21,"name":"asset.bin","size":3}]`)
			} else {
				io.WriteString(w, `[{"id":20,"name":"asset.bin","size":3}]`)
			}
		case "/repos/o/r/releases/assets/20", "/repos/o/r/releases/assets/21":
			http.Redirect(w, r, server.URL+"/asset", 302)
		case "/asset":
			if changed.Load() {
				io.WriteString(w, "xyz")
			} else {
				io.WriteString(w, "abc")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := github.NewClient(github.WithURLs(new(server.URL+"/"), new(server.URL+"/")))
	if err != nil {
		t.Fatal(err)
	}
	settings := config.Settings{GitHub: config.GitHubSettings{ReleaseCount: 1}}
	var previous string
	var synced time.Time
	for id := int64(1); id <= 3; id++ {
		if id == 3 {
			changed.Store(true)
		}
		work, err := m.Prepare(t.Context(), s, id)
		if err != nil {
			t.Fatal(err)
		}
		var progress syncer.Progress
		err = syncer.GitHubWithClient(t.Context(), client, server.Client(), syncer.Task{Source: s, Settings: settings, Directory: work}, func(p syncer.Progress) { progress = p }, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		p, err := m.Publish(t.Context(), s, id)
		if err != nil {
			t.Fatal(err)
		}
		if id == 2 && p.Current.ID != previous {
			t.Fatalf("全部资产复用仍创建新发布代，received=%v skipped=%d previous=%s current=%s", *progress.ReceivedBytes, progress.Skipped, previous, p.Current.ID)
		}
		if id == 2 {
			if *progress.ReceivedBytes != 0 || progress.Skipped != 1 {
				t.Fatal(progress)
			}
			if !p.Current.Summary.Releases["v1"].SyncedAt.After(synced) {
				t.Fatal("reuse did not refresh summary")
			}
			var persisted Publication
			records, err := m.store.Publications()
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(records[0], &persisted); err != nil {
				t.Fatal(err)
			}
			if !persisted.Current.Summary.Releases["v1"].SyncedAt.Equal(p.Current.Summary.Releases["v1"].SyncedAt) {
				t.Fatal("summary not durable")
			}
		}
		if id == 3 && p.Current.ID == previous {
			t.Fatal("changed asset reused generation")
		}
		synced = p.Current.Summary.Releases["v1"].SyncedAt

		previous = p.Current.ID
	}
}
func TestRsyncHardlinkReuseKeepsGeneration(t *testing.T) {
	binary := os.Getenv("MIRRORDOCK_TEST_RSYNC")
	if binary == "" {
		t.Skip("set MIRRORDOCK_TEST_RSYNC for isolated modern rsync")
	}
	m, s := testManager(t)
	upstream := t.TempDir()
	if err := os.WriteFile(filepath.Join(upstream, "a.bin"), []byte("abc"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(upstream, "a.bin"), filepath.Join(upstream, "b.bin")); err != nil {
		t.Fatal(err)
	}
	settings := config.Defaults().Settings
	var previous string
	for id := int64(1); id <= 2; id++ {
		work, err := m.Prepare(t.Context(), s, id)
		if err != nil {
			t.Fatal(err)
		}
		args := syncer.RsyncArgs(syncer.Task{Source: s, Settings: settings, Directory: work, Candidate: upstream + "/"})
		// contimeout is daemon-only; use a local controlled source for this metadata reproduction.
		local := args[:0]
		for _, a := range args {
			if a != "--contimeout=30" {
				local = append(local, a)
			}
		}
		output, err := exec.CommandContext(t.Context(), binary, local...).CombinedOutput()
		if err != nil {
			t.Fatal(err, string(output))
		}
		p, err := m.Publish(t.Context(), s, id)
		if err != nil {
			t.Fatal(err)
		}
		if id == 2 && p.Current.ID != previous {
			t.Fatalf("相同硬链接上游仍创建新发布代\n%s", output)
		}
		if id == 2 {
			work, err = m.Prepare(t.Context(), s, 3)
			if err != nil {
				t.Fatal(err)
			}
			a, err := os.Stat(filepath.Join(work, "a.bin"))
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.Stat(filepath.Join(work, "b.bin"))
			if err != nil {
				t.Fatal(err)
			}
			published, err := os.Stat(filepath.Join(p.Current.Directory, "a.bin"))
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(a, b) || os.SameFile(a, published) {
				t.Fatal("work hardlink group was not isolated")
			}
			if err := os.WriteFile(filepath.Join(work, "a.bin"), []byte("xyz"), 0644); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(p.Current.Directory, "a.bin"))
			if err != nil || string(data) != "abc" {
				t.Fatal("work changed published body", err)
			}
			data, err = os.ReadFile(filepath.Join(work, "b.bin"))
			if err != nil || string(data) != "xyz" {
				t.Fatal("work group not preserved", err)
			}
		}
		previous = p.Current.ID
	}
}

func TestFileDescriptorsBoundBeforeOpen(t *testing.T) {
	m, s := testManager(t)
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "asset.bin"), []byte("abc"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	for range cap(m.fileGates) {
		m.fileGates <- struct{}{}
	}
	defer func() {
		for len(m.fileGates) > 0 {
			<-m.fileGates
		}
	}()
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(method, "/repo/asset.bin", nil))
		if w.Code != 503 || w.Header().Get("Retry-After") == "" {
			t.Fatal(method, w.Code)
		}
	}
}
func TestSummaryPersistenceFailureRetainsOldFactsAndWork(t *testing.T) {
	m, s := testManager(t)
	s.Type = "github_release"
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(work, "v1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "v1", "asset"), []byte("abc"), 0644); err != nil {
		t.Fatal(err)
	}
	old, err := m.Publish(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	work, err = m.Prepare(t.Context(), s, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.DB.Exec("CREATE TRIGGER reject_summary BEFORE UPDATE ON publications BEGIN SELECT RAISE(ABORT,'fixture'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(t.Context(), s, 2); err == nil {
		t.Fatal("summary save failure hidden")
	}
	if m.Publications()[0].Current.ID != old.Current.ID {
		t.Fatal("failed summary changed generation")
	}
	if _, err := os.Stat(work); errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed summary removed work")
	}
	if _, err := m.store.DB.Exec("DROP TRIGGER reject_summary"); err != nil {
		t.Fatal(err)
	}
	p, err := m.Publish(t.Context(), s, 2)
	if err != nil || p.Current.ID != old.Current.ID {
		t.Fatal(p, err)
	}
}
