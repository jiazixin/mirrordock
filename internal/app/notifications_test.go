package app

import (
	"context"
	"encoding/json/v2"
	"mirrordock/internal/notifications"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotificationAPIPermissionsSecretsAndSavedTests(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: fixture-password\nsources: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := openFile(file)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	handler := a.Handler()
	request := func(method, path, body, token, origin string, header bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if header {
			req.Header.Set("X-MirrorDock-Request", "1")
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "mirrordock_session", Value: token})
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	for _, path := range []string{"/api/notifications", "/api/notifications/records"} {
		if out := request("GET", path, "", "", "", false); out.Code != 401 {
			t.Fatal("anonymous read", out.Code)
		}
	}
	if out := request("PUT", "/api/notifications/webhook", "{}", "", "", true); out.Code != 401 {
		t.Fatal("anonymous write", out.Code)
	}
	login := request("POST", "/api/auth/login", `{"username":"admin","password":"fixture-password"}`, "", "", true)
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	token := login.Result().Cookies()[0].Value
	for _, check := range []struct {
		origin string
		header bool
	}{{"", false}, {"http://evil.test", true}} {
		if out := request("PUT", "/api/notifications/webhook", "{}", token, check.origin, check.header); out.Code != 403 {
			t.Fatal("missing CSRF/origin gate")
		}
	}
	if out := request("PUT", "/api/notifications/bad", "{}", token, "", true); out.Code != 400 {
		t.Fatal("invalid channel")
	}
	if out := request("PUT", "/api/notifications/webhook", `{"endpoint":123,"token":"sensitive-token"}`, token, "", true); out.Code != 400 || strings.Contains(out.Body.String(), "sensitive-token") {
		t.Fatal("malformed error leaked", out.Body.String())
	}
	var view notifications.SettingsView
	out := request("GET", "/api/notifications", "", token, "", false)
	if err := json.Unmarshal(out.Body.Bytes(), &view); err != nil || len(view.Channels) != 5 {
		t.Fatal("view", err)
	}
	if out := request("POST", "/api/notifications/webhook/test", "", token, "", true); out.Code != 400 {
		t.Fatal("unsaved test allowed")
	}
	received := false
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		var m notifications.Message
		if err := json.UnmarshalRead(r.Body, &m); err != nil || m.Event != "notification.test" {
			t.Error("test JSON", err)
		}
		w.WriteHeader(204)
	}))
	defer local.Close()
	body, _ := json.Marshal(notifications.Update{Revision: view.Revision, Endpoint: local.URL + "/sensitive-token"})
	out = request("PUT", "/api/notifications/webhook", string(body), token, "", true)
	if out.Code != 200 || strings.Contains(out.Body.String(), "sensitive-token") || strings.Contains(out.Body.String(), local.URL) {
		t.Fatal("save leaked", out.Code, out.Body.String())
	}
	if out := request("POST", "/api/notifications/webhook/test", "", token, "", true); out.Code != 200 || !received {
		t.Fatal("disabled saved test failed", out.Body.String())
	}
	for _, path := range []string{"/api/config", "/api/notifications", "/api/notifications/records"} {
		out = request("GET", path, "", token, "", false)
		if strings.Contains(out.Body.String(), "sensitive-token") || strings.Contains(out.Body.String(), local.URL) || strings.Contains(out.Body.String(), "enc:v1:") {
			t.Fatal("API credential exposed", path)
		}
	}
	if out := request("POST", "/api/notifications/timezone", `{"timezone":"Asia/Shanghai"}`, token, "", true); out.Code != 200 {
		t.Fatal("timezone", out.Body.String())
	}
	out = request("GET", "/api/notifications", "", token, "", false)
	if !strings.Contains(out.Body.String(), "Asia/Shanghai") {
		t.Fatal("timezone not saved")
	}
	if out := request("POST", "/api/notifications/timezone", `{"timezone":"Local"}`, token, "", true); out.Code != 400 {
		t.Fatal("invalid timezone")
	}
}
