package syncer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mirrordock/internal/config"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRsyncFlags(t *testing.T) {
	settings := config.Defaults().Settings
	settings.Rsync.LimitMB = 12.5
	tgt := Task{Source: config.Source{Rsync: config.Rsync{UID: new(0), GID: new(1000)}}, Settings: settings, Directory: "/tmp/work", Candidate: "rsync://example/a/"}
	args := RsyncArgs(tgt)
	for _, s := range []string{"--delete", "--links", "--hard-links", "--owner", "--group", "--chown=0:1000", "--bwlimit=12207"} {
		if !slices.Contains(args, s) {
			t.Fatalf("missing %s: %v", s, args)
		}
	}
	tgt.Source.Rsync.GID = nil
	if !slices.Contains(RsyncArgs(tgt), "--chown=0") {
		t.Fatal("uid only")
	}
	tgt.Source.Rsync.UID = nil
	tgt.Source.Rsync.GID = new(1000)
	if !slices.Contains(RsyncArgs(tgt), "--chown=:1000") {
		t.Fatal("gid only")
	}
}
func TestProgressAndStop(t *testing.T) {
	p, ok := ParseProgress("  1,000,000  50%  2.0MB/s 0:01:20 (xfr#2)")
	if !ok || p.Bytes != 1000000 || p.Percent == nil || *p.Percent != 50 || *p.ETA != 80 {
		t.Fatal(p)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := command(ctx, "sh", []string{"-c", "sleep 30"}, func(Progress) {}, io.Discard)
	if err == nil || time.Since(start) > time.Second {
		t.Fatal(err, time.Since(start))
	}
}

func TestSystemRsyncProtocol(t *testing.T) {
	binary := os.Getenv("MIRRORDOCK_TEST_RSYNC")
	if binary == "" {
		t.Skip("set MIRRORDOCK_TEST_RSYNC to run the isolated real rsync daemon test")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	source := filepath.Join(dir, "upstream")
	dest := filepath.Join(dir, "dest")
	os.Mkdir(source, 0755)
	os.Mkdir(dest, 0755)
	payload := strings.Repeat("0123456789", 10000)
	os.WriteFile(filepath.Join(source, "package.rpm"), []byte(payload), 0644)
	os.Link(filepath.Join(source, "package.rpm"), filepath.Join(source, "hardlink.rpm"))
	os.Symlink("package.rpm", filepath.Join(source, "link.rpm"))
	os.WriteFile(filepath.Join(dest, "obsolete"), []byte("remove"), 0644)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	conf := filepath.Join(dir, "rsyncd.conf")
	contents := fmt.Sprintf("pid file = %s\nlog file = %s\nuse chroot = no\nuid =\ngid =\nmunge symlinks = no\n[repo]\npath = %s\nread only = yes\n", filepath.Join(dir, "daemon.pid"), filepath.Join(dir, "daemon.log"), source)
	os.WriteFile(conf, []byte(contents), 0600)
	cmd := exec.Command(binary, "--daemon", "--no-detach", "--address=127.0.0.1", fmt.Sprintf("--port=%d", port), "--config="+conf)
	configureProcess(cmd)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopProcess(cmd.Process.Pid, true); cmd.Wait() })
	ready := false
	for range 100 {
		conn, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 20*time.Millisecond)
		if e == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("daemon did not start")
	}
	settings := config.Defaults().Settings
	task := Task{Directory: dest, Candidate: fmt.Sprintf("rsync://127.0.0.1:%d/repo/", port), Settings: settings, Source: config.Source{Type: "rsync", Rsync: config.Rsync{UID: new(os.Getuid()), GID: new(os.Getgid())}}}
	var log bytes.Buffer
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err = (SystemRunner{}).Run(ctx, task, func(Progress) {}, &log); err != nil {
		t.Fatal(err, log.String())
	}
	got, err := os.ReadFile(filepath.Join(dest, "package.rpm"))
	if err != nil || string(got) != payload {
		t.Fatal("wrong transfer", err)
	}
	if _, err = os.Stat(filepath.Join(dest, "obsolete")); !os.IsNotExist(err) {
		t.Fatal("delete default did not apply")
	}
	one, _ := os.Stat(filepath.Join(dest, "package.rpm"))
	two, _ := os.Stat(filepath.Join(dest, "hardlink.rpm"))
	if !os.SameFile(one, two) {
		t.Fatal("hardlinks lost")
	}
	if target, err := os.Readlink(filepath.Join(dest, "link.rpm")); err != nil || target != "package.rpm" {
		t.Fatal("symlink lost", target, err)
	}
}

func TestIncrementalScanDoesNotClaimCompleteProgress(t *testing.T) {
	p, ok := ParseProgress("1,000 100% 1.00MB/s 0:00:01 (xfr#1, ir-chk=9/20)")
	if !ok || p.Phase != "scan" || p.Percent != nil || p.ETA != nil {
		t.Fatal(p, ok)
	}
	if _, ok = ParseProgress(strings.Repeat("9", 1000) + " 100% 1.00MB/s 0:00:01"); ok {
		t.Fatal("overflowing progress accepted")
	}
}

func TestRemotePermissionFailureCanSwitchButLocalCannot(t *testing.T) {
	remote := Classify(23, "rsync: [sender] send_files failed: Permission denied (13)")
	local := Classify(23, "rsync: [receiver] write failed: Permission denied (13)")
	if !AsFailure(remote).Retryable || AsFailure(local).Retryable {
		t.Fatal(remote, local)
	}
	if !AsFailure(Classify(5, "@ERROR: Unknown module")).Retryable {
		t.Fatal("remote module failure did not switch")
	}
}
