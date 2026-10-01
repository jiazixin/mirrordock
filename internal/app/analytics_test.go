package app

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyticsAuthenticationValidationAndEmptyResponse(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte("listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: fixture-password\nsources: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
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
	for _, path := range []string{"/api/analytics", "/api/analytics?type=reposync", "/api/analytics?type=rsync&days=0", "/api/analytics?type=rsync&days=abc", "/api/analytics?type=github_release&days=8"} {
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
}
