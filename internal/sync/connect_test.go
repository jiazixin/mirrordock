package syncer

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mirrordock/internal/config"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The test binary doubles as the rsync-connect helper, as the service
// binary does, so rsync can start it through RSYNC_CONNECT_PROG.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == ConnectCommand {
		os.Exit(RunConnect())
	}
	os.Exit(m.Run())
}

const testGreeting = "@RSYNCD: 31.0\n"

// greetingServer greets immediately, like an rsync daemon, then echoes.
func greetingServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				if _, err := io.WriteString(conn, testGreeting); err == nil {
					_, _ = io.Copy(conn, conn)
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func checkTunnel(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	greeting := make([]byte, len(testGreeting))
	if _, err := io.ReadFull(conn, greeting); err != nil || string(greeting) != testGreeting {
		t.Fatalf("greeting lost through tunnel: %q %v", greeting, err)
	}
	if _, err := io.WriteString(conn, "ping\n"); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 5)
	if _, err := io.ReadFull(conn, echo); err != nil || string(echo) != "ping\n" {
		t.Fatalf("relay failed: %q %v", echo, err)
	}
}

// testProxyURL starts an authenticating proxy of scheme that only reaches target.
func testProxyURL(t *testing.T, scheme, target string, requests *atomic.Int32) (*url.URL, *tls.Config) {
	t.Helper()
	if scheme == "socks5" || scheme == "socks5h" {
		var domains atomic.Int32
		proxy, _ := url.Parse(scheme + "://proxy-user:proxy-password@" + testSOCKSProxy(t, target, requests, &domains))
		return proxy, nil
	}
	server := testHTTPProxy(t, scheme == "https", target, requests)
	proxy, _ := url.Parse(server.URL)
	proxy.User = url.UserPassword("proxy-user", "proxy-password")
	if scheme != "https" {
		return proxy, nil
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return proxy, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
}

func TestDialProxyTunnelsEveryScheme(t *testing.T) {
	target := greetingServer(t)
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			var requests atomic.Int32
			proxy, tlsConfig := testProxyURL(t, scheme, target, &requests)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			conn, err := dialProxy(ctx, proxy, target, tlsConfig)
			if err != nil {
				t.Fatal(err)
			}
			checkTunnel(t, conn)
			if requests.Load() != 1 {
				t.Fatal("tunnel did not use the proxy")
			}
		})
	}
}

func TestDialProxyClassifiesFailures(t *testing.T) {
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unreachable := closed.Addr().String()
	closed.Close()
	status := func(code int) string {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		t.Cleanup(server.Close)
		return server.URL
	}
	socks := refusingSOCKSProxy(t)
	for _, test := range []struct{ name, proxy, kind string }{
		{"unreachable", "http://user:private-secret@" + unreachable, connectProxyUnreachable},
		{"auth", status(http.StatusProxyAuthRequired), connectProxyAuth},
		{"denied", status(http.StatusForbidden), connectTargetDenied},
		{"bad gateway", status(http.StatusBadGateway), connectTargetFailed},
		{"not a proxy", status(http.StatusMethodNotAllowed), connectProxyProtocol},
		{"socks without credentials", "socks5://" + socks, connectProxyAuth},
	} {
		t.Run(test.name, func(t *testing.T) {
			proxy, err := config.ParseProxyField("rsync.proxy", test.proxy)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			conn, err := dialProxy(ctx, proxy, "127.0.0.1:873", nil)
			if err == nil {
				conn.Close()
				t.Fatal("dial succeeded")
			}
			failure, ok := errors.AsType[*connectFailure](err)
			if !ok || failure.kind != test.kind || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("got %v, want %s", err, test.kind)
			}
		})
	}
}

// refusingSOCKSProxy accepts no authentication method.
func refusingSOCKSProxy(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				greeting := make([]byte, 3)
				if _, err := io.ReadFull(conn, greeting); err == nil {
					_, _ = conn.Write([]byte{0x05, 0xff})
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func TestDialProxyHonorsConnectTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// Accepts but never answers the CONNECT request.
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = io.Copy(io.Discard, conn)
		}
	}()
	proxy, _ := url.Parse("http://" + listener.Addr().String())
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = dialProxy(ctx, proxy, "127.0.0.1:873", nil)
	if failure, ok := errors.AsType[*connectFailure](err); !ok || failure.kind != connectProxyUnreachable || time.Since(started) > 2*time.Second {
		t.Fatal("stalled proxy did not time out", err, time.Since(started))
	}
}

func TestConnectLineFailure(t *testing.T) {
	for _, test := range []struct {
		kind                          string
		retryable, source, proxyIssue bool
	}{
		{connectProxyUnreachable, true, false, true},
		{connectProxyAuth, false, false, true},
		{connectProxyProtocol, false, false, true},
		{connectInvalid, false, false, true},
		{connectTargetFailed, true, true, false},
		{connectTargetDenied, false, true, false},
	} {
		failure, ok := connectLineFailure(connectMarker+test.kind+": detail", 5)
		if !ok || failure.Code != "RSYNC_PROXY" || failure.Retryable != test.retryable || failure.SourceFailure != test.source || failure.ProxyFailure != test.proxyIssue || !strings.Contains(failure.Message, "detail") {
			t.Fatalf("%s classified as %+v", test.kind, failure)
		}
	}
	if _, ok := connectLineFailure("rsync: connection unexpectedly closed", 12); ok {
		t.Fatal("ordinary rsync line classified as proxy failure")
	}
}

func TestRsyncEnvNeverInheritsProxyVariables(t *testing.T) {
	t.Setenv("RSYNC_PROXY", "inherited:3128")
	t.Setenv("RSYNC_CONNECT_PROG", "nc inherited 873")
	t.Setenv(connectProxyEnv, "http://inherited:1")
	task := Task{Settings: config.Defaults().Settings, Candidate: "rsync://[::1]:8873/repo/"}
	env, err := rsyncEnv(task)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(env, func(entry string) bool { return strings.Contains(entry, "inherited") }) || !slices.Contains(env, "LC_ALL=C") {
		t.Fatal("direct rsync inherited proxy variables", env)
	}
	task.Proxy = "socks5://user:private-secret@127.0.0.1:1080"
	if env, err = rsyncEnv(task); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
	}
	program := values["RSYNC_CONNECT_PROG"]
	if !strings.HasSuffix(program, "' "+ConnectCommand) || strings.Contains(program, "private-secret") {
		t.Fatal("connect program must only name the executable", program)
	}
	if values[connectProxyEnv] != task.Proxy || values[connectTargetEnv] != "[::1]:8873" || values[connectTimeoutEnv] != "30" || values["RSYNC_PROXY"] != "" {
		t.Fatal("helper environment incomplete", values)
	}
	task.Candidate = "rsync://mirror.example/repo/"
	if env, err = rsyncEnv(task); err != nil || !slices.Contains(env, connectTargetEnv+"=mirror.example:873") {
		t.Fatal("default daemon port missing", env, err)
	}
	task.Candidate = "rsync://mirror.example:99999/repo/"
	if _, err = rsyncEnv(task); err == nil {
		t.Fatal("out-of-range daemon port accepted")
	}
}

func TestConnectProgramQuoting(t *testing.T) {
	dir := t.TempDir()
	odd := filepath.Join(dir, "it's 100% $(odd)")
	if err := os.Mkdir(odd, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(odd, "helper")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"ran $1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// rsync turns %% into %; the shell must then see one quoted word.
	program := strings.ReplaceAll(connectProgram(script), "%%", "%")
	out, err := exec.Command("/bin/sh", "-c", program).CombinedOutput()
	if err != nil || string(out) != "ran "+ConnectCommand+"\n" {
		t.Fatalf("quoted program did not run: %q %v", out, err)
	}
}

func TestConnectCommandRelaysThroughProxy(t *testing.T) {
	target := greetingServer(t)
	var requests atomic.Int32
	proxy, _ := testProxyURL(t, "http", target, &requests)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, ConnectCommand)
	cmd.Env = append(os.Environ(), connectProxyEnv+"="+proxy.String(), connectTargetEnv+"="+target, connectTimeoutEnv+"=5")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Like rsync, keep stdin open until the daemon has answered.
	want := testGreeting + "ping\n"
	got := make([]byte, len(want))
	_, _ = io.ReadFull(stdout, got[:len(testGreeting)])
	_, _ = io.WriteString(stdin, "ping\n")
	_, readErr := io.ReadFull(stdout, got[len(testGreeting):])
	stdin.Close()
	if err = cmd.Wait(); err != nil || readErr != nil || string(got) != want || requests.Load() != 1 {
		t.Fatalf("helper relay: %q %q %v %v", got, stderr.String(), readErr, err)
	}

	cmd = exec.Command(executable, ConnectCommand)
	cmd.Env = append(os.Environ(), connectProxyEnv+"=http://user:private-secret@127.0.0.1:1", connectTargetEnv+"="+target, connectTimeoutEnv+"=5")
	stderr.Reset()
	cmd.Stderr = &stderr
	if err = cmd.Run(); err == nil || !strings.HasPrefix(stderr.String(), connectMarker+connectProxyUnreachable+": ") || strings.Contains(stderr.String(), "private-secret") {
		t.Fatalf("helper failure diagnostic: %q %v", stderr.String(), err)
	}
}

func TestSystemRsyncThroughProxy(t *testing.T) {
	binary := os.Getenv("MIRRORDOCK_TEST_RSYNC")
	if binary == "" {
		t.Skip("set MIRRORDOCK_TEST_RSYNC to run the isolated real rsync proxy test")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	source := filepath.Join(dir, "upstream")
	if err = os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("proxied package ", 4096)
	if err = os.WriteFile(filepath.Join(source, "package.rpm"), []byte(payload), 0644); err != nil {
		t.Fatal(err)
	}
	port := startRsyncDaemon(t, binary, dir, source)
	daemon := fmt.Sprintf("127.0.0.1:%d", port)
	for _, scheme := range []string{"http", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			var requests atomic.Int32
			proxy, _ := testProxyURL(t, scheme, daemon, &requests)
			dest := filepath.Join(t.TempDir(), "dest")
			if err := os.Mkdir(dest, 0755); err != nil {
				t.Fatal(err)
			}
			// The candidate names a host that only the proxy can reach.
			task := Task{
				Directory: dest,
				Candidate: "rsync://mirror.invalid/repo/",
				Settings:  config.Defaults().Settings,
				Source:    config.Source{Type: "rsync", Rsync: config.Rsync{UID: new(os.Getuid()), GID: new(os.Getgid())}},
				Proxy:     proxy.String(),
			}
			var log bytes.Buffer
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			if err := (SystemRunner{}).Run(ctx, task, func(Progress) {}, &log); err != nil {
				t.Fatal(err, log.String())
			}
			got, err := os.ReadFile(filepath.Join(dest, "package.rpm"))
			if err != nil || string(got) != payload || requests.Load() == 0 {
				t.Fatal("rsync did not transfer through the proxy", err, requests.Load(), log.String())
			}
			if !strings.Contains(log.String(), "经 "+scheme+" 代理") || strings.Contains(log.String(), "proxy-password") {
				t.Fatal("proxy log line missing or exposed credentials", log.String())
			}
		})
	}
	t.Run("daemon refusal keeps its own classification", func(t *testing.T) {
		var requests atomic.Int32
		proxy, _ := testProxyURL(t, "http", daemon, &requests)
		task := Task{
			Directory: t.TempDir(),
			Candidate: "rsync://mirror.invalid/missing/",
			Settings:  config.Defaults().Settings,
			Source:    config.Source{Type: "rsync"},
			Proxy:     proxy.String(),
		}
		var log bytes.Buffer
		failure := AsFailure((SystemRunner{}).Run(t.Context(), task, func(Progress) {}, &log))
		if failure.Code != "UPSTREAM_ERROR" || failure.ProxyFailure || failure.Retryable || strings.Contains(log.String(), connectMarker) {
			t.Fatalf("unknown module through proxy: %+v\n%s", failure, log.String())
		}
	})
	t.Run("unreachable proxy", func(t *testing.T) {
		closed, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := closed.Addr().String()
		closed.Close()
		task := Task{
			Directory: t.TempDir(),
			Candidate: "rsync://" + daemon + "/repo/",
			Settings:  config.Defaults().Settings,
			Source:    config.Source{Type: "rsync"},
			Proxy:     "socks5://user:private-secret@" + address,
		}
		var log bytes.Buffer
		err = (SystemRunner{}).Run(t.Context(), task, func(Progress) {}, &log)
		failure := AsFailure(err)
		if failure.Code != "RSYNC_PROXY" || !failure.ProxyFailure || !failure.Retryable || strings.Contains(failure.Message+log.String(), "private-secret") {
			t.Fatalf("unreachable proxy: %+v\n%s", failure, log.String())
		}
	})
}

// startRsyncDaemon serves source as module "repo" on a free loopback port.
func startRsyncDaemon(t *testing.T, binary, dir, source string) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	conf := filepath.Join(dir, "rsyncd.conf")
	contents := fmt.Sprintf("pid file = %s\nlog file = %s\nuse chroot = no\nuid =\ngid =\nmunge symlinks = no\n[repo]\npath = %s\nread only = yes\n", filepath.Join(dir, "daemon.pid"), filepath.Join(dir, "daemon.log"), source)
	if err = os.WriteFile(conf, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "--daemon", "--no-detach", "--address=127.0.0.1", fmt.Sprintf("--port=%d", port), "--config="+conf)
	configureProcess(cmd)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopProcess(cmd.Process.Pid, true); cmd.Wait() })
	for range 100 {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 20*time.Millisecond)
		if err == nil {
			conn.Close()
			return port
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon did not start")
	return 0
}
