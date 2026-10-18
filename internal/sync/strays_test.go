package syncer

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHelperProcess is a stand-in executor started by the tests below.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("MIRRORDOCK_HELPER_PROCESS") != "1" {
		return
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

type helper struct {
	cmd    *exec.Cmd
	exited chan error
}

func startHelper(t *testing.T, dir string, args ...string) *helper {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestHelperProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "MIRRORDOCK_HELPER_PROCESS=1")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &helper{cmd: cmd, exited: make(chan error, 1)}
	// Reap promptly so a killed helper does not linger as a zombie.
	go func() { h.exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-h.exited
	})
	return h
}

func (h *helper) killed(t *testing.T) bool {
	t.Helper()
	select {
	case err := <-h.exited:
		h.exited <- err
		exit, ok := errors.AsType[*exec.ExitError](err)
		return ok && exit.Sys().(syscall.WaitStatus).Signal() == syscall.SIGKILL
	case <-time.After(2 * time.Second):
		return false
	}
}

func TestTerminateStraysStopsOnlyWorkProcesses(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, ".mirrordock", "work")
	published := filepath.Join(root, ".mirrordock", "published", "generation")
	for _, dir := range []string{filepath.Join(work, "5"), filepath.Join(work, "6"), published} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	byArgument := startHelper(t, root, "--download-path="+filepath.Join(work, "5"))
	serving := startHelper(t, published, "--daemon", published+"/")
	// An administrator's shell may sit in a work tree; cwd alone never matches.
	browsing := startHelper(t, filepath.Join(work, "6"))
	time.Sleep(100 * time.Millisecond)
	if n, err := TerminateStrays(work); err != nil || n != 1 {
		t.Fatal("unexpected stray count", n, err)
	}
	if !byArgument.killed(t) {
		t.Fatal("process referencing a task work directory survived")
	}
	for _, h := range []*helper{serving, browsing} {
		select {
		case err := <-h.exited:
			t.Fatal("process without a work argument was stopped", err)
		default:
		}
	}
	if n, err := TerminateStrays(work); err != nil || n != 0 {
		t.Fatal("second scan found strays", n, err)
	}
}

func TestCommandStopsDescendantsAfterExit(t *testing.T) {
	var log strings.Builder
	// The background child closes the pipes so the shell exits immediately.
	err := command(t.Context(), "sh", []string{"-c", "sleep 30 >/dev/null 2>&1 </dev/null & echo $!"}, processEnv(), func(Progress) {}, &log)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(log.String()))
	if err != nil {
		t.Fatal(log.String(), err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatal("descendant outlived its executor")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
