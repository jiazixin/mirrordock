package syncer

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"github.com/google/go-github/v88/github"
	"io"
	"mirrordock/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
		reused, err := downloadAsset(t.Context(), client, http.DefaultClient, "owner", "repo", 2, next, target, func(int64) {})
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
			reused, err := downloadAsset(t.Context(), client, server.Client(), "owner", "repo", 1, id, target, func(int64) {})
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
			reused, err = downloadAsset(t.Context(), client, server.Client(), "owner", "repo", 1, id, target, func(int64) {})
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
