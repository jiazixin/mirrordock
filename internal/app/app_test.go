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
	"mirrordock/internal/database"
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
	text := "# preserve comment\nlisten: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: correct password 123\nsettings:\n  github:\n    token: secret-token\n    proxy: http://user:proxy-password@127.0.0.1:8081\nsources: []\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	// A password row from an older version must not override YAML credentials.
	legacy, err := database.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.SetState("admin_password", "old-inert-hash"); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
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
	body, _ := json.Marshal(map[string]string{"username": "operator", "password": "correct password 123"})
	res = request("POST", "/api/auth/login", string(body), "https://other.example")
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("cross origin mutation", res.StatusCode)
	}
	for _, invalid := range []string{`{"password":"correct password 123"}`, `{"username":"admin","password":"correct password 123"}`, `{"username":"operator","password":"wrong password"}`} {
		res = request("POST", "/api/auth/login", invalid, server.URL)
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 401 || !strings.Contains(string(b), "用户名或密码错误") {
			t.Fatal("invalid account accepted or exposed", res.StatusCode)
		}
	}
	res = request("GET", "/api/auth/status", "", "")
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(b), "username") || strings.Contains(string(b), "initialized") {
		t.Fatal("anonymous status exposed an account or obsolete setup state")
	}
	res = request("POST", "/api/auth/login", string(body), server.URL)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("login failed", res.StatusCode)
	}
	cookies := res.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/api/" {
		t.Fatal("session cookie protections missing")
	}
	res = request("GET", "/api/auth/status", "", "")
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), `"username":"operator"`) {
		t.Fatal("authenticated status omitted configured username")
	}
	generation := func() uint64 {
		a.progress.mu.Lock()
		defer a.progress.mu.Unlock()
		return a.progress.generation
	}
	// Anonymous login attempts must not force fresh SSE snapshot queries.
	if generation() != 0 {
		t.Fatal("public login invalidated progress frames", generation())
	}
	res = request("POST", "/api/auth/setup", "{}", server.URL)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatal("obsolete setup endpoint still available", res.StatusCode)
	}
	if generation() != 1 {
		t.Fatal("authenticated write kept the cached progress frame", generation())
	}
	res = request("GET", "/api/config", "", "")
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(b), "secret-token") || strings.Contains(string(b), "correct password 123") || strings.Contains(string(b), `"password"`) {
		t.Fatal("token leaked")
	}
	var view config.View
	if err = json.Unmarshal(b, &view); err != nil {
		t.Fatal(err)
	}
	if view.Config.Settings.GitHub.Proxy != "http://user:proxy-password@127.0.0.1:8081" {
		t.Fatal("admin view omitted configured proxy")
	}
	view.Config.Settings.GitHub.ReleaseCount = 8
	view.Config.Auth.Username = "browser-override"
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
	if !strings.Contains(string(written), "# preserve comment") || !strings.Contains(string(written), "secret-token") || !strings.Contains(string(written), "new-proxy-secret") || !strings.Contains(string(written), "correct password 123") || !strings.Contains(string(written), "username: operator") {
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
	os.WriteFile(file, []byte("listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: correct password 123\ntls:\n  cert: ./cert.pem\n  key: ./key.pem\nsources:\n  - id: repo\n    name: Repo\n    type: rsync\n    dest: repo\n    src1: rsync://localhost/repo/\n"), 0600)
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

func TestSystemInfoUsesStartupDeployment(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	original := "listen: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: startup-password\nsettings:\n  github:\n    token: startup-token\n    proxy: http://user:proxy-password@127.0.0.1:8081\nsources: []\n"
	if err := os.WriteFile(file, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	updated := strings.ReplaceAll(original, "8080", "8090")
	updated = strings.ReplaceAll(updated, "./data", "./new-data")
	if err := os.WriteFile(file, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Config.Reload(); err != nil {
		t.Fatal(err)
	}
	if !a.Config.View().RestartRequired {
		t.Fatal("deployment change did not require restart")
	}
	recorder := httptest.NewRecorder()
	a.info(recorder, httptest.NewRequest("GET", "/api/system/info", nil))
	var info struct {
		OS         string `json:"os"`
		Arch       string `json:"arch"`
		CPUs       int    `json:"cpus"`
		Deployment struct {
			Listen     string     `json:"listen"`
			Data       string     `json:"data"`
			Database   string     `json:"database"`
			MirrorRoot string     `json:"mirror_root"`
			Log        config.Log `json:"log"`
			TLS        config.TLS `json:"tls"`
		} `json:"deployment"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.OS == "" || info.Arch == "" || info.CPUs < 1 {
		t.Fatal("runtime fields missing")
	}
	canonicalDir := filepath.Dir(a.Config.File())
	if info.Deployment.Listen != "127.0.0.1:8080" || info.Deployment.Data != filepath.Join(canonicalDir, "data") || info.Deployment.Database != filepath.Join(canonicalDir, "data", "data.sqlite") || info.Deployment.MirrorRoot != filepath.Join(canonicalDir, "mirror") || info.Deployment.Log != config.Defaults().Log || info.Deployment.TLS != (config.TLS{}) {
		t.Fatal("runtime deployment does not match startup configuration")
	}
	for _, secret := range []string{"startup-password", "startup-token", "proxy-password"} {
		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatal("deployment info exposed a credential")
		}
	}
}

func TestListenFailureLeavesQueuedTaskUntouched(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: fixture\nsources:\n  - id: repo\n    name: Repo\n    type: rsync\n    enabled: true\n    dest: repo\n    src1: rsync://127.0.0.1:1/repo/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	a.Version = "test-build-version"
	run, err := a.Runs.Enqueue("repo", "manual")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	a.Server.Addr = listener.Addr().String()
	if err := a.Serve(); err == nil {
		t.Fatal("occupied port accepted")
	}
	got, err := a.Runs.Get(run.ID)
	if err != nil || got.State != "queued" || len(got.Attempts) != 0 {
		t.Fatal(got, err)
	}
	w := httptest.NewRecorder()
	a.info(w, httptest.NewRequest("GET", "/api/system/info", nil))
	if !strings.Contains(w.Body.String(), "test-build-version") {
		t.Fatal(w.Body.String())
	}
	if err := a.Store.ReadDB.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/runs/999999", nil)
	req.SetPathValue("id", "999999")
	w = httptest.NewRecorder()
	a.getRun(w, req)
	if w.Code != 503 {
		t.Fatal("database failure misreported", w.Code)
	}
}
