package mirrors

import (
	"context"
	"errors"
	"mirrordock/internal/config"
	syncer "mirrordock/internal/sync"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReflinkProbeAndFailureNeverCopies(t *testing.T) {
	m, s := testManager(t)
	if result := m.ProbeReflink(t.Context()); result.Status != "supported" {
		t.Fatalf("fixture filesystem must support cloning: %+v", result)
	}
	work, err := m.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "package.rpm"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	old, err := m.Publish(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	clone := m.clone
	m.clone = func(src *os.File, target string) error {
		if strings.Contains(src.Name(), ".reflink-probe-") {
			return clone(src, target)
		}
		return syscall.EOPNOTSUPP
	}
	if _, err := m.Prepare(t.Context(), s, 2); err == nil || !strings.Contains(err.Error(), "未回退") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.Work(2), "package.rpm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed clone copied data", err)
	}
	if _, err := os.Stat(filepath.Join(m.Work(2), ".mirrordock-prepared")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed preparation marked ready", err)
	}
	data, err := os.ReadFile(filepath.Join(old.Current.Directory, "package.rpm"))
	if err != nil || string(data) != "original" {
		t.Fatal("live mirror changed", err)
	}
	for _, fault := range []struct {
		err    error
		status string
	}{{syscall.EOPNOTSUPP, "unsupported"}, {syscall.EXDEV, "unsupported"}, {syscall.EACCES, "error"}} {
		m.clone = func(*os.File, string) error { return fault.err }
		if result := m.ProbeReflink(t.Context()); result.Status != fault.status {
			t.Fatalf("%v: %+v", fault.err, result)
		}
	}
	for _, dir := range []string{"work", "published"} {
		entries, err := os.ReadDir(filepath.Join(m.Root, ".mirrordock", dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), "probe") {
				t.Fatal("probe leaked", entry.Name())
			}
		}
	}
}

func TestPublicationSkipsOnlyUnchangedWorkingTree(t *testing.T) {
	for _, change := range []string{"none", "private", "write_restore_time", "delete", "permission", "symlink"} {
		t.Run(change, func(t *testing.T) {
			m, s := testManager(t)
			work, err := m.Prepare(t.Context(), s, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(work, "package.rpm"), []byte("old"), 0644); err != nil {
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
			file := filepath.Join(work, "package.rpm")
			switch change {
			case "private":
				err = os.WriteFile(filepath.Join(work, ".mirrordock-private"), []byte("ignored"), 0600)
			case "write_restore_time":
				st, statErr := os.Stat(file)
				if statErr != nil {
					t.Fatal(statErr)
				}
				err = os.WriteFile(file, []byte("new"), 0644)
				if err == nil {
					err = os.Chtimes(file, st.ModTime(), st.ModTime())
				}
			case "delete":
				err = os.Remove(file)
				if err == nil {
					err = os.WriteFile(filepath.Join(work, "another.rpm"), []byte("other"), 0644)
				}
			case "permission":
				err = os.Chmod(file, 0600)
			case "symlink":
				err = os.Symlink("package.rpm", filepath.Join(work, "alias"))
			}
			if err != nil {
				t.Fatal(err)
			}
			next, err := m.Publish(t.Context(), s, 2)
			if err != nil {
				t.Fatal(err)
			}
			unchanged := change == "none" || change == "private"
			if (next.Current.ID == old.Current.ID) != unchanged {
				t.Fatalf("wrong publication reuse for %s", change)
			}
			if unchanged && len(next.Previous) != 0 {
				t.Fatal("no-op created old generation")
			}
			data, err := os.ReadFile(filepath.Join(old.Current.Directory, "package.rpm"))
			if err != nil || string(data) != "old" {
				t.Fatal("clone write changed previous generation", err)
			}
			if _, err := os.Stat(m.Work(2)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("successful work remained", err)
			}
		})
	}
}

func TestCapacityAdmissionSharesWorkerMargin(t *testing.T) {
	m, _ := testManager(t)
	available := uint64(1<<30) + workerSpace + 1
	m.space = func(string) (DiskSpace, error) { return DiskSpace{Total: 10 << 30, Available: available}, nil }
	if err := m.BeginSync(1); err != nil {
		t.Fatal(err)
	}
	if err := m.BeginSync(2); !errors.Is(err, ErrLowSpace) {
		t.Fatal("workers oversubscribed admission margin", err)
	}
	m.EndSync(1)
	if err := m.BeginSync(2); err != nil {
		t.Fatal(err)
	}
	m.EndSync(2)
	available = (1 << 30) - 1
	if err := m.CheckSpace(); !errors.Is(err, ErrLowSpace) {
		t.Fatal("low space accepted", err)
	}
	if info := m.StorageInfo(); info.Reserve != 1<<30 || info.Available != available || info.Workers != 0 {
		t.Fatal(info)
	}
}

func TestCleanupUsesDurableTerminalStates(t *testing.T) {
	m, _ := testManager(t)
	for _, state := range []string{"succeeded", "failed", "timed_out", "cancelled", "queued", "running", "pausing", "paused", "interrupted"} {
		r := struct {
			State string `json:"state"`
		}{state}
		id, err := m.store.CreateRun("fixture", state, time.Now().UTC().Format(time.RFC3339Nano), r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(m.Work(id), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(m.Work(id), "partial"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := m.CleanupWork(t.Context()); err != nil {
			t.Fatal(err)
		}
		_, err = os.Stat(m.Work(id))
		remove := state == "succeeded" || state == "failed" || state == "timed_out" || state == "cancelled"
		if errors.Is(err, os.ErrNotExist) != remove {
			t.Fatalf("%s: %v", state, err)
		}
	}
	unknown := m.Work(99999)
	if err := os.MkdirAll(unknown, 0755); err != nil {
		t.Fatal(err)
	}
	if err := m.CleanupWork(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("unknown work removed", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.CleanupWork(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cleanup ignored cancellation", err)
	}
}

func TestCollectionResumesPrivateGarbageAfterInterruption(t *testing.T) {
	m, _ := testManager(t)
	garbage := filepath.Join(m.Root, ".mirrordock", "published", ".gc-fixture")
	if err := os.MkdirAll(garbage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(garbage, "package"), []byte("garbage"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Collect(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(garbage); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recently touched garbage postponed for 24 hours", err)
	}
}

func TestRealRsyncUnchangedPublication(t *testing.T) {
	binary := os.Getenv("MIRRORDOCK_TEST_RSYNC")
	if binary == "" {
		t.Skip("set MIRRORDOCK_TEST_RSYNC for the isolated local rsync fixture")
	}
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	m, s := testManager(t)
	upstream := t.TempDir()
	if err := os.WriteFile(filepath.Join(upstream, "package.rpm"), []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	var old Publication
	for id := range int64(2) {
		work, err := m.Prepare(t.Context(), s, id+1)
		if err != nil {
			t.Fatal(err)
		}
		args := syncer.RsyncArgs(syncer.Task{Directory: work, Candidate: upstream + "/", Settings: config.Defaults().Settings, Source: s})
		// A local fixture has no connection establishment phase.
		for i, arg := range args {
			if strings.HasPrefix(arg, "--contimeout=") {
				args = append(args[:i], args[i+1:]...)
				break
			}
		}
		if out, err := exec.CommandContext(t.Context(), binary, args...).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
		next, err := m.Publish(t.Context(), s, id+1)
		if err != nil {
			t.Fatal(err)
		}
		if id == 1 && next.Current.ID != old.Current.ID {
			t.Fatal("unchanged rsync created a new generation")
		}
		old = next
	}
}
