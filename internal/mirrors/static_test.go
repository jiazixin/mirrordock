package mirrors

import (
	"mirrordock/internal/config"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func serve(m *Manager, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w
}

func write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func publishFixture(t *testing.T, m *Manager, s config.Source, id int64) {
	t.Helper()
	work, err := m.Prepare(t.Context(), s, id)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(work, "pkg.rpm"), "package")
	if _, err := m.Publish(t.Context(), s, id); err != nil {
		t.Fatal(err)
	}
}

func listed(body, name string) bool { return strings.Contains(body, `data-name="`+name+`"`) }

func TestMirrorRootContentIsPublic(t *testing.T) {
	m, s := testManager(t)
	publishFixture(t, m, s, 1)
	write(t, filepath.Join(m.Root, "tools", "v1", "tool.tar.gz"), "tool v1")
	write(t, filepath.Join(m.Root, "readme.txt"), "hello")
	write(t, filepath.Join(m.Root, ".well-known", "note"), "dotfile")
	write(t, filepath.Join(m.Root, "tools", "upload.part"), "staging")
	write(t, filepath.Join(m.Root, "admin", "index.html"), "shadowed by the service")

	home := serve(m, "GET", "/")
	body := home.Body.String()
	if home.Code != 200 || !strings.Contains(body, "project-table") {
		t.Fatal("home page lost its project listing", home.Code)
	}
	for _, name := range []string{"repo", "tools", "readme.txt", ".well-known"} {
		if !listed(body, name) {
			t.Errorf("home page does not list %s", name)
		}
	}
	for _, name := range []string{".mirrordock", "admin"} {
		if listed(body, name) {
			t.Errorf("home page lists private %s", name)
		}
	}
	if strings.Count(body, `data-name="repo"`) != 1 {
		t.Fatal("a published dest is listed twice")
	}

	for target, want := range map[string]string{
		"/readme.txt":           "hello",
		"/tools/v1/tool.tar.gz": "tool v1",
		"/.well-known/note":     "dotfile",
		"/repo/pkg.rpm":         "package",
	} {
		if w := serve(m, "GET", target); w.Code != 200 || w.Body.String() != want {
			t.Errorf("%s: %d %q", target, w.Code, w.Body.String())
		}
	}
	tools := serve(m, "GET", "/tools/")
	if !listed(tools.Body.String(), "v1") || listed(tools.Body.String(), "upload.part") {
		t.Fatal("static directory listing is wrong")
	}
	for _, target := range []string{"/tools/upload.part", "/missing", "/.mirrordock/published/"} {
		if w := serve(m, "GET", target); w.Code != 404 {
			t.Errorf("%s: %d", target, w.Code)
		}
	}
	if w := serve(m, "GET", "/tools"); w.Code != 301 || w.Header().Get("Location") != "/tools/" {
		t.Fatal("static directory without slash was not redirected", w.Code)
	}
	if w := serve(m, "HEAD", "/readme.txt"); w.Code != 200 || w.Header().Get("Content-Length") != "5" {
		t.Fatal("static HEAD", w.Code, w.Header())
	}
}

func TestMirrorRootContentIsLive(t *testing.T) {
	m, _ := testManager(t)
	file := filepath.Join(m.Root, "notes.txt")
	write(t, file, "first")
	first := serve(m, "GET", "/notes.txt")
	if first.Code != 200 || first.Body.String() != "first" {
		t.Fatal(first.Code)
	}
	// Replacing by rename is the supported way to change live content.
	write(t, file+".new", "second version")
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(file+".new", later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file+".new", file); err != nil {
		t.Fatal(err)
	}
	second := serve(m, "GET", "/notes.txt")
	if second.Body.String() != "second version" || second.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Fatal("rewritten static file kept stale content or ETag")
	}
	write(t, filepath.Join(m.Root, "added.txt"), "new")
	if !listed(serve(m, "GET", "/").Body.String(), "added.txt") {
		t.Fatal("new file not listed without reload")
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/notes.txt", nil)
	req.Header.Set("Range", "bytes=0-5")
	m.ServeHTTP(w, req)
	if w.Code != 206 || w.Body.String() != "second" {
		t.Fatal("static range", w.Code, w.Body.String())
	}
}

func TestMirrorRootSymlinksStayOutsideMirrorDock(t *testing.T) {
	m, s := testManager(t)
	publishFixture(t, m, s, 1)
	write(t, filepath.Join(m.Root, "tools", "v2", "tool"), "v2")
	outside := filepath.Join(t.TempDir(), "secret")
	write(t, outside, "secret")
	links := map[string]string{
		"latest":     "tools/v2",
		"tool":       "tools/v2/tool",
		"repo-link":  "repo",
		"private":    ".mirrordock/published",
		"absolute":   filepath.Join(m.Root, "tools"),
		"escape":     "../" + filepath.Base(filepath.Dir(outside)),
		"loop":       "loop",
		"tools/up":   "../tools",
		"tools/repo": "../repo/pkg.rpm",
	}
	for link, target := range links {
		if err := os.Symlink(target, filepath.Join(m.Root, link)); err != nil {
			t.Fatal(err)
		}
	}
	for target, want := range map[string]string{"/latest/tool": "v2", "/tool": "v2", "/tools/up/v2/tool": "v2"} {
		if w := serve(m, "GET", target); w.Code != 200 || w.Body.String() != want {
			t.Errorf("%s: %d", target, w.Code)
		}
	}
	for _, target := range []string{"/repo-link/pkg.rpm", "/private/", "/absolute/", "/escape/secret", "/loop", "/tools/repo"} {
		if w := serve(m, "GET", target); w.Code != 404 {
			t.Errorf("%s: %d", target, w.Code)
		}
	}
	body := serve(m, "GET", "/").Body.String()
	if !listed(body, "latest") || !strings.Contains(body, `href="/latest/"`) || !listed(body, "tool") {
		t.Fatal("followable links are not listed as their targets")
	}
	for _, name := range []string{"repo-link", "private", "absolute", "escape", "loop"} {
		if listed(body, name) {
			t.Errorf("unservable link %s is listed", name)
		}
	}
}

func TestMirrorRootNeverServesConfiguredDests(t *testing.T) {
	m, s := testManager(t)
	publishFixture(t, m, s, 1)
	// A reposync tree without metadata cannot be imported and stays on disk.
	write(t, filepath.Join(m.Root, "pending", "pkg.rpm"), "unvalidated")
	write(t, filepath.Join(m.Root, "el", "9", "pkg.rpm"), "unvalidated")
	pending := config.Source{ID: "pending", Type: "reposync", Dest: "pending"}
	nested := config.Source{ID: "el9", Type: "reposync", Dest: "el/9"}
	write(t, filepath.Join(m.Root, "el", "README.txt"), "about el")
	if err := m.Reconcile(t.Context(), []config.Source{s, pending, nested}); err == nil {
		t.Fatal("invalid trees were imported")
	}
	for _, target := range []string{"/pending/pkg.rpm", "/el/9/pkg.rpm", "/el/9/"} {
		if w := serve(m, "GET", target); w.Code != 404 {
			t.Errorf("%s: %d", target, w.Code)
		}
	}
	if listed(serve(m, "GET", "/").Body.String(), "pending") {
		t.Fatal("unpublished dest is listed")
	}
	el := serve(m, "GET", "/el/").Body.String()
	if !listed(el, "README.txt") || listed(el, "9") {
		t.Fatal("parent of an unpublished dest lists it or hides its own files")
	}
	// The alias of a removed source points into a generation and stays private.
	m.Restrict([]config.Source{pending, nested})
	if w := serve(m, "GET", "/repo/pkg.rpm"); w.Code != 404 {
		t.Fatal("removed source served through its leftover alias", w.Code)
	}
	if listed(serve(m, "GET", "/").Body.String(), "repo") {
		t.Fatal("removed source alias is listed")
	}
}

func TestMirrorRootMergesFilesBesideNestedDests(t *testing.T) {
	m, _ := testManager(t)
	nested := config.Source{ID: "el9", Name: "EL 9", Type: "rsync", Dest: "el/9"}
	if err := m.Reconcile(t.Context(), []config.Source{nested}); err != nil {
		t.Fatal(err)
	}
	publishFixture(t, m, nested, 2)
	write(t, filepath.Join(m.Root, "el", "README.txt"), "about el")
	if err := syscall.Mkfifo(filepath.Join(m.Root, "el", "fifo"), 0644); err != nil {
		t.Fatal(err)
	}
	el := serve(m, "GET", "/el/").Body.String()
	if !listed(el, "9") || !listed(el, "README.txt") || listed(el, "fifo") || strings.Count(el, `data-name="9"`) != 1 {
		t.Fatal("nested dest parent does not merge projects and files")
	}
	if w := serve(m, "GET", "/el/README.txt"); w.Code != 200 || w.Body.String() != "about el" {
		t.Fatal(w.Code)
	}
	if w := serve(m, "GET", "/el/fifo"); w.Code != 404 {
		t.Fatal("special file served", w.Code)
	}
	if w := serve(m, "GET", "/el/9/pkg.rpm"); w.Code != 200 || w.Body.String() != "package" {
		t.Fatal("nested dest no longer served from its generation", w.Code)
	}
}

func TestMirrorRootListingBoundKeepsProjects(t *testing.T) {
	m, s := testManager(t)
	publishFixture(t, m, s, 1)
	for i := range maxListedEntries + 5 {
		if err := os.Mkdir(filepath.Join(m.Root, "a"+fmtInt(i)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	body := serve(m, "GET", "/").Body.String()
	if !listed(body, "repo") || !strings.Contains(body, "仅列出前 1000 项") {
		t.Fatal("bounded home page dropped the project or the truncation note")
	}
	if n := strings.Count(body, "<tr class=\"is-dir\""); n != maxListedEntries {
		t.Fatal("home page lists", n)
	}
}
