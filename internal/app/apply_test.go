package app

import (
	"context"
	"encoding/json/v2"
	"io"
	"mirrordock/internal/config"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPartialConfigApplyReturnsRevisionAndWarning(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	text := "listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: correct password 123\nsources: []\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := openFile(file)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	// A user file occupies the destination; the YAML save itself must succeed.
	if err := os.WriteFile(filepath.Join(dir, "mirror", "occupied"), []byte("user"), 0644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	request := func(method, path, body string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("X-MirrorDock-Request", "1")
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return res.StatusCode, data
	}
	if status, _ := request("POST", "/api/auth/login", `{"username":"operator","password":"correct password 123"}`); status != 200 {
		t.Fatal("login failed", status)
	}
	view := a.Config.View()
	c := view.Config
	c.Sources = append(c.Sources, config.Source{ID: "occupied", Name: "Occupied", Type: "rsync", Enabled: true, Dest: "occupied", Upstreams: map[string]string{"src1": "rsync://localhost/one/"}})
	body, err := json.Marshal(map[string]any{"config": c, "revision": view.Revision})
	if err != nil {
		t.Fatal(err)
	}
	status, data := request("PUT", "/api/config", string(body))
	var saved struct {
		Revision string `json:"revision"`
		Warning  string `json:"warning"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err, string(data))
	}
	if status != 200 || saved.Warning == "" || saved.Revision == view.Revision || saved.Revision != a.Config.View().Revision {
		t.Fatal("partial apply hid the saved revision", status, string(data))
	}
	status, data = request("GET", "/api/system/checks", "")
	if status != 200 || !strings.Contains(string(data), `"publication_issues":[{"source_id":"occupied"`) {
		t.Fatal("publication issue missing from checks", string(data))
	}
	status, data = request("POST", "/api/config/reload", "")
	if status != 200 || !strings.Contains(string(data), `"warning"`) {
		t.Fatal("partial reload hid the warning", status, string(data))
	}
}
