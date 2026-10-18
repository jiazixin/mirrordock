package syncer

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/google/go-github/v88/github"
	"io"
	"mirrordock/internal/config"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func assetClient(t *testing.T, handler http.HandlerFunc) (*github.Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := github.NewClient(github.WithURLs(new(server.URL+"/"), new(server.URL+"/")))
	if err != nil {
		t.Fatal(err)
	}
	return client, server
}

// testDownload builds a direct asset download with the production idle limit.
func testDownload(
	client *github.Client,
	httpClient *http.Client,
	id AssetIdentity,
	target string,
	received ...func(int64),
) assetDownload {
	download := assetDownload{
		client:     client,
		httpClient: httpClient,
		owner:      "owner",
		repo:       "repo",
		identity:   id,
		target:     target,
		idle:       assetIdleTimeout,
		progress:   func(int64) {},
	}
	if len(received) > 0 {
		download.received = received[0]
	}
	return download
}

func testDownloadIdle(client *github.Client, httpClient *http.Client, id AssetIdentity, target string, idle time.Duration) assetDownload {
	download := testDownload(client, httpClient, id, target)
	download.idle = idle
	return download
}

func TestAssetReplacementCannotReuseOldSameSize(t *testing.T) {
	client, _ := assetClient(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failure", 503) })
	target := filepath.Join(t.TempDir(), "asset.bin")
	os.WriteFile(target, []byte("old"), 0644)
	old := AssetIdentity{ID: 1, Size: 3}
	b, _ := json.Marshal(old)
	config.AtomicWrite(identityFile(target), b, 0600)
	next := AssetIdentity{ID: 2, Size: 3}
	for range 2 {
		reused, err := testDownload(client, http.DefaultClient, next, target).run(t.Context())
		if err == nil || reused {
			t.Fatal("reused old bytes for replaced asset")
		}
	}
	if !sameIdentity(target, old) || sameIdentity(target, next) {
		t.Fatal("failed download changed completed identity")
	}
}

func TestAssetResumeAndInvalidRangeRestart(t *testing.T) {
	for _, badRange := range []bool{false, true} {
		t.Run(fmt.Sprint(badRange), func(t *testing.T) {
			var downloads atomic.Int32
			var assetURL string
			client, server := assetClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/repos/") {
					http.Redirect(w, r, assetURL, 302)
					return
				}
				downloads.Add(1)
				if r.Header.Get("Range") != "" {
					if r.Header.Get("Range") != "bytes=3-" {
						t.Error("bad request range")
					}
					if badRange {
						w.Header().Set("Content-Range", "bytes 2-8/9")
						w.WriteHeader(206)
						io.WriteString(w, "invalid")
					} else {
						w.Header().Set("Content-Range", "bytes 3-8/9")
						w.WriteHeader(206)
						io.WriteString(w, "345678")
					}
					return
				}
				io.WriteString(w, "012345678")
			})
			assetURL = server.URL + "/asset"
			target := filepath.Join(t.TempDir(), "asset.bin")
			id := AssetIdentity{ID: 1, Size: 9}
			os.WriteFile(target+".part", []byte("012"), 0644)
			b, _ := json.Marshal(id)
			config.AtomicWrite(identityFile(target+".part"), b, 0600)
			received := int64(0)
			reused, err := testDownload(client, server.Client(), id, target, func(n int64) { received += n }).run(t.Context())
			if err != nil || reused {
				t.Fatal(reused, err)
			}
			b, err = os.ReadFile(target)
			if err != nil || string(b) != "012345678" {
				t.Fatal(string(b), err)
			}
			expectedBytes := int64(6)
			if badRange {
				expectedBytes = 9
			}
			if received != expectedBytes {
				t.Fatalf("received %d, want %d", received, expectedBytes)
			}
			want := int32(1)
			if badRange {
				want = 2
			}
			if downloads.Load() != want {
				t.Fatal(downloads.Load())
			}
			reused, err = testDownload(client, server.Client(), id, target, func(n int64) { received += n }).run(t.Context())
			if received != expectedBytes {
				t.Fatal("reuse counted as download", received)
			}
			if err != nil || !reused || downloads.Load() != want {
				t.Fatal("verified reuse failed", err)
			}
		})
	}
}

func TestGitHubSelectsNewestAssetsWithoutSourceArchives(t *testing.T) {
	var assetURL string
	client, server := assetClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases":
			io.WriteString(w, `[{"id":1,"tag_name":"old","published_at":"2025-01-01T00:00:00Z"},{"id":2,"tag_name":"new","published_at":"2026-01-01T00:00:00Z","zipball_url":"http://invalid/source.zip"}]`)
		case "/repos/o/r/releases/2/assets":
			io.WriteString(w, `[{"id":20,"name":"linux-amd64.bin","size":3}]`)
		case "/repos/o/r/releases/assets/20":
			http.Redirect(w, r, assetURL, 302)
		case "/asset":
			io.WriteString(w, "abc")
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	assetURL = server.URL + "/asset"
	dir := t.TempDir()
	task := Task{Directory: dir, Source: config.Source{GitHubRelease: config.GitHubRelease{Repository: "o/r", AssetPatterns: []string{"*linux*"}}}, Settings: config.Settings{GitHub: config.GitHubSettings{ReleaseCount: 1}}}
	var progress Progress
	if err := GitHubWithClient(t.Context(), client, server.Client(), task, func(p Progress) { progress = p }, io.Discard); err != nil {
		t.Fatal(err)
	}
	if progress.ReceivedBytes == nil || *progress.ReceivedBytes != 3 {
		t.Fatal(progress)
	}
	if err := GitHubWithClient(t.Context(), client, server.Client(), task, func(p Progress) { progress = p }, io.Discard); err != nil {
		t.Fatal(err)
	}
	if progress.ReceivedBytes == nil || *progress.ReceivedBytes != 0 || progress.Bytes != 3 || progress.Skipped != 1 {
		t.Fatal("reuse bytes", progress)
	}
	b, err := os.ReadFile(filepath.Join(dir, "new", "linux-amd64.bin"))
	if err != nil || string(b) != "abc" {
		t.Fatal(string(b), err)
	}
	marker, markerErr := os.ReadFile(filepath.Join(dir, "new", ".mirrordock-release-sync"))
	if markerErr != nil {
		t.Fatal(markerErr)
	}
	if _, err := time.Parse(time.RFC3339Nano, string(marker)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "old")); !os.IsNotExist(err) {
		t.Fatal("old release downloaded")
	}
}

func TestGitHubFailureDiagnosticsNeverEchoRequestSecrets(t *testing.T) {
	proxy := "https://proxy-user:proxy-secret@proxy.invalid:2001"
	for _, tc := range []struct {
		name         string
		err          error
		proxy        string
		code, detail string
	}{
		{"proxy EOF", &url.Error{Op: "Get", URL: "https://api.invalid/?token=secret", Err: &net.OpError{Op: "proxyconnect", Net: "tcp", Err: io.EOF}}, proxy, "GITHUB_PROXY", "EOF"},
		{"truncated tunnel", io.ErrUnexpectedEOF, proxy, "GITHUB_PROXY", "EOF"},
		{"DNS", &net.DNSError{Name: "secret.invalid", Err: "no such host"}, proxy, "GITHUB_PROXY", "DNS"},
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, proxy, "GITHUB_PROXY", "连接被拒绝"},
		{"direct EOF", io.EOF, "", "GITHUB_API", "直连失败"},
		{"certificate", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, proxy, "GITHUB_PROXY", "TLS 证书"},
		{"proxy auth", errors.New("Proxy Authentication Required: proxy-secret"), proxy, "GITHUB_PROXY", "HTTP 407"},
		{"token", &github.ErrorResponse{Response: &http.Response{StatusCode: 401}, Message: "secret-token"}, proxy, "GITHUB_API", "Token 无效"},
		{"forbidden", &github.ErrorResponse{Response: &http.Response{StatusCode: 403}, Message: "secret-token"}, "", "GITHUB_API", "权限"},
		{"missing", &github.ErrorResponse{Response: &http.Response{StatusCode: 404}, Message: "secret-token"}, "", "GITHUB_API", "不存在"},
		{"rate", &github.RateLimitError{}, "", "GITHUB_RATE_LIMIT", "配额"},
		{"secondary rate", &github.AbuseRateLimitError{}, "", "GITHUB_RATE_LIMIT", "临时限流"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := AsFailure(githubError(tc.err, tc.proxy))
			if f.Code != tc.code || !strings.Contains(f.Message, tc.detail) {
				t.Fatal(f)
			}
			for _, secret := range []string{"proxy-secret", "proxy-user", "secret-token", "secret.invalid", "token=secret"} {
				if strings.Contains(f.Message, secret) {
					t.Fatal("diagnostic exposed request or upstream secret")
				}
			}
		})
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		err := &url.Error{Op: "Get", URL: "https://download.invalid/?signed=private", Err: sentinel}
		if got := githubError(err, proxy); (sentinel == context.Canceled && got != sentinel) || (sentinel == context.DeadlineExceeded && !AsFailure(got).Retryable) {
			t.Fatal("context identity lost or URL retained", got)
		}
	}
}

func TestGitHubClosedProxyLogsActionableFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer server.Close()
	var log bytes.Buffer
	task := Task{Token: "secret-token", Directory: t.TempDir(), Source: config.Source{GitHubRelease: config.GitHubRelease{Repository: "o/r"}}, Settings: config.Settings{GitHub: config.GitHubSettings{ReleaseCount: 1, Proxy: server.URL}}}
	err := GitHub(t.Context(), task, func(Progress) {}, &log)
	if err == nil || AsFailure(err).Code != "GITHUB_PROXY" || !strings.Contains(log.String(), "EOF") || !strings.Contains(log.String(), "scanning Release") {
		t.Fatal("closed proxy should produce a useful failure and log", err, log.String())
	}
	if strings.Contains(log.String(), task.Token) || strings.Contains(log.String(), server.URL) {
		t.Fatal("log exposed secrets or URL")
	}
}

func TestGitHubAPIRedirectCannotLeakTokenToAnotherOrigin(t *testing.T) {
	var leakedRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leakedRequests.Add(1) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Error("API token missing")
		}
		http.Redirect(w, r, destination.URL+"/repos/o/r/releases", http.StatusMovedPermanently)
	}))
	defer origin.Close()
	httpClient := origin.Client()
	httpClient.CheckRedirect = githubAPIRedirect
	client, err := github.NewClient(github.WithHTTPClient(httpClient), github.WithAuthToken("secret-token"), github.WithURLs(new(origin.URL+"/"), nil))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = client.Repositories.ListReleases(t.Context(), "o", "r", nil)
	if err == nil || leakedRequests.Load() != 0 {
		t.Fatal("authenticated cross-origin redirect followed")
	}
}

func TestGitHubRetryClassification(t *testing.T) {
	for _, status := range []int{401, 403, 404, 407, 408, 429, 500, 502, 503, 504} {
		f := AsFailure(githubError(&github.ErrorResponse{Response: &http.Response{StatusCode: status}}, ""))
		if f.Retryable != transientHTTP(status) {
			t.Fatal("API status classification", status, f)
		}
	}
	for _, tc := range []struct {
		err   error
		retry bool
	}{{io.EOF, true}, {io.ErrUnexpectedEOF, true}, {context.DeadlineExceeded, true}, {&net.OpError{Err: syscall.ECONNRESET}, true}, {&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, false}, {errors.New("Proxy Authentication Required"), false}} {
		if f := AsFailure(httpNetworkError(tc.err, "", "DOWNLOAD_FAILED", "download")); f.Retryable != tc.retry {
			t.Fatal(tc.err, f)
		}
	}
}

func TestTruncatedAssetCanResumeAfterNetworkFailure(t *testing.T) {
	var downloads atomic.Int32
	var assetURL string
	client, server := assetClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			http.Redirect(w, r, assetURL, 302)
			return
		}
		if downloads.Add(1) == 1 {
			w.Header().Set("Content-Length", "9")
			io.WriteString(w, "012")
			return
		}
		if r.Header.Get("Range") != "bytes=3-" {
			t.Error("partial not reused", r.Header)
		}
		w.Header().Set("Content-Range", "bytes 3-8/9")
		w.WriteHeader(206)
		io.WriteString(w, "345678")
	})
	assetURL = server.URL + "/asset"
	target := filepath.Join(t.TempDir(), "asset.bin")
	id := AssetIdentity{ID: 1, Size: 9}
	_, err := testDownload(client, server.Client(), id, target).run(t.Context())
	if err == nil || !AsFailure(err).Retryable {
		t.Fatal("truncation not retryable", err)
	}
	if _, err = testDownload(client, server.Client(), id, target).run(t.Context()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "012345678" || downloads.Load() != 2 {
		t.Fatal("invalid resumed asset", err)
	}
}

func TestFailedAssetRetainsBytesSinceLastProgressTick(t *testing.T) {
	var assetURL string
	payload := strings.Repeat("x", 70<<10)
	client, server := assetClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			http.Redirect(w, r, assetURL, 302)
			return
		}
		w.Header().Set("Content-Length", "102400")
		io.WriteString(w, payload)
	})
	assetURL = server.URL + "/asset"
	var received, latest int64
	_, err := func() (bool, error) {
		download := testDownload(client, server.Client(), AssetIdentity{ID: 1, Size: 102400}, filepath.Join(t.TempDir(), "asset.bin"), func(n int64) { received += n })
		download.progress = func(n int64) { latest = n }
		return download.run(t.Context())
	}()
	if err == nil || received != int64(len(payload)) || latest != received {
		t.Fatal(err, received, latest)
	}
}

func TestGitHubRejectsReservedPublicationNames(t *testing.T) {
	for _, name := range []string{".mirrordock-release-sync", ".mirrordock-asset-pkg.json", "pkg.part", ".rsync-partial", ".~tmp~"} {
		for _, tag := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/tag=%t", name, tag), func(t *testing.T) {
				client, server := assetClient(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/repos/o/r/releases":
						tagName := "v1"
						if tag {
							tagName = name
						}
						fmt.Fprintf(w, `[{"id":1,"tag_name":%q}]`, tagName)
					case "/repos/o/r/releases/1/assets":
						fmt.Fprintf(w, `[{"id":2,"name":%q,"size":3}]`, name)
					default:
						t.Error("reserved name reached download", r.URL.Path)
						http.Error(w, "unexpected", 500)
					}
				})
				dir := t.TempDir()
				task := Task{Directory: dir, Source: config.Source{GitHubRelease: config.GitHubRelease{Repository: "o/r"}}, Settings: config.Settings{GitHub: config.GitHubSettings{ReleaseCount: 1}}}
				err := GitHubWithClient(t.Context(), client, server.Client(), task, func(Progress) {}, io.Discard)
				code := "INVALID_ASSET"
				if tag {
					code = "INVALID_TAG"
				}
				if err == nil || AsFailure(err).Code != code {
					t.Fatal(err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatal("reserved path created", entries, err)
				}
			})
		}
	}
}

func TestDownloadRedirectRejectsHTTPSDowngrade(t *testing.T) {
	var insecureReads atomic.Int32
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		insecureReads.Add(1)
		fmt.Fprint(w, "substituted key")
	}))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, insecure.URL, http.StatusFound)
	}))
	defer secure.Close()
	client := secure.Client()
	client.CheckRedirect = downloadRedirect
	req, err := http.NewRequestWithContext(t.Context(), "GET", secure.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.Do(req)
	if res != nil {
		res.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "HTTPS downgrade rejected") || insecureReads.Load() != 0 {
		t.Fatal("HTTPS key download reached plaintext endpoint", err, insecureReads.Load())
	}
}

func TestAssetIdleTimeoutRetainsPartialAndRetries(t *testing.T) {
	var assetURL string
	var finish atomic.Bool
	client, server := assetClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			http.Redirect(w, r, assetURL, 302)
			return
		}
		if finish.Load() {
			if r.Header.Get("Range") != "bytes=3-" {
				t.Error("partial not resumed", r.Header.Get("Range"))
			}
			w.Header().Set("Content-Range", "bytes 3-5/6")
			w.WriteHeader(206)
			io.WriteString(w, "def")
			return
		}
		io.WriteString(w, "abc")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	assetURL = server.URL + "/asset"
	target := filepath.Join(t.TempDir(), "asset.bin")
	id := AssetIdentity{ID: 1, Size: 6}
	_, err := testDownloadIdle(client, server.Client(), id, target, 30*time.Millisecond).run(t.Context())
	f := AsFailure(err)
	if f.Code != "DOWNLOAD_IDLE" || !f.Retryable {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target + ".part")
	if err != nil || string(b) != "abc" {
		t.Fatal(string(b), err)
	}
	finish.Store(true)
	if _, err := testDownloadIdle(client, server.Client(), id, target, time.Second).run(t.Context()); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(target)
	if err != nil || string(b) != "abcdef" {
		t.Fatal(string(b), err)
	}
}

func TestRateLimitRetainsServerRetryTime(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	err := githubError(&github.RateLimitError{Rate: github.Rate{Reset: github.Timestamp{Time: reset}}}, "")
	f := AsFailure(err)
	if !f.Retryable || !f.RetryAt.Equal(reset) {
		t.Fatal(f)
	}
}

func TestGitHubPrunesObsoleteAssetsOnlyAfterSuccess(t *testing.T) {
	var fail atomic.Bool
	var assetURL string
	client, server := assetClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases":
			io.WriteString(w, `[{"id":1,"tag_name":"v1","published_at":"2026-01-01T00:00:00Z"}]`)
		case "/repos/o/r/releases/1/assets":
			io.WriteString(w, `[{"id":2,"name":"new.bin","size":3}]`)
		case "/repos/o/r/releases/assets/2":
			http.Redirect(w, r, assetURL, 302)
		case "/asset":
			if fail.Load() {
				http.Error(w, "failure", 503)
			} else {
				io.WriteString(w, "new")
			}
		default:
			http.NotFound(w, r)
		}
	})
	assetURL = server.URL + "/asset"
	dir := t.TempDir()
	tag := filepath.Join(dir, "v1")
	if err := os.Mkdir(tag, 0755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(tag, "old.bin")
	for _, name := range []string{old, identityFile(old), old + ".part", identityFile(old + ".part")} {
		if err := os.WriteFile(name, []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	task := Task{Directory: dir, Source: config.Source{GitHubRelease: config.GitHubRelease{Repository: "o/r"}}, Settings: config.Settings{GitHub: config.GitHubSettings{ReleaseCount: 1, PruneOldReleases: true}}}
	fail.Store(true)
	if err := GitHubWithClient(t.Context(), client, server.Client(), task, func(Progress) {}, io.Discard); err == nil {
		t.Fatal("failed download passed")
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("failed download pruned old asset", err)
	}
	fail.Store(false)
	if err := GitHubWithClient(t.Context(), client, server.Client(), task, func(Progress) {}, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{old, identityFile(old), old + ".part", identityFile(old + ".part")} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatal("obsolete file retained", name, err)
		}
	}
}
