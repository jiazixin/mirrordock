package config

import (
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDestinationSymlinkAliasesCannotOverlap(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mirror")
	if err := os.MkdirAll(filepath.Join(root, "original", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("original", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{"alias", "alias/nested"} {
		c := Defaults()
		c.Auth = Auth{Username: "admin", Password: "fixture"}
		c.Path, c.MirrorRoot = "./state", "./mirror"
		c.Sources = []Source{
			{ID: "first", Name: "First", Type: "rsync", Dest: "original", Upstreams: map[string]string{"src1": "rsync://localhost/repo/"}},
			{ID: "second", Name: "Second", Type: "rsync", Dest: destination, Upstreams: map[string]string{"src1": "rsync://localhost/repo/"}},
		}
		if err := c.Validate(filepath.Join(dir, "config.yaml")); err == nil {
			t.Fatal("overlapping symlink destination accepted", destination)
		}
		c.Sources[1].Dest = "independent"
		if err := c.Validate(filepath.Join(dir, "config.yaml")); err != nil {
			t.Fatal("independent destination rejected", err)
		}
	}
}

func TestSourceNumericBounds(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	base := "auth:\n  username: admin\n  password: fixture\npath: ./state\nmirror_root: ./mirror\nsources:\n  - id: repo\n    name: Repo\n    type: rsync\n    dest: repo\n    src1: rsync://localhost/repo/\n"
	for _, extra := range []string{"    src9999999999999999999999999: rsync://localhost/repo/\n", "    rsync:\n      uid: 4294967295\n", "    rsync:\n      gid: -1\n"} {
		if _, err := Parse([]byte(base+extra), file); err == nil {
			t.Fatal("invalid numeric bound accepted", extra)
		}
	}
	if _, err := Parse([]byte(base+"    rsync:\n      uid: 4294967294\n      gid: 0\n"), file); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultsAndCandidates(t *testing.T) {
	if Defaults().Log != (Log{Level: "info", Format: "json"}) {
		t.Fatal("wrong logging defaults")
	}
	s := Source{Upstreams: map[string]string{"src10": "ten", "src2": "two", "src1": "one"}}
	if strings.Join(s.Candidates(), ",") != "one,two,ten" {
		t.Fatal(s.Candidates())
	}
	if !DefaultBool(s.Rsync.Delete) || !DefaultBool(s.Rsync.PreserveHardlinks) || DefaultBool(new(false)) {
		t.Fatal("defaults")
	}
}

func TestLogConfiguration(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	base := "listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: test-secret\nsources: []\n"
	for _, section := range []string{"", "log: {}\n", "log:\n  level: debug\n", "log:\n  format: text\n  path: ./logs/server.log\n"} {
		c, err := Parse([]byte(base+section), file)
		if err != nil || c.Log.Level == "" || c.Log.Format == "" {
			t.Fatal("valid log configuration rejected", err)
		}
	}
	for _, section := range []string{"log:\n  level: trace\n", "log:\n  format: xml\n", "log:\n  level: ''\n", "log:\n  unknown: true\n", "log:\n  path: './mirror/server.log'\n", "log:\n  path: './data/data.sqlite'\n", "log:\n  path: './data/data.sqlite-wal'\n", "log:\n  path: './data/.mirrordock.lock'\n", "log:\n  path: './config.yaml'\n", "log:\n  path: './data/logs/1/1.log'\n", "log:\n  path: ' trailing '\n"} {
		if _, err := Parse([]byte(base+section), file); err == nil {
			t.Fatal("unsafe log configuration accepted", section)
		}
	}
	text := base + "log:\n  level: info\n  path: '' # keep log comment\n  format: json\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	c := m.View().Config
	c.Log = Log{Level: "error", Path: "./logs/server.log", Format: "text"}
	if err := m.Save(c, m.View().Revision, nil, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), "# keep log comment") || m.Current().Log != Defaults().Log || m.View().RestartRequired {
		t.Fatal("browser save changed deployment logging", err)
	}
	text = base + "log:\n  level: error\n  path: ./logs/server.log\n  format: text\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(); err != nil || m.Current().Log != c.Log {
		t.Fatal("log reload failed", err)
	}
	if !m.View().RestartRequired || m.Startup().Log != Defaults().Log {
		t.Fatal("YAML logging edit did not retain startup settings")
	}
	for _, link := range []string{"hardlink.log", "symlink.log"} {
		p := filepath.Join(filepath.Dir(file), link)
		var err error
		if link == "hardlink.log" {
			err = os.Link(file, p)
		} else {
			err = os.Symlink(file, p)
		}
		if err != nil {
			t.Fatal(err)
		}
		c.Log.Path = p
		if err := c.Validate(file); err == nil {
			t.Fatal("log path alias of config accepted", link)
		}
	}
}

func TestWritebackConflictAndSecrets(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	b := []byte("# keep me\nlisten: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: yaml-auth-secret\nsettings:\n  github:\n    token: secret\n    proxy: http://user:proxy-secret@127.0.0.1:8081\nsources: []\n")
	if err := os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	v := m.View()
	if v.Config.Auth.Password != "" || v.RestartRequired || m.Current().Auth.Password != "yaml-auth-secret" || m.Startup().Auth.Password != "yaml-auth-secret" {
		t.Fatal("authentication lost during cloning, leaked, or incorrectly requires restart")
	}
	encoded, err := json.Marshal(m.Current())
	if err != nil || strings.Contains(string(encoded), "yaml-auth-secret") || strings.Contains(string(encoded), `"password"`) {
		t.Fatal("JSON exposed auth password", err)
	}
	if v.Config.Settings.GitHub.Token != "" || !v.TokenConfigured {
		t.Fatal("token leaked")
	}
	if v.Config.Settings.GitHub.Proxy != "http://user:proxy-secret@127.0.0.1:8081" || !v.ProxyConfigured {
		t.Fatal("configured proxy missing from admin view")
	}
	c := m.Current()
	c.Listen = "192.0.2.10"
	c.Port = 8091
	c.Path = "browser-data"
	c.MirrorRoot = "browser-mirrors"
	c.TLS = TLS{Cert: "browser-cert", Key: "browser-key"}
	c.Auth = Auth{Username: "browser-override", Password: "browser-password"}
	if err = m.Save(c, v.Revision, nil, nil); err != nil {
		t.Fatal(err)
	}
	saved := m.Current()
	if saved.Listen != v.Config.Listen || saved.Port != v.Config.Port || saved.Path != v.Config.Path || saved.MirrorRoot != v.Config.MirrorRoot || saved.TLS != v.Config.TLS || m.View().RestartRequired {
		t.Fatal("browser save changed YAML-only deployment settings")
	}
	got, _ := os.ReadFile(file)
	if !strings.Contains(string(got), "# keep me") || !strings.Contains(string(got), "secret") || !strings.Contains(string(got), "proxy-secret") || !strings.Contains(string(got), "yaml-auth-secret") || !strings.Contains(string(got), "username: operator") {
		t.Fatal(string(got))
	}
	if !errors.Is(m.Save(c, v.Revision, nil, nil), ErrConflict) {
		t.Fatal("stale revision accepted")
	}
	if err = m.Save(c, m.View().Revision, new(""), nil); err != nil {
		t.Fatal(err)
	}
	if m.View().TokenConfigured {
		t.Fatal("cannot clear token")
	}
	if err = m.Save(m.View().Config, m.View().Revision, nil, new("socks5://127.0.0.1:1080")); err != nil {
		t.Fatal(err)
	}
	if m.Current().Settings.GitHub.Proxy != "socks5://127.0.0.1:1080" {
		t.Fatal("cannot replace proxy")
	}
	if err = m.Save(m.View().Config, m.View().Revision, nil, nil); err != nil || !m.View().ProxyConfigured {
		t.Fatal("unrelated configuration save lost proxy", err)
	}
	if err = m.Save(m.View().Config, m.View().Revision, nil, new("")); err != nil || m.View().ProxyConfigured {
		t.Fatal("cannot clear proxy", err)
	}
	if m.Current().Auth != (Auth{Username: "operator", Password: "yaml-auth-secret"}) {
		t.Fatal("unrelated saves changed account credentials")
	}
}

func TestAuthenticationValidation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	for _, auth := range []Auth{{}, {Username: "admin"}, {Password: "test-secret"}, {Username: " admin", Password: "test-secret"}, {Username: "admin\n", Password: "test-secret"}, {Username: "admin\x1b", Password: "test-secret"}, {Username: strings.Repeat("a", 129), Password: "test-secret"}, {Username: "admin", Password: "  "}, {Username: "admin", Password: strings.Repeat("a", 73)}, {Username: "admin", Password: strings.Repeat("密", 25)}} {
		c := Defaults()
		c.Auth = auth
		c.Path, c.MirrorRoot = "./data", "./mirror"
		err := c.Validate(file)
		if err == nil || strings.Contains(err.Error(), "test-secret") {
			t.Fatal("invalid authentication accepted or secret exposed")
		}
	}
	c := Defaults()
	c.Auth = Auth{Username: "管理员", Password: "请替换为你的密码"}
	c.Path, c.MirrorRoot = "./data", "./mirror"
	if err := c.Validate(file); err != nil {
		t.Fatal("non-ASCII credentials rejected", err)
	}
	if _, err := Parse([]byte("listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nsources: []\n"), file); err == nil {
		t.Fatal("missing authentication accepted")
	}
}

func TestAuthenticationReloadAndWriteback(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	text := "listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: first-secret # preserve account comment\nsources: []\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(text, "first-secret", "second-secret", 1)
	if err := os.WriteFile(file, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	view := m.View()
	if !view.RestartRequired || view.Config.Auth.Password != "" || m.Startup().Auth.Password != "first-secret" || m.Current().Auth.Password != "second-secret" {
		t.Fatal("password reload did not preserve startup account or require restart")
	}
	if err := m.Save(view.Config, view.Revision, nil, nil); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(written), "second-secret") || !strings.Contains(string(written), "# preserve account comment") {
		t.Fatal("save lost reloaded password or comment", err)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(updated, "second-secret", "\"\"", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(); err == nil || m.Current().Auth.Password != "second-secret" {
		t.Fatal("invalid reload replaced the active configuration")
	}
}

func TestGitHubProxyValidation(t *testing.T) {
	for _, v := range []string{"", "http://127.0.0.1:8081", "https://user:pass@proxy.example:443", "socks5://[::1]:1080", "socks5h://user:pass@proxy.example:1080"} {
		if _, err := ParseProxyURL(v); err != nil {
			t.Fatalf("valid proxy rejected: %v", err)
		}
	}
	for _, v := range []string{"127.0.0.1:8081", "ftp://user:secret@proxy:21", "http://", "http://proxy:99999", "http://proxy:0", "http://proxy/path", "http://proxy?token=secret", "http://proxy#secret", "http://user:%0Asecret@proxy:80", " http://proxy:80"} {
		if _, err := ParseProxyURL(v); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid proxy accepted or credential exposed")
		}
	}
}

func TestUnsafeConfig(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	for _, dest := range []string{"../escape", "admin/evil", "api", "favicon.ico", ".internal", "a/../b"} {
		c := Defaults()
		c.Auth = Auth{Username: "admin", Password: "fixture-password"}
		c.Path = "./data"
		c.MirrorRoot = "./mirror"
		c.Sources = []Source{{ID: "test", Name: "Test", Type: "rsync", Dest: dest, Upstreams: map[string]string{"src1": "rsync://localhost/test/"}}}
		if err := c.Validate(file); err == nil {
			t.Fatalf("accepted %s", dest)
		}
	}
}

func TestExampleConfigurationLoads(t *testing.T) {
	data, err := os.ReadFile("../../configs/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(data, filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sources) != 5 || c.Settings.TaskTimeout != "8h" || !DefaultBool(c.Sources[0].Rsync.Delete) {
		t.Fatal("example does not match implemented schema")
	}
	c.Sources[0].Schedule.Cron = "CRON_TZ=UTC 0 2 * * *"
	if err = c.Validate(filepath.Join(t.TempDir(), "config.yaml")); err == nil {
		t.Fatal("cron overrode process timezone")
	}
}

func TestGitHubTokenRejectsWhitespaceWithoutExposingValue(t *testing.T) {
	for _, token := range []string{" secret-token", "secret-token\n", "Bearer secret-token", "secret\ttoken", "secret\x00token"} {
		c := Defaults()
		c.Auth = Auth{Username: "operator", Password: "test-password"}
		c.Settings.GitHub.Token = token
		err := c.Validate(filepath.Join(t.TempDir(), "config.yaml"))
		if err == nil || !strings.Contains(err.Error(), "github.token") || strings.Contains(err.Error(), "secret") {
			t.Fatal("malformed token accepted or exposed")
		}
	}
}

func TestRetryConfigurationDefaultsValidationAndWriteback(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	base := "listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: test-secret\nsources: []\n"
	for _, tc := range []struct {
		section string
		want    int
	}{{"", 3}, {"settings: {}\n", 3}, {"settings:\n  retry_count: 0\n", 0}, {"settings:\n  retry_count: 10\n", 10}} {
		c, err := Parse([]byte(base+tc.section), file)
		if err != nil || int(c.Settings.RetryCount) != tc.want {
			t.Fatal(c.Settings.RetryCount, err)
		}
	}
	for _, v := range []string{"-1", "11", "1.5", "invalid"} {
		if _, err := Parse([]byte(base+"settings:\n  retry_count: "+v+"\n"), file); err == nil {
			t.Fatal("invalid retry accepted", v)
		}
	}
	if err := os.WriteFile(file, []byte(base+"settings:\n  retry_count: 3 # keep retry comment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Current()
	c.Settings.RetryCount = 0
	if err = m.Save(c, m.View().Revision, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Reload(); err != nil || m.View().Config.Settings.RetryCount != 0 {
		t.Fatal("zero did not persist", err)
	}
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), "retry_count: 0 # keep retry comment") {
		t.Fatal("comment lost", err)
	}
}

func TestConcurrencyDefaultsValidationAndWriteback(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	base := "listen: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: operator\n  password: test-secret\nsources: []\n"
	for _, tc := range []struct {
		section string
		want    Concurrency
	}{
		{"", Concurrency{Rsync: 4, RepoSync: 4, GitHubRelease: 4}},
		{"settings:\n  concurrency:\n    rsync: 1\n", Concurrency{Rsync: 1, RepoSync: 4, GitHubRelease: 4}},
		{"settings:\n  concurrency:\n    reposync: 16\n    github_release: 2\n", Concurrency{Rsync: 4, RepoSync: 16, GitHubRelease: 2}},
	} {
		c, err := Parse([]byte(base+tc.section), file)
		if err != nil || c.Settings.Concurrency != tc.want {
			t.Fatal(tc.section, c.Settings.Concurrency, err)
		}
	}
	if Defaults().Settings.Concurrency.Limit("unknown") != 0 {
		t.Fatal("unknown type received slots")
	}
	for _, v := range []string{"0", "17", "-1", "1.5", "invalid"} {
		if _, err := Parse([]byte(base+"settings:\n  concurrency:\n    github_release: "+v+"\n"), file); err == nil {
			t.Fatal("invalid concurrency accepted", v)
		}
	}
	if _, err := Parse([]byte(base+"settings:\n  concurrency:\n    git: 1\n"), file); err == nil {
		t.Fatal("unknown concurrency type accepted")
	}
	if err := os.WriteFile(file, []byte(base), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Current()
	c.Settings.Concurrency.Rsync = 2
	if err = m.Save(c, m.View().Revision, nil, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), "concurrency:\n    rsync: 2\n    reposync: 4\n    github_release: 4\n") {
		t.Fatal("concurrency not written back", string(b), err)
	}
	var view View
	encoded, _ := json.Marshal(m.View())
	if err = json.Unmarshal(encoded, &view); err != nil || view.Config.Settings.Concurrency.Rsync != 2 {
		t.Fatal("concurrency missing from read API", err)
	}
}
