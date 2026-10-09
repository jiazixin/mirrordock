package config

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

type TLS struct {
	Cert string `yaml:"cert" json:"cert"`
	Key  string `yaml:"key" json:"key"`
}
type Auth struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"-"`
}
type Log struct {
	Level  string `yaml:"level" json:"level"`
	Path   string `yaml:"path" json:"path"`
	Format string `yaml:"format" json:"format"`
}
type RsyncSettings struct {
	IOTimeout      string  `yaml:"io_timeout" json:"io_timeout"`
	ConnectTimeout string  `yaml:"connect_timeout" json:"connect_timeout"`
	LimitMB        float64 `yaml:"limit_mb" json:"limit_mb"`
}
type GitHubSettings struct {
	ReleaseCount       int    `yaml:"release_count" json:"release_count"`
	IncludePrereleases bool   `yaml:"include_prereleases" json:"include_prereleases"`
	PruneOldReleases   bool   `yaml:"prune_old_releases" json:"prune_old_releases"`
	Token              string `yaml:"token" json:"token"`
	Proxy              string `yaml:"proxy" json:"proxy"`
}

// RetryCount requires a YAML integer; yaml.v3 otherwise truncates decimals.
type RetryCount int

func (c *RetryCount) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return errors.New("retry_count 必须为 0–10 之间的整数")
	}
	var value int
	if err := node.Decode(&value); err != nil {
		return err
	}
	*c = RetryCount(value)
	return nil
}

type Settings struct {
	RetryCount    RetryCount              `yaml:"retry_count" json:"retry_count"`
	TaskTimeout   string                  `yaml:"task_timeout" json:"task_timeout"`
	Rsync         RsyncSettings           `yaml:"rsync" json:"rsync"`
	GitHub        GitHubSettings          `yaml:"github" json:"github"`
	Notifications map[string]Notification `yaml:"notifications,omitempty" json:"-"`
}
type Rsync struct {
	PreserveLinks       *bool `yaml:"preserve_links,omitempty" json:"preserve_links"`
	PreserveTimes       *bool `yaml:"preserve_times,omitempty" json:"preserve_times"`
	PreservePermissions *bool `yaml:"preserve_permissions,omitempty" json:"preserve_permissions"`
	PreserveHardlinks   *bool `yaml:"preserve_hardlinks,omitempty" json:"preserve_hardlinks"`
	Delete              *bool `yaml:"delete,omitempty" json:"delete"`
	UID                 *int  `yaml:"uid,omitempty" json:"uid"`
	GID                 *int  `yaml:"gid,omitempty" json:"gid"`
}
type RepoSync struct {
	RepoID           string   `yaml:"repo_id" json:"repo_id"`
	Releasever       string   `yaml:"releasever" json:"releasever"`
	Basearch         string   `yaml:"basearch" json:"basearch"`
	Architectures    []string `yaml:"architectures" json:"architectures"`
	DownloadMetadata *bool    `yaml:"download_metadata,omitempty" json:"download_metadata"`
	NewestOnly       bool     `yaml:"newest_only" json:"newest_only"`
	Delete           *bool    `yaml:"delete,omitempty" json:"delete"`
	GPGCheck         *bool    `yaml:"gpgcheck,omitempty" json:"gpgcheck"`
	GPGKeys          []string `yaml:"gpg_keys" json:"gpg_keys"`
	ModuleHotfixes   bool     `yaml:"module_hotfixes" json:"module_hotfixes"`
}
type GitHubRelease struct {
	Repository    string   `yaml:"repository" json:"repository"`
	ReleaseCount  int      `yaml:"release_count,omitempty" json:"release_count"`
	AssetPatterns []string `yaml:"asset_patterns" json:"asset_patterns"`
}
type Schedule struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Cron    string `yaml:"cron" json:"cron"`
	Overlap string `yaml:"overlap" json:"overlap"`
}
type Source struct {
	ID            string            `yaml:"id" json:"id"`
	Name          string            `yaml:"name" json:"name"`
	Type          string            `yaml:"type" json:"type"`
	Enabled       bool              `yaml:"enabled" json:"enabled"`
	Dest          string            `yaml:"dest" json:"dest"`
	Exclude       []string          `yaml:"exclude,omitempty" json:"exclude"`
	Rsync         Rsync             `yaml:"rsync,omitempty" json:"rsync"`
	RepoSync      RepoSync          `yaml:"reposync,omitempty" json:"reposync"`
	GitHubRelease GitHubRelease     `yaml:"github_release,omitempty" json:"github_release"`
	Schedule      Schedule          `yaml:"schedule" json:"schedule"`
	Upstreams     map[string]string `yaml:",inline" json:"upstreams"`
}
type Config struct {
	Listen     string   `yaml:"listen" json:"listen"`
	Port       int      `yaml:"port" json:"port"`
	Path       string   `yaml:"path" json:"path"`
	MirrorRoot string   `yaml:"mirror_root" json:"mirror_root"`
	Auth       Auth     `yaml:"auth" json:"auth"`
	Log        Log      `yaml:"log" json:"log"`
	TLS        TLS      `yaml:"tls" json:"tls"`
	Settings   Settings `yaml:"settings" json:"settings"`
	Sources    []Source `yaml:"sources" json:"sources"`
}
type View struct {
	Config          Config `json:"config"`
	Revision        string `json:"revision"`
	TokenConfigured bool   `json:"token_configured"`
	ProxyConfigured bool   `json:"proxy_configured"`
	RestartRequired bool   `json:"restart_required"`
	File            string `json:"file"`
}
type Manager struct {
	mu       sync.RWMutex
	file     string
	cfg      Config
	revision string
	startup  Config
}

var ErrConflict = errors.New("配置已被修改，请重新加载后再保存")
var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)
var srcPattern = regexp.MustCompile(`^src([1-9][0-9]*)$`)
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func DefaultBool(v *bool) bool { return v == nil || *v }

func (s Source) MarshalYAML() (any, error) {
	type sourceFields Source
	var node yaml.Node
	if err := node.Encode(sourceFields(s)); err != nil {
		return nil, err
	}
	fields := []*yaml.Node{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if (key == "rsync" && s.Type != "rsync") || (key == "reposync" && s.Type != "reposync") || (key == "github_release" && s.Type != "github_release") {
			continue
		}
		fields = append(fields, node.Content[i], node.Content[i+1])
	}
	node.Content = fields
	return &node, nil
}
func Defaults() Config {
	return Config{Listen: "0.0.0.0", Port: 8080, Path: "../data", MirrorRoot: "/srv/mirrors", Log: Log{Level: "info", Format: "json"}, Settings: Settings{RetryCount: 3, TaskTimeout: "8h", Rsync: RsyncSettings{IOTimeout: "5m", ConnectTimeout: "30s"}, GitHub: GitHubSettings{ReleaseCount: 5}}}
}
func Clone(c Config) Config {
	b, _ := json.Marshal(c)
	var out Config
	_ = json.Unmarshal(b, &out)
	// Passwords are omitted from JSON, but internal copies retain credentials.
	out.Auth = c.Auth
	out.Settings.Notifications = maps.Clone(c.Settings.Notifications)
	return out
}
func (s Source) Candidates() []string {
	keys := make([]string, 0, len(s.Upstreams))
	for k := range s.Upstreams {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		na, _ := strconv.Atoi(strings.TrimPrefix(a, "src"))
		nb, _ := strconv.Atoi(strings.TrimPrefix(b, "src"))
		return na - nb
	})
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if s.Upstreams[k] != "" {
			out = append(out, s.Upstreams[k])
		}
	}
	return out
}
func Resolve(file, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(filepath.Dir(file), p)
}
func Canonical(p string) (string, error) {
	p = filepath.Clean(p)
	resolved, err := filepath.EvalSymlinks(p)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(p)
	if parent == p {
		return "", err
	}
	r, err := Canonical(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(r, filepath.Base(p)), nil
}
func Within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (c Config) Validate(file string) error {
	if err := ValidateNotifications(c.Settings.Notifications); err != nil {
		return err
	}
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, c.Log.Level) {
		return errors.New("log.level 只支持 debug、info、warn、error")
	}
	if c.Log.Format != "json" && c.Log.Format != "text" {
		return errors.New("log.format 只支持 json、text")
	}
	if strings.TrimSpace(c.Log.Path) != c.Log.Path || strings.IndexFunc(c.Log.Path, unicode.IsControl) >= 0 {
		return errors.New("log.path 不能包含首尾空白或控制字符")
	}
	if strings.TrimSpace(c.Auth.Username) == "" || strings.TrimSpace(c.Auth.Username) != c.Auth.Username || len(c.Auth.Username) > 128 || strings.IndexFunc(c.Auth.Username, unicode.IsControl) >= 0 {
		return errors.New("auth.username 必须非空，最多 128 字节，不能包含首尾空白或控制字符")
	}
	if strings.TrimSpace(c.Auth.Password) == "" || len(c.Auth.Password) > 72 {
		return errors.New("auth.password 必须非空，最多 72 字节")
	}
	if net.ParseIP(c.Listen) == nil && c.Listen != "localhost" {
		return errors.New("listen 必须是 IP 地址或 localhost")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("port 必须在 1–65535 之间")
	}
	for name, v := range map[string]string{"path": c.Path, "mirror_root": c.MirrorRoot} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s 不能为空", name)
		}
	}
	for name, v := range map[string]string{"task_timeout": c.Settings.TaskTimeout, "io_timeout": c.Settings.Rsync.IOTimeout, "connect_timeout": c.Settings.Rsync.ConnectTimeout} {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("%s 必须为正时间长度", name)
		}
	}
	if c.Settings.RetryCount < 0 || c.Settings.RetryCount > 10 {
		return errors.New("retry_count 必须在 0–10 之间")
	}
	if math.IsNaN(c.Settings.Rsync.LimitMB) || math.IsInf(c.Settings.Rsync.LimitMB, 0) || c.Settings.Rsync.LimitMB < 0 {
		return errors.New("limit_mb 必须为非负数")
	}
	if c.Settings.GitHub.ReleaseCount < 1 || c.Settings.GitHub.ReleaseCount > 100 {
		return errors.New("release_count 必须在 1–100 之间")
	}
	if strings.IndexFunc(c.Settings.GitHub.Token, unicode.IsSpace) >= 0 || strings.IndexFunc(c.Settings.GitHub.Token, unicode.IsControl) >= 0 {
		return errors.New("github.token 不能包含空白或控制字符，请只填写 Token 本身")
	}
	if _, err := ParseProxyURL(c.Settings.GitHub.Proxy); err != nil {
		return err
	}
	root, err := Canonical(Resolve(file, c.MirrorRoot))
	if err != nil {
		return err
	}
	data, err := Canonical(Resolve(file, c.Path))
	if err != nil {
		return err
	}
	cfgFile, err := Canonical(file)
	if err != nil {
		return err
	}
	if root == string(filepath.Separator) || Within(root, data) || Within(data, root) || Within(root, cfgFile) {
		return errors.New("镜像根必须与数据库、日志和配置目录隔离，且不能是文件系统根")
	}
	if c.Log.Path != "" {
		logFile, err := Canonical(Resolve(file, c.Log.Path))
		if err != nil {
			return err
		}
		if Within(root, logFile) {
			return errors.New("log.path 不能位于镜像根中")
		}
		taskLogs, err := Canonical(filepath.Join(data, "logs"))
		if err != nil {
			return err
		}
		if Within(taskLogs, logFile) && filepath.Dir(logFile) != taskLogs {
			return errors.New("log.path 不能使用任务日志子目录；可使用 <path>/logs/mirrordock.log")
		}
		logInfo, err := os.Stat(logFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if logInfo != nil && !logInfo.Mode().IsRegular() {
			return errors.New("log.path 必须是普通文件")
		}
		protected := []string{cfgFile, filepath.Join(data, "data.sqlite"), filepath.Join(data, "data.sqlite-wal"), filepath.Join(data, "data.sqlite-shm"), filepath.Join(data, ".mirrordock.lock"), filepath.Join(data, "notification.key")}
		for _, p := range []string{c.TLS.Cert, c.TLS.Key} {
			if p != "" {
				protected = append(protected, Resolve(file, p))
			}
		}
		for _, p := range protected {
			p, err := Canonical(p)
			if err != nil {
				return err
			}
			protectedInfo, statErr := os.Stat(p)
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
			if logFile == p || (logInfo != nil && protectedInfo != nil && os.SameFile(logInfo, protectedInfo)) {
				return errors.New("log.path 不能覆盖配置、数据库、实例锁或 TLS 文件")
			}
		}
	}
	if (c.TLS.Cert == "") != (c.TLS.Key == "") {
		return errors.New("TLS 证书与私钥必须同时填写")
	}
	if c.TLS.Cert != "" {
		for _, p := range []string{c.TLS.Cert, c.TLS.Key} {
			p, err := Canonical(Resolve(file, p))
			if err != nil {
				return err
			}
			if Within(root, p) {
				return errors.New("TLS 文件不能位于镜像根中")
			}
		}
		if _, err := tls.LoadX509KeyPair(Resolve(file, c.TLS.Cert), Resolve(file, c.TLS.Key)); err != nil {
			return fmt.Errorf("TLS 加载失败: %w", err)
		}
	}
	ids := map[string]bool{}
	dests := []string{}
	resolvedDests := []string{}
	for _, s := range c.Sources {
		if !idPattern.MatchString(s.ID) || ids[s.ID] {
			return fmt.Errorf("源 id 无效或重复: %s", s.ID)
		}
		ids[s.ID] = true
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("%s 缺少 name", s.ID)
		}
		if s.Dest == "" || path.Clean(s.Dest) != s.Dest || strings.HasPrefix(s.Dest, "/") || strings.ContainsAny(s.Dest, "\\\x00\r\n") {
			return fmt.Errorf("%s 的 dest 必须是规范相对目录", s.ID)
		}
		for seg := range strings.SplitSeq(s.Dest, "/") {
			if seg == ".." || seg == "." || strings.HasPrefix(seg, ".") || seg == "admin" || seg == "api" || seg == "favicon.ico" {
				return fmt.Errorf("%s 的 dest 包含保留路径", s.ID)
			}
		}
		for _, d := range dests {
			if d == s.Dest || strings.HasPrefix(d, s.Dest+"/") || strings.HasPrefix(s.Dest, d+"/") {
				return errors.New("dest 目录不能重复或互相嵌套")
			}
		}
		dests = append(dests, s.Dest)
		target, err := Canonical(filepath.Join(root, filepath.FromSlash(s.Dest)))
		if err != nil {
			return err
		}
		if !Within(root, target) {
			return fmt.Errorf("%s 的目标链接越界", s.ID)
		}
		for _, existing := range resolvedDests {
			if Within(existing, target) || Within(target, existing) {
				return errors.New("dest 解析符号链接后不能重复或互相嵌套")
			}
		}
		resolvedDests = append(resolvedDests, target)
		for k, v := range s.Upstreams {
			if !srcPattern.MatchString(k) {
				return fmt.Errorf("%s: 不支持字段 %s，仅接受 src1/src2…", s.ID, k)
			}
			if _, err := strconv.Atoi(strings.TrimPrefix(k, "src")); err != nil {
				return fmt.Errorf("%s 的 src 编号超出整数范围", s.ID)
			}
			if strings.ContainsAny(v, "\x00\r\n") {
				return errors.New("来源地址包含控制字符")
			}
			if err := publicURL(v); err != nil {
				return err
			}
		}
		for _, v := range s.RepoSync.GPGKeys {
			if err := SourceURL(v); err != nil {
				return err
			}
		}
		switch s.Type {
		case "rsync":
			if len(s.Candidates()) == 0 {
				return fmt.Errorf("%s 缺少 src1", s.ID)
			}
			for _, v := range s.Candidates() {
				u, err := url.Parse(v)
				if err != nil || u.Scheme != "rsync" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
					return fmt.Errorf("%s 的来源必须是 rsync:// 地址", s.ID)
				}
			}
			for _, n := range []*int{s.Rsync.UID, s.Rsync.GID} {
				if n != nil && (*n < 0 || int64(*n) >= math.MaxUint32) {
					return errors.New("uid/gid 必须在 0–4294967294 之间")
				}
			}
			for _, p := range s.Exclude {
				if strings.ContainsAny(p, "\x00\r\n") {
					return errors.New("exclude 包含控制字符")
				}
			}
		case "reposync":
			if len(s.Candidates()) != 1 {
				return errors.New("reposync 当前只接受一个 src1")
			}
			if err := SourceURL(s.Upstreams["src1"]); err != nil {
				return err
			}
			if !idPattern.MatchString(s.RepoSync.RepoID) || !idPattern.MatchString(s.RepoSync.Releasever) || !idPattern.MatchString(s.RepoSync.Basearch) {
				return errors.New("reposync 缺少有效 repo_id/releasever/basearch")
			}
			if s.RepoSync.NewestOnly && DefaultBool(s.RepoSync.DownloadMetadata) {
				return errors.New("完整 metadata 不能搭配 newest_only")
			}
			if DefaultBool(s.RepoSync.GPGCheck) && len(s.RepoSync.GPGKeys) == 0 {
				return errors.New("GPG 检查需要 gpg_keys")
			}
			for _, v := range s.RepoSync.GPGKeys {
				if err := SourceURL(v); err != nil {
					return err
				}
			}
			for _, a := range s.RepoSync.Architectures {
				if !idPattern.MatchString(a) {
					return errors.New("无效 architecture")
				}
			}
		case "github_release":
			if !repoPattern.MatchString(s.GitHubRelease.Repository) || s.GitHubRelease.ReleaseCount < 0 || s.GitHubRelease.ReleaseCount > 100 {
				return errors.New("GitHub 仓库或 Release 数量无效")
			}
			for _, p := range s.GitHubRelease.AssetPatterns {
				if _, err := path.Match(p, "test"); err != nil {
					return fmt.Errorf("无效 asset glob: %w", err)
				}
			}
		default:
			return fmt.Errorf("不支持源类型: %s", s.Type)
		}
		if s.Schedule.Overlap != "" && s.Schedule.Overlap != "skip" {
			return errors.New("schedule.overlap 只支持 skip")
		}
		if s.Schedule.Enabled {
			if len(strings.Fields(s.Schedule.Cron)) != 5 {
				return fmt.Errorf("%s cron 必须为五字段并使用程序启动时区", s.ID)
			}
			if _, err := cron.ParseStandard(s.Schedule.Cron); err != nil {
				return fmt.Errorf("%s cron: %w", s.ID, err)
			}
		}
	}
	return nil
}
func HTTPURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || strings.ContainsAny(v, "\x00\r\n") {
		return errors.New("需要合法的 HTTP/HTTPS 地址")
	}
	return nil
}

// Configured source addresses are public facts, unlike transient signed asset
// URLs received from GitHub. No credential-bearing URL component is accepted.
func SourceURL(v string) error {
	if err := HTTPURL(v); err != nil {
		return err
	}
	return publicURL(v)
}

func publicURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("来源及 GPG 公钥地址不能包含用户信息、查询参数或片段；不支持 URL 鉴权")
	}
	return nil
}

// ParseProxyURL never includes the input in errors: proxy URLs may contain credentials.
func ParseProxyURL(v string) (*url.URL, error) {
	if v == "" {
		return nil, nil
	}
	u, err := url.Parse(v)
	if err != nil || len(v) > 4096 || strings.TrimSpace(v) != v || strings.ContainsAny(v, "\x00\r\n\t") || u.Hostname() == "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("github.proxy 必须为合法的代理地址，不能包含路径、查询或片段")
	}
	if !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, u.Scheme) {
		return nil, errors.New("github.proxy 只支持 http、https、socks5、socks5h")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("github.proxy 端口必须在 1–65535 之间")
		}
	}
	if u.User != nil {
		password, _ := u.User.Password()
		if strings.ContainsAny(u.User.Username()+password, "\x00\r\n") || ((u.Scheme == "socks5" || u.Scheme == "socks5h") && (len(u.User.Username()) > 255 || len(password) > 255)) {
			return nil, errors.New("github.proxy 的认证信息无效")
		}
	}
	return u, nil
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func Parse(b []byte, file string) (Config, error) {
	c := Defaults()
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return c, errors.New("仅支持一个有效 YAML 文档")
	}
	if c.Sources == nil {
		c.Sources = []Source{}
	}
	for i := range c.Sources {
		if c.Sources[i].Upstreams == nil {
			c.Sources[i].Upstreams = map[string]string{}
		}
	}
	return c, c.Validate(file)
}
func Open(file string) (*Manager, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	c, err := Parse(b, abs)
	if err != nil {
		return nil, err
	}
	return &Manager{file: abs, cfg: c, startup: Clone(c), revision: digest(b)}, nil
}
func (m *Manager) File() string    { return m.file }
func (m *Manager) Current() Config { m.mu.RLock(); defer m.mu.RUnlock(); return Clone(m.cfg) }
func (m *Manager) Startup() Config { return Clone(m.startup) }
func (m *Manager) View() View {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := Clone(m.cfg)
	configured := c.Settings.GitHub.Token != ""
	proxyConfigured := c.Settings.GitHub.Proxy != ""
	c.Settings.GitHub.Token = ""
	restart := c.Listen != m.startup.Listen || c.Port != m.startup.Port || c.Path != m.startup.Path || c.MirrorRoot != m.startup.MirrorRoot || c.TLS != m.startup.TLS || c.Auth != m.startup.Auth || c.Log != m.startup.Log
	c.Auth.Password = ""
	return View{Config: c, Revision: m.revision, TokenConfigured: configured, ProxyConfigured: proxyConfigured, RestartRequired: restart, File: m.file}
}
func (m *Manager) Reload() error {
	return m.ReloadChecked(func(Config) error { return nil })
}

func (m *Manager) ReloadChecked(check func(Config) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := os.ReadFile(m.file)
	if err != nil {
		return err
	}
	c, err := Parse(b, m.file)
	if err != nil {
		return err
	}
	if err := check(c); err != nil {
		return err
	}
	m.cfg = c
	m.revision = digest(b)
	return nil
}
func (m *Manager) Save(c Config, revision string, tokenUpdate, proxyUpdate *string) error {
	return m.save(c, revision, tokenUpdate, proxyUpdate, nil)
}
func (m *Manager) SaveNotifications(channels map[string]Notification, revision string) error {
	return m.save(Config{}, revision, nil, nil, channels)
}
func (m *Manager) save(c Config, revision string, tokenUpdate, proxyUpdate *string, channels map[string]Notification) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if channels != nil {
		c = Clone(m.cfg)
	}
	c.Settings.Notifications = maps.Clone(m.cfg.Settings.Notifications)
	if channels != nil {
		c.Settings.Notifications = maps.Clone(channels)
	}
	if c.Sources == nil {
		c.Sources = []Source{}
	}
	for i := range c.Sources {
		if c.Sources[i].Upstreams == nil {
			c.Sources[i].Upstreams = map[string]string{}
		}
	}
	old, err := os.ReadFile(m.file)
	if err != nil {
		return err
	}
	if revision != m.revision || digest(old) != m.revision {
		return ErrConflict
	}
	// Fixed deployment settings are edited in YAML only, never through a browser save.
	c.Listen = m.cfg.Listen
	c.Port = m.cfg.Port
	c.Path = m.cfg.Path
	c.MirrorRoot = m.cfg.MirrorRoot
	c.TLS = m.cfg.TLS
	c.Auth = m.cfg.Auth
	c.Log = m.cfg.Log
	c.Settings.GitHub.Token = m.cfg.Settings.GitHub.Token
	if tokenUpdate != nil {
		c.Settings.GitHub.Token = *tokenUpdate
	}
	c.Settings.GitHub.Proxy = m.cfg.Settings.GitHub.Proxy
	if proxyUpdate != nil {
		c.Settings.GitHub.Proxy = *proxyUpdate
	}
	if err := c.Validate(m.file); err != nil {
		return err
	}
	var before, after yaml.Node
	if err := yaml.Unmarshal(old, &before); err != nil {
		return err
	}
	fresh, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(fresh, &after); err != nil {
		return err
	}
	preserveComments(&before, &after)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&after); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	b := out.Bytes()
	if err := AtomicWrite(m.file, b, 0600); err != nil {
		return err
	}
	m.cfg = Clone(c)
	m.revision = digest(b)
	return nil
}
func AtomicWrite(file string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(file), ".mirrordock-config-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(name, file); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(file))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func preserveComments(old, fresh *yaml.Node) {
	fresh.HeadComment = old.HeadComment
	fresh.LineComment = old.LineComment
	fresh.FootComment = old.FootComment
	if old.Kind == yaml.DocumentNode && fresh.Kind == yaml.DocumentNode {
		preserveComments(old.Content[0], fresh.Content[0])
		return
	}
	if old.Kind == yaml.MappingNode && fresh.Kind == yaml.MappingNode {
		for i := 0; i < len(fresh.Content); i += 2 {
			for j := 0; j < len(old.Content); j += 2 {
				if fresh.Content[i].Value == old.Content[j].Value {
					preserveComments(old.Content[j], fresh.Content[i])
					preserveComments(old.Content[j+1], fresh.Content[i+1])
					break
				}
			}
		}
	}
	if old.Kind == yaml.SequenceNode && fresh.Kind == yaml.SequenceNode {
		for i, n := range fresh.Content {
			key := nodeID(n)
			for j, o := range old.Content {
				if (key != "" && key == nodeID(o)) || (key == "" && i == j) {
					preserveComments(o, n)
					break
				}
			}
		}
	}
}
func nodeID(n *yaml.Node) string {
	if n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			if n.Content[i].Value == "id" {
				return n.Content[i+1].Value
			}
		}
	}
	return ""
}
