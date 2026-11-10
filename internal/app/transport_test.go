package app

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The console must work however it is reached: plain HTTP on a LAN address,
// where browsers send no Sec-Fetch-Site header, native HTTPS, and a proxy
// that ends TLS and keeps the external Host. Only native TLS marks the
// session cookie Secure; a write from another site is refused everywhere.
func TestAdminSessionAcrossTransports(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	text := "listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: transport-fixture\nsources: []\n"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := openFile(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestApp(t, a)
	handler := a.Handler()
	for _, tc := range []struct {
		name, host, origin, fetchSite string
		native                        bool
	}{
		{name: "lan http", host: "192.168.1.20:8080", origin: "http://192.168.1.20:8080"},
		{name: "native https", host: "mirror.example", origin: "https://mirror.example", fetchSite: "same-origin", native: true},
		{name: "tls ending proxy", host: "mirror.example", origin: "https://mirror.example", fetchSite: "same-origin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			send := func(method, path, body, origin, fetchSite string, session *http.Cookie) *httptest.ResponseRecorder {
				t.Helper()
				request := httptest.NewRequest(method, "http://"+tc.host+path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Origin", origin)
				if fetchSite != "" {
					request.Header.Set("Sec-Fetch-Site", fetchSite)
				}
				if tc.native {
					request.TLS = &tls.ConnectionState{}
				}
				if session != nil {
					request.AddCookie(session)
				}
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, request)
				return recorder
			}
			login := send(http.MethodPost, "/api/auth/login", `{"username":"operator","password":"transport-fixture"}`, tc.origin, tc.fetchSite, nil)
			cookies := login.Result().Cookies()
			if login.Code != http.StatusOK || len(cookies) != 1 {
				t.Fatalf("got login status %d with %d cookies, want 200 and the session", login.Code, len(cookies))
			}
			session := cookies[0]
			if session.Secure != tc.native || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
				t.Fatalf("got cookie Secure=%v HttpOnly=%v SameSite=%v, want Secure=%v, HttpOnly and Strict", session.Secure, session.HttpOnly, session.SameSite, tc.native)
			}
			if got := send(http.MethodPost, "/api/config/reload", "", tc.origin, tc.fetchSite, session); got.Code != http.StatusOK {
				t.Fatalf("got same-origin write status %d, want 200: %s", got.Code, got.Body)
			}
			crossSite := "cross-site"
			if tc.fetchSite == "" {
				crossSite = "" // older and non-secure contexts are judged by Origin alone
			}
			if got := send(http.MethodPost, "/api/config/reload", "", "https://attacker.example", crossSite, session); got.Code != http.StatusForbidden {
				t.Fatalf("got cross-site write status %d, want 403", got.Code)
			}
		})
	}
}
