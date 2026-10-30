package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mirrordock/internal/config"
)

func TestPartialConfigApplyReturnsRevisionAndWarning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	text := "listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: correct password 123\nsources: []\n"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := openFile(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestApp(t, a)
	// A user file occupies the destination; the YAML save itself must succeed.
	if err := os.WriteFile(filepath.Join(dir, "mirror", "occupied"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	etag := ""
	request := func(method, path, body string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if method == "PUT" {
			req.Header.Set("If-Match", strconv.Quote(a.Config.View().Revision))
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		etag = res.Header.Get("ETag")
		data, _ := io.ReadAll(res.Body)
		return res.StatusCode, data
	}
	if status, _ := request("POST", "/api/auth/login", `{"username":"operator","password":"correct password 123"}`); status != 200 {
		t.Fatal("login failed", status)
	}
	view := a.Config.View()
	c := view.Config
	c.Sources = append(c.Sources, config.Source{ID: "occupied", Name: "Occupied", Type: "rsync", Enabled: true, Dest: "occupied", Upstreams: map[string]string{"src1": "rsync://localhost/one/"}})
	body, err := json.Marshal(map[string]any{"config": c})
	if err != nil {
		t.Fatal(err)
	}
	status, data := request("PUT", "/api/config", string(body))
	var saved struct {
		Warning struct {
			Code string `json:"code"`
		} `json:"warning"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err, string(data))
	}
	if status != 200 || saved.Warning.Code != "config_apply_incomplete" || etag == strconv.Quote(view.Revision) || etag != strconv.Quote(a.Config.View().Revision) {
		t.Fatal("partial apply hid the saved revision", status, string(data))
	}
	status, data = request("GET", "/api/system/checks", "")
	if status != 200 || !strings.Contains(string(data), `"publication_issues":[{"source_id":"occupied","code":"dest_not_directory"`) {
		t.Fatal("publication issue missing from checks", string(data))
	}
	status, data = request("POST", "/api/config/reload", "")
	if status != 200 || !strings.Contains(string(data), `"warning"`) {
		t.Fatal("partial reload hid the warning", status, string(data))
	}
}
