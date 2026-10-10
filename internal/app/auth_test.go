package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredAccountRequiresRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	text := "listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: first-password\nsources: []\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(context.Background()) })
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
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Config.Reload(); err != nil {
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
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(context.Background()) })
	if _, err := b.auth.login("new-operator", "second-password"); err != nil {
		t.Fatal("new configured account rejected after restart")
	}
	if _, err := b.auth.login("operator", "first-password"); err == nil || b.auth.authorized(request) {
		t.Fatal("old account or old session survived restart")
	}
	if value, err := b.Store.State("admin_password"); err != nil || value != "" {
		t.Fatal("new password was persisted to SQLite", err)
	}
}

func TestLoginLimitsSeparateClientsAndReset(t *testing.T) {
	a := &auth{attempts: map[string]loginWindow{}}
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
	if clientIP(req) != "2001:db8::1" {
		t.Fatal("untrusted header affected peer identity")
	}
}
