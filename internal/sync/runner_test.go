package syncer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mirrordock/internal/config"
	"net"
	"net/http"
	"net/http/httptest"
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
	if slices.Contains(args, "--delay-updates") || slices.Contains(args, "--inplace") || !slices.Contains(args, "--partial-dir=.rsync-partial") || !slices.Contains(args, "--exclude=.mirrordock*") {
		t.Fatal("private work must retain resumable per-file replacement", args)
	}
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
	var progress Progress
	if err = (SystemRunner{}).Run(ctx, task, func(p Progress) { progress = p }, &log); err != nil {
		t.Fatal(err, log.String())
	}
	if progress.ReceivedBytes == nil || *progress.ReceivedBytes == 0 {
		t.Fatal("missing actual received bytes", progress, log.String())
	}
	firstBytes := *progress.ReceivedBytes
	if err = (SystemRunner{}).Run(ctx, task, func(p Progress) { progress = p }, &log); err != nil {
		t.Fatal(err, log.String())
	}
	if progress.ReceivedBytes == nil || *progress.ReceivedBytes >= firstBytes {
		t.Fatal("incremental sync counted full file size", progress)
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
	if !ok || p.Phase != "transfer" || p.Percent != nil || p.ETA != nil {
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
	if AsFailure(Classify(5, "@ERROR: Unknown module")).Retryable {
		t.Fatal("invalid remote module was retried")
	}
}

func TestReposyncTransientDiagnostics(t *testing.T) {
	for _, line := range []string{"Curl error (6): Could not resolve host", "Curl error (28): Timeout", "Status code: 503", "Curl error (56): Connection reset"} {
		if !reposyncNetworkLine(line) {
			t.Fatal("transient error rejected", line)
		}
	}
	for _, line := range []string{"Curl error (60): SSL certificate problem", "Status code: 401", "GPG signature verification failed", "No space left on device", "Permission denied"} {
		if reposyncNetworkLine(line) {
			t.Fatal("permanent error retried", line)
		}
	}
}

func TestRsyncAuthenticationIsNotRetryable(t *testing.T) {
	for _, line := range []string{"@ERROR: auth failed on module repo", "@ERROR: access denied to repo", "@ERROR: Unknown module"} {
		if AsFailure(Classify(5, line)).Retryable {
			t.Fatal("invalid configuration retried", line)
		}
	}
}

func TestReposyncCommandKeepsNetworkDiagnosticsAndStopsOnPermanentErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		diagnostic string
		retry      bool
	}{
		{"Curl error (28): Timeout", true},
		{"Curl error (28): Timeout\nGPG check FAILED", false},
		{"Curl error (6): DNS failed\nPermission denied", false},
		{"Curl error (6): DNS failed\nCurl error (60): SSL certificate problem", false},
		{"Status code: 503", true},
		{"Status code: 404", false},
		{"invalid configuration", false},
	} {
		script := "#!/bin/sh\nprintf '%s\\n' '" + tc.diagnostic + "' 'Error: All mirrors were tried' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(dir, "dnf"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		err := command(t.Context(), "dnf", nil, func(Progress) {}, io.Discard)
		if err == nil || AsFailure(err).Retryable != tc.retry {
			t.Fatal(tc.diagnostic, err)
		}
		if strings.Contains(tc.diagnostic, "Permission denied") && AsFailure(err).Code != "LOCAL_TRANSFER" {
			t.Fatal("local permission error misclassified", err)
		}
		if tc.diagnostic == "Status code: 404" && AsFailure(err).Code != "UPSTREAM_ERROR" {
			t.Fatal("HTTP error misclassified", err)
		}
	}
}

func TestRsyncCommandRetainsAuthenticationFailureBeforeExitSummary(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\nprintf '%s\\n' '@ERROR: auth failed on module repo' 'rsync error: error starting client-server protocol (code 5)' >&2\nexit 5\n"
	if err := os.WriteFile(filepath.Join(dir, "rsync"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	err := command(t.Context(), "rsync", nil, func(Progress) {}, io.Discard)
	if err == nil || AsFailure(err).Retryable || !strings.Contains(err.Error(), "auth failed") {
		t.Fatal("lost auth diagnostic", err)
	}
}

func TestIncrementalRecursionStaysInTransfer(t *testing.T) {
	_, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	var seen []Progress
	out := processOutput{name: "rsync", log: io.Discard, cancel: cancel, emit: func(p Progress) { seen = append(seen, p) }}
	out.scan(strings.NewReader(strings.Join([]string{
		"  2,379,397,210   8%    4.31MB/s    1:42:56  ",
		"  2,381,280,309   8%    3.02MB/s    0:12:31 (xfr#1550, ir-chk=10465/12100)",
		"  2,383,803,445   8%    4.16MB/s    1:46:38  ",
		"  2,386,512,297   8%    3.02MB/s    0:12:32 (xfr#1551, ir-chk=10464/12100)",
		"  2,388,281,769   8%    3.90MB/s    1:53:45  ",
	}, "\n")))
	if len(seen) != 5 {
		t.Fatal(seen)
	}
	for i, p := range seen {
		// The first line arrives before rsync reveals incremental recursion.
		if p.Phase != "transfer" || i > 0 && (p.Percent != nil || p.ETA != nil || p.Total != 0) {
			t.Fatal(i, p)
		}
	}
	if seen[4].Bytes != 2388281769 || seen[4].Speed <= 0 {
		t.Fatal(seen[4])
	}
	seen = nil
	out = processOutput{name: "rsync", log: io.Discard, cancel: cancel, emit: func(p Progress) { seen = append(seen, p) }}
	out.scan(strings.NewReader("5,000 50% 1.00MB/s 0:00:05\n5,500 55% 1.00MB/s 0:00:02 (xfr#2, to-chk=8/20)\n"))
	if len(seen) != 2 || seen[1].Percent == nil || *seen[1].Percent != 55 || seen[1].ETA == nil || *seen[1].ETA != 5 {
		t.Fatal("complete file list lost percent or ETA", seen)
	}
}

func TestReceivedStatsDoNotUseFileProgress(t *testing.T) {
	_, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	var got Progress
	out := processOutput{name: "rsync", log: io.Discard, cancel: cancel, emit: func(p Progress) { got = p }}
	out.scan(strings.NewReader("1,000,000 100% 1.0MB/s 0:00:00\nTotal transferred file size: 1,000,000 bytes\nMatched data: 999,000 bytes\n"))
	if got.ReceivedBytes != nil {
		t.Fatal("logical progress claimed network bytes", got)
	}
	out.scan(strings.NewReader("Total bytes received: 1,234\n1,000,000 100% 1.0MB/s 0:00:00\n"))
	if got.ReceivedBytes == nil || *got.ReceivedBytes != 1234 || got.Bytes != 1000000 {
		t.Fatal(got)
	}
}

func TestReposyncImportsKeysBeforeDownloadInPrivateRoot(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MIRRORDOCK_KEY_TRACE", trace)
	for name, script := range map[string]string{
		"rpmkeys": "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$MIRRORDOCK_KEY_TRACE\"\ncat \"$4\" >>\"$MIRRORDOCK_KEY_TRACE\"\n",
		"dnf":     "#!/bin/sh\nprintf '\\ndnf invoked\\n' >>\"$MIRRORDOCK_KEY_TRACE\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "fixture-public-key") }))
	defer server.Close()
	task := Task{Directory: filepath.Join(dir, "work"), Candidate: server.URL, Source: config.Source{Type: "reposync", RepoSync: config.RepoSync{RepoID: "fixture", Releasever: "9", Basearch: "aarch64", GPGKeys: []string{server.URL + "/key.asc"}}}}
	if err := (SystemRunner{}).Run(t.Context(), task, func(Progress) {}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) != 7 || lines[0] != "--root" || lines[2] != "--import" || lines[4] != "fixture-public-key" || lines[5] != "dnf invoked" {
		t.Fatal(string(data))
	}
	private := filepath.Dir(lines[1])
	if !config.Within(dir, private) || filepath.Base(lines[1]) != "root" || filepath.Dir(lines[3]) != private {
		t.Fatal("keyring escaped private workspace", string(data))
	}
	if _, err := os.Stat(private); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private keyring not removed", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rpmkeys"), []byte("#!/bin/sh\necho invalid-key >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	os.Remove(trace)
	if err := (SystemRunner{}).Run(t.Context(), task, func(Progress) {}, io.Discard); err == nil || AsFailure(err).Retryable {
		t.Fatal("invalid key import was accepted or retried", err)
	}
	if _, err := os.Stat(trace); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dnf ran after key import failure", err)
	}
}

func TestRepoKeyDownloadLimitsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		bytes  int
		retry  bool
	}{
		{"empty", 200, 0, false},
		{"oversize", 200, (1 << 20) + 1, false},
		{"unavailable", 503, 0, true},
		{"missing", 404, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, strings.Repeat("x", tc.bytes))
			}))
			defer server.Close()
			err := importRepoKeys(t.Context(), t.TempDir(), []string{server.URL}, func(Progress) {}, io.Discard)
			if err == nil || AsFailure(err).Code != "GPG_KEY_DOWNLOAD" || AsFailure(err).Retryable != tc.retry {
				t.Fatal(err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := importRepoKeys(ctx, t.TempDir(), []string{"http://127.0.0.1:1/key.asc?credential=private"}, func(Progress) {}, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
