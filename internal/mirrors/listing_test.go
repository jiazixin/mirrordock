package mirrors

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"golang.org/x/sys/unix"
	"html"
	"mirrordock/internal/config"
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
	for _, want := range []string{`a&lt;&amp;&#34;.json`, `title="&lt;unsafe&gt;"`, `href="/repo/"`, `aria-current="page"`, `data-bytes="-1"`} {
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

func TestListingHomeAndFullPathNavigation(t *testing.T) {
	for _, tc := range []struct {
		path, title, heading string
		links                []string
	}{
		{"/", "镜像目录", "镜像目录", nil},
		{"/github/repo%20%26%20%E9%95%9C%E5%83%8F/v1/", "/github/repo &amp; 镜像/v1/", "v1", []string{`href="/"`, `href="/github/"`, `href="/github/repo%20&amp;%20%E9%95%9C%E5%83%8F/"`}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			renderListing(w, httptest.NewRequest("GET", tc.path, nil), nil, false)
			body := w.Body.String()
			if !strings.Contains(body, "<title>"+tc.title+" · MirrorDock</title>") || !strings.Contains(body, `<h1 class="page-title">`+tc.heading+`<span class="title-dot" aria-hidden="true">.</span></h1>`) {
				t.Fatal("missing page title or heading")
			}
			for _, link := range tc.links {
				if !strings.Contains(body, link) {
					t.Errorf("missing path link %s", link)
				}
			}
			_, nav, found := strings.Cut(body, `<nav class="directory-path"`)
			if tc.links == nil {
				// The home page heading names the location, so it has no breadcrumb.
				if found || strings.Contains(body, `class="home-icon"`) {
					t.Fatal("home page must not render a breadcrumb")
				}
				return
			}
			nav, _, _ = strings.Cut(nav, "</nav>")
			if strings.Count(nav, `<a href=`) != len(tc.links) || strings.Count(nav, `aria-current="page"`) != 1 || !strings.Contains(nav, `<span class="path-current" aria-current="page"`) || !strings.Contains(nav, `class="home-icon"`) {
				t.Fatal("only ancestors should be links; current directory must be a non-clickable span")
			}
			for _, removed := range []string{"公开下载", "上级目录", "当前位置", `class="parent-link"`, `class="directory-slash"`, `class="action"`, "prefers-color-scheme"} {
				if strings.Contains(body, removed) {
					t.Errorf("obsolete UI: %s", removed)
				}
			}
		})
	}
}

func TestListingProjectSummary(t *testing.T) {
	items := []listingItem{
		{Name: "ok", Project: true, StatusClass: "success", Bytes: 1024},
		{Name: "bad", Project: true, StatusClass: "failure", Bytes: 2048},
		{Name: "pending", Project: true, StatusClass: "neutral", Bytes: -1},
		{Name: "parent", Dir: true, Bytes: 1 << 40},
	}
	got := summarizeProjects(items)
	if *got != (listingSummary{Count: 3, Healthy: 1, Attention: 1, Size: "3.0", Unit: "KiB"}) {
		t.Fatalf("%+v", *got)
	}
	if got := summarizeProjects(items[2:]); got.Size != "—" || got.Unit != "" {
		t.Fatalf("unknown size must not render as zero: %+v", *got)
	}
	w := httptest.NewRecorder()
	renderListing(w, httptest.NewRequest("GET", "/", nil), items, false)
	body := w.Body.String()
	for _, want := range []string{`<dl class="summary" aria-label="镜像概况">`, `<dd class="ok">1<small>个</small></dd>`, `<dd class="bad">1<small>个</small></dd>`, `<dd>3.0<small>KiB</small></dd>`, "全部镜像"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	w = httptest.NewRecorder()
	renderListing(w, httptest.NewRequest("GET", "/repo/", nil), []listingItem{newListingItem("a.rpm", "/repo/a.rpm", false, nil)}, false)
	if body := w.Body.String(); strings.Contains(body, `class="summary"`) || !strings.Contains(body, "文件列表") {
		t.Fatal("file listings must not render the mirror summary")
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
	if strings.Count(html, "<tr data-name=") > 1000 || !strings.Contains(html, "搜索仅针对已列出的条目") {
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

func TestProjectAndReleaseListingMetadata(t *testing.T) {
	m, source := testManager(t)
	source.Type, source.Dest = "github_release", "github/openlist"
	work, err := m.Prepare(t.Context(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	synced := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	for _, tag := range []string{"v1", "v2"} {
		dir := filepath.Join(work, tag)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "asset.zip"), make([]byte, 2048), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "v1", ".mirrordock-release-sync"), []byte(synced.Format(time.RFC3339Nano)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "v1", "partial.part"), make([]byte, 999), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "secret"), filepath.Join(work, "v1", "link")); err != nil {
		t.Fatal(err)
	}
	pub, err := m.Publish(t.Context(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pub.Current.Summary.Bytes != 4096 || pub.Current.Summary.Releases["v1"].Bytes != 2048 || !pub.Current.Summary.Releases["v1"].SyncedAt.Equal(synced) || !pub.Current.Summary.Releases["v2"].SyncedAt.IsZero() {
		t.Fatal(pub.Current.Summary)
	}
	calls := 0
	projectLast := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	projectNext := projectLast.Add(time.Hour)
	m.ListingInfo = func(context.Context) map[string]SourceListingInfo {
		calls++
		return map[string]SourceListingInfo{source.ID: {State: "failed", Last: projectLast, Next: projectNext}}
	}
	read := func(p string) string {
		t.Helper()
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		return html.UnescapeString(w.Body.String())
	}
	if html := read("/"); strings.Contains(html, `class="project-table"`) || calls != 0 {
		t.Fatal("virtual parent must retain ordinary directory metadata")
	}
	repositoryHTML := read("/github/")
	for _, want := range []string{`class="project-table"`, "上次同步", "下次同步", "同步失败", "4.0 KiB", projectLast.In(time.Local).Format(time.RFC3339), projectNext.In(time.Local).Format(time.RFC3339)} {
		if !strings.Contains(repositoryHTML, want) {
			t.Errorf("repository row missing %s", want)
		}
	}
	if calls != 1 {
		t.Fatal("source metadata must be loaded once per repository listing", calls)
	}
	html := read("/github/openlist/")
	for _, want := range []string{"类型", "大小", "修改时间", "2.0 KiB"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
	if calls != 1 {
		t.Fatal("release listing must not query project sync metadata", calls)
	}
	for _, removed := range []string{`class="project-table"`, "上次同步", "下次同步", "同步失败", `class="filters"`, `class="page-footer"`, "浏览目录，获取", "软件镜像 · 公开文件服务"} {
		if strings.Contains(html, removed) {
			t.Errorf("obsolete UI: %s", removed)
		}
	}
	if strings.Index(html, `class="search"`) < strings.Index(html, `id="files-heading"`) {
		t.Fatal("list title must precede search in the header toolbar")
	}
	if html := read("/github/openlist/v1/"); strings.Contains(html, `class="project-table"`) || calls != 1 {
		t.Fatal("asset directory must retain file metadata")
	}
	// Unselected releases retain their own earlier synchronization time.
	nextWork, err := m.Prepare(t.Context(), source, 2)
	if err != nil {
		t.Fatal(err)
	}
	nextSync := time.Now().UTC().Truncate(time.Second)
	if err := os.WriteFile(filepath.Join(nextWork, "v2", ".mirrordock-release-sync"), []byte(nextSync.Format(time.RFC3339Nano)), 0600); err != nil {
		t.Fatal(err)
	}
	nextPub, err := m.Publish(t.Context(), source, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !nextPub.Current.Summary.Releases["v1"].SyncedAt.Equal(synced) || !nextPub.Current.Summary.Releases["v2"].SyncedAt.Equal(nextSync) {
		t.Fatal(nextPub.Current.Summary)
	}
	// A new manager must restore summaries without querying the task store on downloads.
	reopened, err := New(m.Root, m.store, []config.Source{source})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Publications()[0].Current.Summary.Releases["v1"]; got.Bytes != 2048 || !got.SyncedAt.Equal(synced) {
		t.Fatal(got)
	}
}

func TestOrdinaryMirrorProjectListing(t *testing.T) {
	m, source := testManager(t)
	work, err := m.Prepare(t.Context(), source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "file.rpm"), make([]byte, 1024), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish(t.Context(), source, 1); err != nil {
		t.Fatal(err)
	}
	m.ListingInfo = func(context.Context) map[string]SourceListingInfo {
		return map[string]SourceListingInfo{source.ID: {State: "succeeded"}}
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if html := w.Body.String(); !strings.Contains(html, `class="project-table"`) || !strings.Contains(html, "1.0 KiB") || !strings.Contains(html, "同步成功") {
		t.Fatal(html)
	}
}

func TestSummaryIgnoresUnsafeSyncMarkers(t *testing.T) {
	dir := t.TempDir()
	for _, tag := range []string{"fifo", "link"} {
		if err := os.Mkdir(filepath.Join(dir, tag), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, tag, "asset"), []byte("abc"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mkfifo(filepath.Join(dir, "fifo", ".mirrordock-release-sync"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte(time.Now().Format(time.RFC3339Nano)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "link", ".mirrordock-release-sync")); err != nil {
		t.Fatal(err)
	}
	summary, err := summarizeDirectory(t.Context(), dir, "github_release")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Bytes != 6 || !summary.Releases["fifo"].SyncedAt.IsZero() || !summary.Releases["link"].SyncedAt.IsZero() {
		t.Fatal(summary)
	}
}
