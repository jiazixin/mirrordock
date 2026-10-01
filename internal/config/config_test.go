package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsAndCandidates(t *testing.T) {
	s := Source{Upstreams: map[string]string{"src10": "ten", "src2": "two", "src1": "one"}}
	if strings.Join(s.Candidates(), ",") != "one,two,ten" {
		t.Fatal(s.Candidates())
	}
	if !DefaultBool(s.Rsync.Delete) || !DefaultBool(s.Rsync.PreserveHardlinks) || DefaultBool(new(false)) {
		t.Fatal("defaults")
	}
}
func TestWritebackConflictAndSecrets(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	b := []byte("# keep me\nlisten: 127.0.0.1\nport: 8080\npath: ./data\nmirror_root: ./mirror\nsettings:\n  github:\n    token: secret\n    proxy: http://user:proxy-secret@127.0.0.1:8081\nsources: []\n")
	if err := os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	v := m.View()
	if v.Config.Settings.GitHub.Token != "" || !v.TokenConfigured {
		t.Fatal("token leaked")
	}
	if v.Config.Settings.GitHub.Proxy != "" || !v.ProxyConfigured {
		t.Fatal("proxy credentials leaked")
	}
	c := m.Current()
	c.Port = 8091
	if err = m.Save(c, v.Revision, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	if !strings.Contains(string(got), "# keep me") || !strings.Contains(string(got), "secret") || !strings.Contains(string(got), "proxy-secret") {
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
