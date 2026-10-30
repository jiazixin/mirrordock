package app

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mirrordock/internal/database"
	runengine "mirrordock/internal/run"
	"mirrordock/internal/timefmt"
)

func TestAnalyticsAuthenticationValidationAndEmptyResponse(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte("listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: fixture-password\nsources: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := openFile(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestApp(t, a)
	token, err := a.auth.login("admin", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	request := func(path string, login bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if login {
			r.AddCookie(&http.Cookie{Name: "mirrordock_session", Value: token})
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	if w := request("/api/analytics?type=rsync", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"/api/runs?range=invalid", "/api/runs?limit=abc", "/api/runs?offset=-1", "/api/runs?latest=1", "/api/runs/abc", "/api/analytics", "/api/analytics?type=unknown", "/api/analytics?type=rsync&days=0", "/api/analytics?type=rsync&days=abc", "/api/analytics?type=github_release&days=8"} {
		if w := request(path, true); w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	w := request("/api/analytics?type=rsync&source=%27%20OR%201=1--", true)
	var payload struct {
		Days    []any `json:"days"`
		Summary struct {
			Runs int `json:"runs"`
		} `json:"summary"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &payload) != nil || len(payload.Days) != 30 || payload.Summary.Runs != 0 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("/api/analytics?type=reposync", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	now := time.Now().UTC()
	for _, kind := range []string{"rsync", "reposync", "github_release"} {
		r := runengine.Run{SourceID: "fixture", Name: "fixture", Type: kind, State: "succeeded", CreatedAt: timefmt.From(now), StartedAt: timefmt.From(now), ElapsedMillis: 5000, RetryCount: 2}
		if _, err := a.Store.CreateRun(t.Context(), database.CreateRunOptions{Source: r.SourceID, State: r.State, Created: now, Value: &r}); err != nil {
			t.Fatal(err)
		}
	}

	for _, key := range []string{"1h", "12h", "24h", "3d", "7d", "30d", "month", "yesterday"} {
		w := request("/api/runs?range="+key, true)
		var page struct {
			Total int `json:"total"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatal(key, w.Code, w.Body.String())
		}
		want := 3
		if key == "yesterday" {
			want = 0
		}
		if page.Total != want {
			t.Fatal(key, page.Total, want)
		}
	}
	w = request("/api/analytics?type=reposync&days=7&source=fixture", true)
	var repo database.Analytics
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &repo) != nil || repo.Type != "reposync" || len(repo.Days) != 7 || repo.Summary.Runs != 1 || repo.Summary.RetriedRuns != 1 || repo.Summary.ElapsedMillis != 5000 {
		t.Fatal(w.Code, w.Body.String())
	}
}
