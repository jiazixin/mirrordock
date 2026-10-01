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
func TestAssetReplacementCannotReuseOldSameSize(t *testing.T) {
	client, _ := assetClient(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failure", 503) })
	target := filepath.Join(t.TempDir(), "asset.bin")
	os.WriteFile(target, []byte("old"), 0644)
	old := AssetIdentity{ID: 1, Size: 3}
	b, _ := json.Marshal(old)
	config.AtomicWrite(identityFile(target), b, 0600)
	next := AssetIdentity{ID: 2, Size: 3}
	for range 2 {
		reused, err := downloadAsset(t.Context(), client, http.DefaultClient, "owner", "repo", 2, next, target, func(int64) {}, "")
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
			reused, err := downloadAsset(t.Context(), client, server.Client(), "owner", "repo", 1, id, target, func(int64) {}, "")
			if err != nil || reused {
				t.Fatal(reused, err)
			}
			b, err = os.ReadFile(target)
			if err != nil || string(b) != "012345678" {
				t.Fatal(string(b), err)
			}
			want := int32(1)
			if badRange {
				want = 2
			}
			if downloads.Load() != want {
				t.Fatal(downloads.Load())
			}
			reused, err = downloadAsset(t.Context(), client, server.Client(), "owner", "repo", 1, id, target, func(int64) {}, "")
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
	if err := GitHubWithClient(context.Background(), client, server.Client(), task, func(Progress) {}, io.Discard); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "new", "linux-amd64.bin"))
	if err != nil || string(b) != "abc" {
		t.Fatal(string(b), err)
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
		if got := githubError(err, proxy); got != sentinel {
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
