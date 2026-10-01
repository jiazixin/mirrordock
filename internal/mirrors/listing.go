package mirrors

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"
)

// These resources are owned by the public directory renderer. Only repository
// assets, never file names or request values, receive trusted template types.
//
//go:embed listing.html
var listingHTML string

//go:embed listing.css
var listingCSS string

//go:embed listing.js
var listingJS string

//go:embed listing-icons.html
var listingIconsHTML string

var listing = template.Must(template.New("listing.html").Funcs(template.FuncMap{
	"styles":   func() template.CSS { return template.CSS(listingCSS) },
	"script":   func() template.JS { return template.JS(listingJS) },
	"minusOne": func(n int) int { return n - 1 },
}).Parse(listingHTML + listingIconsHTML))

var listingCSP = func() string {
	sum := sha256.Sum256([]byte(listingJS))
	return "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'"
}()

type listingItem struct {
	Name, URL, Kind, Group, Icon string
	Size, Modified, Timestamp    string
	Bytes                        int64
	Dir                          bool
}

func newListingItem(name, u string, dir bool, info os.FileInfo) listingItem {
	kind, group, icon := fileKind(name)
	item := listingItem{Name: name, URL: u, Kind: kind, Group: group, Icon: icon, Size: "—", Modified: "—", Timestamp: "", Bytes: -1, Dir: dir}
	if dir {
		item.Kind, item.Group, item.Icon = "目录", "folder", "folder"
	}
	if info == nil {
		return item
	}
	if info.Mode()&os.ModeSymlink != 0 {
		item.Kind, item.Group, item.Icon = "符号链接", "other", "link"
	}
	if info.Mode().IsRegular() {
		item.Bytes, item.Size = info.Size(), fileSize(info.Size())
	}
	modified := info.ModTime().In(time.Local)
	item.Modified = modified.Format("2006-01-02 15:04")
	item.Timestamp = modified.Format(time.RFC3339)
	return item
}

func fileKind(name string) (kind, group, icon string) {
	lower := strings.ToLower(name)
	for _, ext := range []string{"tar.gz", "tar.xz", "tar.bz2", "tar.zst", "tar.lz", "tar.lzma", "tgz", "txz", "tbz2", "zip", "7z", "rar", "tar", "gz", "xz", "bz2", "zst", "lz", "lzma"} {
		if strings.HasSuffix(lower, "."+ext) {
			return strings.ToUpper(ext), "archive", "archive"
		}
	}
	ext := strings.TrimPrefix(path.Ext(lower), ".")
	switch ext {
	case "rpm", "deb", "udeb", "apk", "whl", "exe", "msi", "dmg", "appimage":
		return strings.ToUpper(ext), "package", "package"
	case "json", "xml", "yaml", "yml", "toml", "repo", "list":
		return strings.ToUpper(ext), "metadata", "metadata"
	case "asc", "sig", "gpg":
		return "签名 · " + strings.ToUpper(ext), "metadata", "signature"
	case "sha256", "sha512", "sha1", "md5", "checksum":
		return "校验和", "metadata", "checksum"
	case "iso", "img", "qcow2", "vhd", "vmdk":
		return strings.ToUpper(ext), "other", "disk"
	case "md":
		return "MD", "other", "markdown"
	case "py":
		return "PY", "other", "python"
	case "ini":
		return "INI", "other", "ini"
	case "txt", "log":
		return strings.ToUpper(ext), "other", "text"
	case "sh", "conf", "cfg":
		return strings.ToUpper(ext), "other", "code"
	}
	if strings.HasPrefix(lower, "rpm-gpg-key") {
		return "签名密钥", "metadata", "signature"
	}
	if strings.Contains(lower, "sums") || lower == "checksums" {
		return "校验和", "metadata", "checksum"
	}
	switch lower {
	case "release", "inrelease", "packages", "sources", "contents", ".treeinfo", ".discinfo":
		return "仓库元数据", "metadata", "metadata"
	case "readme", "license", "copying":
		return "文本", "other", "text"
	}
	return "文件", "other", "file"
}

func fileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"} {
		value /= 1024
		if value < 1024 || unit == "EiB" {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return "—"
}

type listingCrumb struct{ Name, URL string }

// Directory metadata and template work have their own bounded pool, so listing
// bursts cannot consume the slots reserved for package or repository downloads.
func (m *Manager) acquireListing(w http.ResponseWriter) bool {
	select {
	case m.listingGates <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "3")
		http.Error(w, "directory capacity reached", http.StatusServiceUnavailable)
		return false
	}
}

func renderListing(w http.ResponseWriter, r *http.Request, items []listingItem, more bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", listingCSP)
	if r.Method == "HEAD" {
		return
	}
	slices.SortFunc(items, func(a, b listingItem) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(items) > 1000 {
		items, more = items[:1000], true
	}
	crumbs := []listingCrumb{{Name: "全部镜像", URL: "/"}}
	u := "/"
	for segment := range strings.SplitSeq(strings.Trim(r.URL.Path, "/"), "/") {
		if segment == "" {
			continue
		}
		u += url.PathEscape(segment) + "/"
		crumbs = append(crumbs, listingCrumb{Name: segment, URL: u})
	}
	parent := ""
	if len(crumbs) > 1 {
		parent = crumbs[len(crumbs)-2].URL
	}
	_ = listing.Execute(w, struct {
		Title, Parent string
		Crumbs        []listingCrumb
		Items         []listingItem
		More          bool
	}{crumbs[len(crumbs)-1].Name, parent, crumbs, items, more})
}
