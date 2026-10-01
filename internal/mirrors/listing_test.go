package mirrors

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListingFileKinds(t *testing.T) {
	for _, tc := range []struct{ name, kind, group, icon string }{
		{"linux.RPM", "RPM", "package", "package"},
		{"client.deb", "DEB", "package", "package"},
		{"source.tar.gz", "TAR.GZ", "archive", "archive"},
		{"source.tar.xz", "TAR.XZ", "archive", "archive"},
		{"index.json", "JSON", "metadata", "metadata"},
		{"repomd.xml", "XML", "metadata", "metadata"},
		{".treeinfo", "仓库元数据", "metadata", "metadata"},
		{"InRelease", "仓库元数据", "metadata", "metadata"},
		{"SHA256SUMS", "校验和", "metadata", "checksum"},
		{"SHA256SUMS.asc", "签名 · ASC", "metadata", "signature"},
		{"RPM-GPG-KEY-repo", "签名密钥", "metadata", "signature"},
		{"image.iso", "ISO", "other", "disk"},
		{"LICENSE", "文本", "other", "text"},
		{"unknown.bin", "文件", "other", "file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, group, icon := fileKind(tc.name)
			if kind != tc.kind || group != tc.group || icon != tc.icon {
				t.Fatalf("got %q %q %q", kind, group, icon)
			}
		})
	}
}

func TestListingEscapingBreadcrumbsAndScriptPolicy(t *testing.T) {
	name := `a<&".json`
	p := "/repo/<unsafe>/"
	items := []listingItem{newListingItem(name, p+url.PathEscape(name), false, nil), newListingItem("folder", p+"folder/", true, nil)}
	w := httptest.NewRecorder()
	renderListing(w, httptest.NewRequest("GET", "/repo/%3Cunsafe%3E/", nil), items, false)
	html := w.Body.String()
	for _, want := range []string{`a&lt;&amp;&#34;.json`, `href="/repo/%3Cunsafe%3E/"`, `href="/repo/"`, `aria-current="page"`, `data-bytes="-1"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing escaped content: %s", want)
		}
	}
	if strings.Contains(html, "<unsafe>") || strings.Index(html, `data-name="folder"`) > strings.Index(html, `data-name="a&lt;`) {
		t.Fatal("unescaped markup or files before directories")
	}
	_, script, ok := strings.Cut(html, "<script>")
	if !ok {
		t.Fatal("script missing")
	}
	script, _, _ = strings.Cut(script, "</script>")
	sum := sha256.Sum256([]byte(script))
	policy := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "script-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") || strings.Contains(policy, "script-src 'unsafe-inline'") {
		t.Fatal("rendered script must match CSP hash")
	}
	head := httptest.NewRecorder()
	renderListing(head, httptest.NewRequest("HEAD", "/", nil), nil, false)
	if head.Body.Len() != 0 || head.Header().Get("Content-Security-Policy") != policy {
		t.Fatal("HEAD must retain headers without rendering a body")
	}
}

func TestDirectoryListingMetadataAndBounds(t *testing.T) {
	m, _ := testManager(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "client.deb")
	if err := os.WriteFile(file, make([]byte, 2048), 0600); err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2026, 10, 1, 10, 30, 0, 0, time.Local)
	if err := os.Chtimes(file, modified, modified); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "packages"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing-secret"), filepath.Join(dir, "external-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.part"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	headFile, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer headFile.Close()
	head := httptest.NewRecorder()
	m.directory(head, httptest.NewRequest("HEAD", "/repo/", nil), headFile)
	remaining, err := headFile.ReadDir(-1)
	if err != nil || len(remaining) != 4 || head.Body.Len() != 0 || head.Code != 200 {
		t.Fatal("directory HEAD must not enumerate entries", err, len(remaining), head.Code)
	}
	read := func() string {
		t.Helper()
		f, err := os.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		w := httptest.NewRecorder()
		m.directory(w, httptest.NewRequest("GET", "/repo/", nil), f)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		return w.Body.String()
	}
	html := read()
	for _, want := range []string{`href="/repo/packages/"`, `data-bytes="2048"`, `2.0 KiB`, `2026-10-01 10:30`, `符号链接`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing metadata: %s", want)
		}
	}
	if strings.Contains(html, "secret.part") || strings.Contains(html, "missing-secret") {
		t.Fatal("private entry or link target leaked")
	}
	for i := range 1005 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("item-%04d.txt", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	html = read()
	if strings.Count(html, "<tr data-name=") > 1000 || !strings.Contains(html, "搜索与筛选仅针对已列出的条目") {
		t.Fatal("directory must retain its bounded listing and partial-results notice")
	}
}

func BenchmarkDirectoryListing1000(b *testing.B) {
	dir := b.TempDir()
	for i := range 1000 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("package-%04d.rpm", i)), nil, 0600); err != nil {
			b.Fatal(err)
		}
	}
	m := &Manager{listingGates: make(chan struct{}, 8)}
	r := httptest.NewRequest("GET", "/repo/", nil)
	b.ReportAllocs()
	for b.Loop() {
		f, err := os.Open(dir)
		if err != nil {
			b.Fatal(err)
		}
		w := httptest.NewRecorder()
		m.directory(w, r, f)
		f.Close()
		if w.Code != 200 {
			b.Fatal(w.Code)
		}
	}
}

func TestSaturatedListingPreservesDownloadsAndHEAD(t *testing.T) {
	m, source := testManager(t)
	work, err := m.Prepare(t.Context(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "manifest.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(t.Context(), source, 1); err != nil {
		t.Fatal(err)
	}
	for range cap(m.listingGates) {
		m.listingGates <- struct{}{}
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/repo/", 503},
		{"GET", "/", 503},
		{"HEAD", "/repo/", 200},
		{"HEAD", "/", 200},
		{"GET", "/repo/manifest.json", 200},
	} {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status || (tc.status == 503 && w.Header().Get("Retry-After") != "3") {
			t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
		}
	}
}
