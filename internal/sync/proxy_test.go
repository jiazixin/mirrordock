package syncer

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"mirrordock/internal/config"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v88/github"
)

// These proxies route exclusively to a local TLS fixture, with no public DNS or upstream traffic.
func relayProxy(t *testing.T, downstream net.Conn, input io.Reader, upstream net.Conn) {
	t.Helper()
	defer downstream.Close()
	defer upstream.Close()
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = io.Copy(upstream, input)
		_ = upstream.Close()
	})
	_, _ = io.Copy(downstream, upstream)
	_ = downstream.Close()
	_ = upstream.Close()
	wg.Wait()
}

func testHTTPProxy(t *testing.T, secure bool, target string, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy-user:proxy-password")) {
			t.Error("proxy CONNECT or authentication missing")
			http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
			return
		}
		requests.Add(1)
		upstream, err := net.DialTimeout("tcp", target, time.Second)
		if err != nil {
			t.Error(err)
			http.Error(w, "fixture unavailable", http.StatusBadGateway)
			return
		}
		downstream, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			t.Error(err)
			return
		}
		_ = downstream.SetDeadline(time.Now().Add(5 * time.Second))
		_ = upstream.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err = io.WriteString(downstream, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			downstream.Close()
			upstream.Close()
			return
		}
		relayProxy(t, downstream, buffered.Reader, upstream)
	})
	var server *httptest.Server
	if secure {
		server = httptest.NewTLSServer(handler)
	} else {
		server = httptest.NewServer(handler)
	}
	t.Cleanup(server.Close)
	return server
}

func testSOCKSProxy(t *testing.T, target string, requests *atomic.Int32, domains *atomic.Int32) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				version, _ := reader.ReadByte()
				n, _ := reader.ReadByte()
				methods := make([]byte, int(n))
				if _, err := io.ReadFull(reader, methods); err != nil || version != 5 {
					t.Error("invalid SOCKS greeting")
					return
				}
				_, _ = conn.Write([]byte{5, 2}) // Username/password authentication.
				version, _ = reader.ReadByte()
				n, _ = reader.ReadByte()
				username := make([]byte, int(n))
				_, _ = io.ReadFull(reader, username)
				n, _ = reader.ReadByte()
				password := make([]byte, int(n))
				_, err := io.ReadFull(reader, password)
				if err != nil || version != 1 || string(username) != "proxy-user" || string(password) != "proxy-password" {
					t.Error("SOCKS authentication missing")
					return
				}
				_, _ = conn.Write([]byte{1, 0})
				header := make([]byte, 4)
				if _, err := io.ReadFull(reader, header); err != nil || header[0] != 5 || header[1] != 1 {
					t.Error("invalid SOCKS connect")
					return
				}
				length := 0
				switch header[3] {
				case 1:
					length = 4
				case 3:
					n, _ := reader.ReadByte()
					length = int(n)
					domains.Add(1)
				case 4:
					length = 16
				default:
					t.Error("invalid SOCKS address type")
					return
				}
				if _, err := io.ReadFull(reader, make([]byte, length+2)); err != nil {
					t.Error(err)
					return
				}
				upstream, err := net.DialTimeout("tcp", target, time.Second)
				if err != nil {
					t.Error(err)
					return
				}
				_ = upstream.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				requests.Add(1)
				relayProxy(t, conn, reader, upstream)
			})
		}
	})
	t.Cleanup(func() { listener.Close(); wg.Wait() })
	return listener.Addr().String()
}

func TestGitHubProxyAPIAndAssetDownloads(t *testing.T) {
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			var apiRequests, assetRequests, proxyRequests, domains atomic.Int32
			var assetURL string
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credentials leaked to upstream")
				}
				if r.URL.Path == "/asset" {
					assetRequests.Add(1)
					if r.Header.Get("Authorization") != "" {
						t.Error("GitHub token leaked to asset download")
					}
					_, _ = io.WriteString(w, "abc")
					return
				}
				apiRequests.Add(1)
				if r.Header.Get("Authorization") != "Bearer github-token" {
					t.Error("API authentication missing")
				}
				switch r.URL.Path {
				case "/repos/o/r/releases":
					_, _ = io.WriteString(w, `[{"id":1,"tag_name":"v1","published_at":"2026-10-01T00:00:00Z"}]`)
				case "/repos/o/r/releases/1/assets":
					_, _ = io.WriteString(w, `[{"id":1,"name":"linux.bin","size":3}]`)
				case "/repos/o/r/releases/assets/1":
					http.Redirect(w, r, assetURL, http.StatusFound)
				default:
					t.Error("unexpected API request", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(upstream.Close)
			assetURL = upstream.URL + "/asset"
			roots := x509.NewCertPool()
			roots.AddCert(upstream.Certificate())
			u, _ := url.Parse(upstream.URL)
			var proxyURL string
			if scheme == "http" || scheme == "https" {
				proxy := testHTTPProxy(t, scheme == "https", u.Host, &proxyRequests)
				p, _ := url.Parse(proxy.URL)
				p.User = url.UserPassword("proxy-user", "proxy-password")
				proxyURL = p.String()
				if scheme == "https" {
					roots.AddCert(proxy.Certificate())
				}
			} else {
				proxyURL = scheme + "://proxy-user:proxy-password@" + testSOCKSProxy(t, u.Host, &proxyRequests, &domains)
			}
			httpClient, transport, err := githubHTTP(proxyURL)
			if err != nil {
				t.Fatal(err)
			}
			defer transport.CloseIdleConnections()
			transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
			// example.com is covered by the fixture certificate. The local proxy resolves/routes it.
			client, err := github.NewClient(github.WithHTTPClient(httpClient), github.WithTimeout(time.Second*3), github.WithAuthToken("github-token"), github.WithURLs(new("https://example.com/"), nil))
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			task := Task{Directory: dir, Source: config.Source{GitHubRelease: config.GitHubRelease{Repository: "o/r"}}, Settings: config.Settings{GitHub: config.GitHubSettings{ReleaseCount: 1, Proxy: proxyURL}}}
			if err = GitHubWithClient(t.Context(), client, httpClient, task, func(Progress) {}, io.Discard); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(dir, "v1", "linux.bin"))
			if err != nil || string(b) != "abc" || apiRequests.Load() != 3 || assetRequests.Load() != 1 || proxyRequests.Load() < 2 {
				t.Fatal("proxy did not carry API and download", err, apiRequests.Load(), assetRequests.Load(), proxyRequests.Load())
			}
			if (scheme == "socks5" || scheme == "socks5h") && domains.Load() == 0 {
				t.Fatal("SOCKS must receive hostname for proxy-side DNS")
			}
		})
	}
}

func TestGitHubBlankProxyIsDirect(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	_, transport, err := githubHTTP("")
	if err != nil || transport.Proxy != nil {
		t.Fatal("blank explicit proxy must be direct", err)
	}
	transport.CloseIdleConnections()
}

func TestGitHubUnavailableProxyCannotFallBackToDirect(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "[]")
	}))
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	for _, scheme := range []string{"http", "socks5"} {
		httpClient, transport, err := githubHTTP(scheme + "://user:private-password@" + address)
		if err != nil {
			t.Fatal(err)
		}
		client, err := github.NewClient(github.WithHTTPClient(httpClient), github.WithURLs(new(server.URL+"/"), nil))
		if err != nil {
			t.Fatal(err)
		}
		task := Task{Directory: t.TempDir(), Source: config.Source{GitHubRelease: config.GitHubRelease{Repository: "o/r"}}, Settings: config.Settings{GitHub: config.GitHubSettings{ReleaseCount: 1, Proxy: scheme + "://user:private-password@" + address}}}
		err = GitHubWithClient(t.Context(), client, httpClient, task, func(Progress) {}, io.Discard)
		transport.CloseIdleConnections()
		if err == nil || requests.Load() != 0 || AsFailure(err).Code != "GITHUB_PROXY" {
			t.Fatal("unavailable proxy fell back or exposed credentials", requests.Load())
		}
	}
}
