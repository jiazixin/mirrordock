package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json/v2"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"mirrordock/internal/config"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminAuthenticationWritebackAndPublicIsolation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	text := "# preserve comment\nlisten: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nsettings:\n  github:\n    token: secret-token\n    proxy: http://user:proxy-password@127.0.0.1:8081\nsources: []\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	if other, err := Open(file); err == nil {
		other.Close(context.Background())
		t.Fatal("same data dir opened twice")
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	request := func(method, path, body, origin string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("X-MirrorDock-Request", "1")
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := request("GET", "/api/config", "", "")
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("anonymous admin access", res.StatusCode)
	}
	// Both public and admin pages must get the same real ICO before login.
	var icon []byte
	for _, path := range []string{"/favicon.ico", "/admin/favicon.ico"} {
		res = request("GET", path+"?v=2", "", "")
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK || !bytes.HasPrefix(data, []byte{0, 0, 1, 0, 6, 0}) {
			t.Fatal("invalid embedded favicon", path, res.StatusCode, err)
		}
		if icon != nil && !bytes.Equal(icon, data) {
			t.Fatal("public and admin favicon differ")
		}
		icon = data
		res = request("HEAD", path, "", "")
		data, err = io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK || len(data) != 0 || res.ContentLength != int64(len(icon)) {
			t.Fatal("favicon HEAD failed", path, err)
		}
	}
	res = request("GET", "/admin/missing.ico", "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatal("missing icon served as SPA")
	}
	body, _ := json.Marshal(map[string]string{"code": a.SetupCode(), "password": "correct password 123"})
	res = request("POST", "/api/auth/setup", string(body), "https://other.example")
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("cross origin mutation", res.StatusCode)
	}
	res = request("POST", "/api/auth/setup", string(body), server.URL)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode, string(b))
	}
	res = request("POST", "/api/auth/login", `{"password":"correct password 123"}`, server.URL)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("login failed", res.StatusCode)
	}
	if a.SetupCode() != "" {
		t.Fatal("setup code reused")
	}
	res = request("GET", "/api/config", "", "")
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(b), "secret-token") || strings.Contains(string(b), "proxy-password") {
		t.Fatal("token leaked")
	}
	var view config.View
	if err = json.Unmarshal(b, &view); err != nil {
		t.Fatal(err)
	}
	view.Config.Settings.GitHub.ReleaseCount = 8
	payload, _ := json.Marshal(map[string]any{"config": view.Config, "revision": view.Revision, "proxy_update": "socks5://user:new-proxy-secret@127.0.0.1:1080"})
	res = request("PUT", "/api/config", string(payload), server.URL)
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode, string(b))
	}
	res = request("PUT", "/api/config", string(payload), server.URL)
	res.Body.Close()
	if res.StatusCode != 409 {
		t.Fatal("stale configuration accepted", res.StatusCode)
	}
	written, _ := os.ReadFile(file)
	if !strings.Contains(string(written), "# preserve comment") || !strings.Contains(string(written), "secret-token") || !strings.Contains(string(written), "new-proxy-secret") {
		t.Fatal("lost comment or token")
	}
	for _, path := range []string{"/data.sqlite", "/.mirrordock/work/1", "/config.yaml", "/missing.rpm"} {
		res = request("GET", path, "", "")
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatal("private path accessible", path, res.StatusCode)
		}
	}
	res = request("GET", "/admin/records", "", "")
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), "/admin/assets/") {
		t.Fatal("embedded Vue missing", res.StatusCode)
	}
	res = request("POST", "/api/auth/logout", "{}", server.URL)
	res.Body.Close()
	res = request("GET", "/api/config", "", "")
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("logout left session active")
	}
}

func TestTLSHTTP2PublicDownload(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600)
	file := filepath.Join(dir, "config.yaml")
	os.WriteFile(file, []byte("listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\ntls:\n  cert: ./cert.pem\n  key: ./key.pem\nsources:\n  - id: repo\n    name: Repo\n    type: rsync\n    dest: repo\n    src1: rsync://localhost/repo/\n"), 0600)
	a, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	s := a.Config.Current().Sources[0]
	work, err := a.Mirrors.Prepare(t.Context(), s, 1)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(work, "pkg.rpm"), []byte("package"), 0644)
	if _, err = a.Mirrors.Publish(t.Context(), s, 1); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Server.ServeTLS(listener, certFile, keyFile) }()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	res, err := client.Get("https://" + listener.Addr().String() + "/repo/pkg.rpm")
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != 200 || string(b) != "package" || res.ProtoMajor != 2 {
		t.Fatal(res.StatusCode, res.Proto, string(b), err)
	}
	if err = a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}

func TestTCPStallWatcherPreservesActiveProgress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	monitor := &connections{entries: map[net.Conn]*connection{}}
	monitor.state(server, http.StateNew)
	monitor.state(server, http.StateActive)
	if _, err = transmitted(server); err != nil {
		t.Fatal("socket progress metrics unavailable", err)
	}
	if _, err = server.Write([]byte("progress")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err = io.ReadFull(client, buf); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(6 * time.Minute)
	monitor.check(now)
	if _, err = server.Write([]byte("more")); err != nil {
		t.Fatal("progressing connection was closed", err)
	}
	if _, err = io.ReadFull(client, buf[:4]); err != nil {
		t.Fatal(err)
	}
	monitor.check(now.Add(time.Second))
	monitor.check(now.Add(6 * time.Minute))
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = client.Read(buf); err != io.EOF {
		t.Fatal("stalled connection not closed", err)
	}
}
