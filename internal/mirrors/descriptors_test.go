//go:build linux || darwin

package mirrors

import (
	"fmt"
	"golang.org/x/sys/unix"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRetainedGenerationsUnderLowDescriptorLimit(t *testing.T) {
	if os.Getenv("MIRRORDOCK_FD_FIXTURE") != "1" {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRetainedGenerationsUnderLowDescriptorLimit$", "-test.v")
		cmd.Env = append(os.Environ(), "MIRRORDOCK_FD_FIXTURE=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
		return
	}
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	limit.Cur = min(limit.Max, 128)
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	m, s := testManager(t)
	for id := int64(1); id <= 150; id++ {
		work, err := m.Prepare(t.Context(), s, id)
		if err != nil {
			t.Fatalf("generation %d prepare: %v", id, err)
		}
		if id == 1 {
			if err := os.WriteFile(filepath.Join(work, "old.bin"), []byte("old"), 0644); err != nil {
				t.Fatal(err)
			}
		} else if id == 2 {
			if err := os.Remove(filepath.Join(work, "old.bin")); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(work, "asset.bin"), []byte(fmt.Sprint(id)), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Publish(t.Context(), s, id); err != nil {
			t.Fatalf("generation %d publish: %v", id, err)
		}
	}
	if len(m.Publications()[0].Previous) != 149 {
		t.Fatal("retention shortened")
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/repo/old.bin", nil))
	if w.Code != 200 || w.Body.String() != "old" {
		t.Fatal("lazy fallback failed", w.Code, w.Body.String())
	}
	files, connections := DescriptorLimits()
	if files*3+connections > int(limit.Cur/2) {
		t.Fatal("shared FD budget exceeded", files, connections)
	}
}
