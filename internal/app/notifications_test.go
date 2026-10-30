package app

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mirrordock/internal/notification"
)

func TestNotificationAPIPermissionsSecretsAndSavedTests(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: fixture-password\nsources: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := openFile(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestApp(t, a)
	handler := a.Handler()
	etag := ""
	request := func(method, path, body, token, origin string, fetchSite string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if etag != "" {
			req.Header.Set("If-Match", etag)
		}
		if fetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", fetchSite)
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
		if out := request("GET", path, "", "", "", ""); out.Code != 401 {
			t.Fatal("anonymous read", out.Code)
		}
	}
	if out := request("PATCH", "/api/notifications/webhook", "{}", "", "", ""); out.Code != 401 {
		t.Fatal("anonymous write", out.Code)
	}
	login := request("POST", "/api/auth/login", `{"username":"admin","password":"fixture-password"}`, "", "", "")
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	token := login.Result().Cookies()[0].Value
	for _, check := range []struct {
		origin    string
		fetchSite string
	}{{"", "cross-site"}, {"http://evil.test", ""}} {
		if out := request("PATCH", "/api/notifications/webhook", "{}", token, check.origin, check.fetchSite); out.Code != 403 {
			t.Fatal("missing cross-origin gate", check)
		}
	}
	if out := request("PATCH", "/api/notifications/webhook", `{"enabled":true}`, token, "", ""); out.Code != http.StatusPreconditionFailed {
		t.Fatal("unconditional channel write", out.Code)
	}
	var view notification.SettingsView
	out := request("GET", "/api/notifications", "", token, "", "")
	if err := json.Unmarshal(out.Body.Bytes(), &view); err != nil || len(view.Channels) != 5 || out.Header().Get("ETag") == "" {
		t.Fatal("view", err)
	}
	etag = out.Header().Get("ETag")
	if out := request("PATCH", "/api/notifications/bad", "{}", token, "", ""); out.Code != 400 {
		t.Fatal("invalid channel")
	}
	if out := request("PATCH", "/api/notifications/webhook", `{"endpoint":123,"token":"sensitive-token"}`, token, "", ""); out.Code != 400 || strings.Contains(out.Body.String(), "sensitive-token") {
		t.Fatal("malformed error leaked", out.Body.String())
	}
	if out := request("PATCH", "/api/notifications/webhook", `{"endpoint":null}`, token, "", ""); out.Code != 400 || !strings.Contains(out.Body.String(), `"reason":"not_nullable"`) {
		t.Fatal("null endpoint accepted", out.Body.String())
	}
	if out := request("POST", "/api/notifications/webhook/test", "", token, "", ""); out.Code != 400 {
		t.Fatal("unsaved test allowed")
	}
	received := false
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		var m notification.Message
		if err := json.UnmarshalRead(r.Body, &m); err != nil || m.Event != "notification.test" {
			t.Error("test JSON", err)
		}
		w.WriteHeader(204)
	}))
	defer local.Close()
	body, _ := json.Marshal(map[string]string{"endpoint": local.URL + "/sensitive-token"})
	out = request("PATCH", "/api/notifications/webhook", string(body), token, "", "")
	if out.Code != 200 || strings.Contains(out.Body.String(), "sensitive-token") || strings.Contains(out.Body.String(), local.URL) {
		t.Fatal("save leaked", out.Code, out.Body.String())
	}
	if out := request("POST", "/api/notifications/webhook/test", "", token, "", ""); out.Code != http.StatusNoContent || !received {
		t.Fatal("disabled saved test failed", out.Body.String())
	}
	for _, path := range []string{"/api/config", "/api/notifications", "/api/notifications/records"} {
		out = request("GET", path, "", token, "", "")
		if strings.Contains(out.Body.String(), "sensitive-token") || strings.Contains(out.Body.String(), local.URL) || strings.Contains(out.Body.String(), "enc:v1:") {
			t.Fatal("API credential exposed", path)
		}
	}
	if out := request("PUT", "/api/notifications/timezone", `{"timezone":"Asia/Shanghai"}`, token, "", ""); out.Code != http.StatusNoContent {
		t.Fatal("timezone", out.Body.String())
	}
	out = request("GET", "/api/notifications", "", token, "", "")
	if !strings.Contains(out.Body.String(), "Asia/Shanghai") {
		t.Fatal("timezone not saved")
	}
	if out := request("PUT", "/api/notifications/timezone", `{"timezone":"Local"}`, token, "", ""); out.Code != 400 {
		t.Fatal("invalid timezone")
	}
}
