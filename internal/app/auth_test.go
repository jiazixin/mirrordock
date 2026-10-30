package app

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mirrordock/internal/config"
)

func TestSessionStoresOnlyDigestAndExpires(t *testing.T) {
	t.Parallel()
	a, err := newAuth(config.Auth{Username: "operator", Password: "fixture-password"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	token, err := a.login("operator", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(token))
	if _, ok := a.sessions[digest]; !ok {
		t.Fatal("session must be indexed by its digest")
	}
	r := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	if !a.authorized(r) {
		t.Fatal("got unauthorized for a fresh session")
	}
	now = now.Add(sessionLifetime)
	if a.authorized(r) {
		t.Fatal("got authorized for an expired session")
	}
}

func TestConfiguredAccountRequiresRestart(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "config.yaml")
	text := "listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: first-password\nsources: []\n"
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := openFile(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestApp(t, a) })
	token, err := a.auth.login("operator", "first-password")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/api/config", nil)
	request.AddCookie(&http.Cookie{Name: "mirrordock_session", Value: token})
	if !a.auth.authorized(request) {
		t.Fatal("new session rejected")
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "operator", "new-operator"), "first-password", "second-password")
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reloadConfig(a.Config); err != nil {
		t.Fatal(err)
	}
	if !a.Config.View().RestartRequired || !a.auth.authorized(request) {
		t.Fatal("account change took effect before restart or was not marked")
	}
	if _, err := a.auth.login("new-operator", "second-password"); err == nil {
		t.Fatal("new account accepted before restart")
	}
	if _, err := a.auth.login("operator", "first-password"); err != nil {
		t.Fatal("startup account changed during reload")
	}
	if err := closeTestApp(t, a); err != nil {
		t.Fatal(err)
	}
	b, err := openFile(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestApp(t, b) })
	if _, err := b.auth.login("new-operator", "second-password"); err != nil {
		t.Fatal("new configured account rejected after restart")
	}
	if _, err := b.auth.login("operator", "first-password"); err == nil || b.auth.authorized(request) {
		t.Fatal("old account or old session survived restart")
	}
	if value, err := b.Store.State(t.Context(), "admin_password"); err != nil || value != "" {
		t.Fatal("new password was persisted to SQLite", err)
	}
}

func TestLoginLimitsSeparateClientsAndReset(t *testing.T) {
	t.Parallel()
	a := &auth{now: time.Now, attempts: map[string]loginWindow{}}
	for range 10 {
		if a.limited("192.0.2.1") {
			t.Fatal("early limit")
		}
	}
	if !a.limited("192.0.2.1") || a.limited("192.0.2.2") {
		t.Fatal("limits shared across clients")
	}
	a.resetLimit("192.0.2.1")
	if a.limited("192.0.2.1") {
		t.Fatal("successful login did not reset")
	}
	req := httptest.NewRequest("POST", "/api/auth/login", nil)
	req.RemoteAddr = "[2001:db8::1]:1234"
	req.Header.Set("X-Forwarded-For", "192.0.2.2")
	if clientIP(req) != "2001:db8::/64" {
		t.Fatal("untrusted header affected peer identity", clientIP(req))
	}
}

func TestLoginLimitBucketsIPv6PrefixAndEvictsOldest(t *testing.T) {
	t.Parallel()
	ip := func(address string) string {
		req := httptest.NewRequest("POST", "/api/auth/login", nil)
		req.RemoteAddr = address
		return clientIP(req)
	}
	if ip("[2001:db8::1]:1") != ip("[2001:db8::ffff:2]:2") || ip("[2001:db8:0:1::1]:1") == ip("[2001:db8::1]:1") {
		t.Fatal("IPv6 clients must share one /64 bucket")
	}
	if ip("[::ffff:192.0.2.9]:1") != "192.0.2.9" {
		t.Fatal("IPv4-mapped peer was not normalized")
	}
	a := &auth{now: time.Now, attempts: map[string]loginWindow{}}
	a.attempts["oldest"] = loginWindow{attempts: 11, start: time.Now().Add(-30 * time.Second)}
	for i := range 4095 {
		a.attempts[fmt.Sprint("client-", i)] = loginWindow{attempts: 1, start: time.Now()}
	}
	if a.limited("198.51.100.1") {
		t.Fatal("full limiter table refused a new client")
	}
	if _, ok := a.attempts["oldest"]; ok || len(a.attempts) != 4096 {
		t.Fatal("full limiter table did not evict the oldest window", len(a.attempts))
	}
}
