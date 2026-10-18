package config

import (
	"bytes"
	"cmp"
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
	Proxy          string  `yaml:"proxy" json:"proxy"`
}

type GitHubSettings struct {
	ReleaseCount       int    `yaml:"release_count" json:"release_count"`
	IncludePrereleases bool   `yaml:"include_prereleases" json:"include_prereleases"`
	PruneOldReleases   bool   `yaml:"prune_old_releases" json:"prune_old_releases"`
	Token              string `yaml:"token" json:"token"`
	Proxy              string `yaml:"proxy" json:"proxy"`
}

// decodeInteger requires a YAML integer; yaml.v3 otherwise truncates decimals.
func decodeInteger(node *yaml.Node, message string) (int, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return 0, errors.New(message)
	}
	var value int
	err := node.Decode(&value)
	return value, err
}

type RetryCount int

func (c *RetryCount) UnmarshalYAML(node *yaml.Node) error {
	value, err := decodeInteger(node, "retry_count 必须为 0–10 之间的整数")
	*c = RetryCount(value)
	return err
}

// Slots is the number of runs of one source type that may execute at once.
type Slots int

func (s *Slots) UnmarshalYAML(node *yaml.Node) error {
	value, err := decodeInteger(node, "concurrency 必须为 1–16 之间的整数")
	*s = Slots(value)
	return err
}

// Concurrency limits simultaneous runs per source type. Each type has its
// own slots, so a long rsync never blocks a GitHub Release run.
type Concurrency struct {
	Rsync         Slots `yaml:"rsync" json:"rsync"`
	RepoSync      Slots `yaml:"reposync" json:"reposync"`
	GitHubRelease Slots `yaml:"github_release" json:"github_release"`
}

// MaxSlots bounds each type's concurrency.
const MaxSlots = 16

// Limit returns the slots for sourceType.
func (c Concurrency) Limit(sourceType string) int {
	switch sourceType {
	case "rsync":
		return int(c.Rsync)
	case "reposync":
		return int(c.RepoSync)
	case "github_release":
		return int(c.GitHubRelease)
	}
	return 0
}

type Settings struct {
	RetryCount    RetryCount              `yaml:"retry_count" json:"retry_count"`
	TaskTimeout   string                  `yaml:"task_timeout" json:"task_timeout"`
	Concurrency   Concurrency             `yaml:"concurrency" json:"concurrency"`
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
	// ProxyMode is blank or "global" to follow settings.rsync.proxy, "direct"
	// to connect without a proxy, or "custom" to use Proxy.
	ProxyMode string `yaml:"proxy_mode,omitempty" json:"proxy_mode"`
	Proxy     string `yaml:"proxy,omitempty" json:"proxy"`
}

// Rsync proxy modes of a source.
const (
	ProxyGlobal = "global"
	ProxyDirect = "direct"
	ProxyCustom = "custom"
)

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
	// Only the selected executor's option block is written back to YAML.
	executorKeys := []string{"rsync", "reposync", "github_release"}
	fields := []*yaml.Node{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if slices.Contains(executorKeys, key) && key != s.Type {
			continue
		}
		fields = append(fields, node.Content[i], node.Content[i+1])
	}
	node.Content = fields
	return &node, nil
}

func Defaults() Config {
	return Config{
		Listen:     "0.0.0.0",
		Port:       8080,
		Path:       "../data",
		MirrorRoot: "/srv/mirrors",
		Log:        Log{Level: "info", Format: "json"},
		Settings: Settings{
			RetryCount:  3,
			TaskTimeout: "8h",
			Concurrency: Concurrency{Rsync: 4, RepoSync: 4, GitHubRelease: 4},
			Rsync:       RsyncSettings{IOTimeout: "5m", ConnectTimeout: "30s"},
			GitHub:      GitHubSettings{ReleaseCount: 5},
		},
	}
}

func Clone(c Config) Config {
	encoded, _ := json.Marshal(c)
	var out Config
	_ = json.Unmarshal(encoded, &out)
	// Passwords are omitted from JSON, but internal copies retain credentials.
	out.Auth = c.Auth
	out.Settings.Notifications = maps.Clone(c.Settings.Notifications)
	return out
}

func (s Source) Candidates() []string {
	sourceNumber := func(key string) int {
		number, _ := strconv.Atoi(strings.TrimPrefix(key, "src"))
		return number
	}
	keys := slices.SortedFunc(maps.Keys(s.Upstreams), func(a, b string) int {
		return cmp.Compare(sourceNumber(a), sourceNumber(b))
	})
	addresses := make([]string, 0, len(keys))
	for _, key := range keys {
		if s.Upstreams[key] != "" {
			addresses = append(addresses, s.Upstreams[key])
		}
	}
	return addresses
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
	resolvedParent, err := Canonical(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(p)), nil
}

func Within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (c Config) Validate(file string) error {
	if err := ValidateNotifications(c.Settings.Notifications); err != nil {
		return err
	}
	if err := c.validateService(); err != nil {
		return err
	}
	if err := c.Settings.validate(); err != nil {
		return err
	}
	paths, err := c.resolvePaths(file)
	if err != nil {
		return err
	}
	if c.Log.Path != "" {
		if err := c.validateLogPath(file, paths); err != nil {
			return err
		}
	}
	if err := c.validateTLS(file, paths.mirrorRoot); err != nil {
		return err
	}
	return validateSources(c.Sources, paths.mirrorRoot)
}

// validateService checks the logging, account and listener fields.
func (c Config) validateService() error {
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, c.Log.Level) {
		return errors.New("log.level 只支持 debug、info、warn、error")
	}
	if c.Log.Format != "json" && c.Log.Format != "text" {
		return errors.New("log.format 只支持 json、text")
	}
	if !trimmedPrintable(c.Log.Path) {
		return errors.New("log.path 不能包含首尾空白或控制字符")
	}
	username := c.Auth.Username
	if username == "" || len(username) > 128 || !trimmedPrintable(username) {
		return errors.New("auth.username 必须非空，最多 128 字节，不能包含首尾空白或控制字符")
	}
	if strings.TrimSpace(c.Auth.Password) == "" || len(c.Auth.Password) > maxPasswordBytes {
		return errors.New("auth.password 必须非空，最多 72 字节")
	}
	if net.ParseIP(c.Listen) == nil && c.Listen != "localhost" {
		return errors.New("listen 必须是 IP 地址或 localhost")
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("port 必须在 1–65535 之间")
	}
	if strings.TrimSpace(c.Path) == "" {
		return errors.New("path 不能为空")
	}
	if strings.TrimSpace(c.MirrorRoot) == "" {
		return errors.New("mirror_root 不能为空")
	}
	return nil
}

// maxPasswordBytes is the bcrypt input limit.
const maxPasswordBytes = 72

// trimmedPrintable reports whether v has no surrounding whitespace and no
// control characters.
func trimmedPrintable(v string) bool {
	return strings.TrimSpace(v) == v && strings.IndexFunc(v, unicode.IsControl) < 0
}

func (s Settings) validate() error {
	durations := []struct{ name, value string }{
		{"task_timeout", s.TaskTimeout},
		{"io_timeout", s.Rsync.IOTimeout},
		{"connect_timeout", s.Rsync.ConnectTimeout},
	}
	for _, field := range durations {
		duration, err := time.ParseDuration(field.value)
		if err != nil || duration <= 0 {
			return fmt.Errorf("%s 必须为正时间长度", field.name)
		}
	}
	if s.RetryCount < 0 || s.RetryCount > 10 {
		return errors.New("retry_count 必须在 0–10 之间")
	}
	for _, slots := range []Slots{s.Concurrency.Rsync, s.Concurrency.RepoSync, s.Concurrency.GitHubRelease} {
		if slots < 1 || slots > MaxSlots {
			return errors.New("concurrency 每种类型必须在 1–16 之间")
		}
	}
	limit := s.Rsync.LimitMB
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit < 0 {
		return errors.New("limit_mb 必须为非负数")
	}
	if s.GitHub.ReleaseCount < 1 || s.GitHub.ReleaseCount > 100 {
		return errors.New("release_count 必须在 1–100 之间")
	}
	token := s.GitHub.Token
	if strings.IndexFunc(token, unicode.IsSpace) >= 0 || strings.IndexFunc(token, unicode.IsControl) >= 0 {
		return errors.New("github.token 不能包含空白或控制字符，请只填写 Token 本身")
	}
	if _, err := ParseProxyURL(s.GitHub.Proxy); err != nil {
		return err
	}
	_, err := ParseProxyField("rsync.proxy", s.Rsync.Proxy)
	return err
}

// resolvedPaths holds canonical deployment paths after symlink resolution.
type resolvedPaths struct {
	mirrorRoot string
	data       string
	configFile string
}

func (c Config) resolvePaths(file string) (resolvedPaths, error) {
	root, err := Canonical(Resolve(file, c.MirrorRoot))
	if err != nil {
		return resolvedPaths{}, err
	}
	data, err := Canonical(Resolve(file, c.Path))
	if err != nil {
		return resolvedPaths{}, err
	}
	configFile, err := Canonical(file)
	if err != nil {
		return resolvedPaths{}, err
	}
	isolated := !Within(root, data) && !Within(data, root) && !Within(root, configFile)
	if root == string(filepath.Separator) || !isolated {
		return resolvedPaths{}, errors.New(
			"镜像根必须与数据库、日志和配置目录隔离，且不能是文件系统根",
		)
	}
	return resolvedPaths{mirrorRoot: root, data: data, configFile: configFile}, nil
}

// validateLogPath keeps the program log out of the mirror, the task log
// directories and every file the service owns.
func (c Config) validateLogPath(file string, paths resolvedPaths) error {
	logFile, err := Canonical(Resolve(file, c.Log.Path))
	if err != nil {
		return err
	}
	if Within(paths.mirrorRoot, logFile) {
		return errors.New("log.path 不能位于镜像根中")
	}
	taskLogs, err := Canonical(filepath.Join(paths.data, "logs"))
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
	protected := []string{paths.configFile}
	stateFiles := []string{"data.sqlite", "data.sqlite-wal", "data.sqlite-shm", ".mirrordock.lock", "notification.key"}
	for _, name := range stateFiles {
		protected = append(protected, filepath.Join(paths.data, name))
	}
	for _, tlsFile := range []string{c.TLS.Cert, c.TLS.Key} {
		if tlsFile != "" {
			protected = append(protected, Resolve(file, tlsFile))
		}
	}
	for _, candidate := range protected {
		candidate, err := Canonical(candidate)
		if err != nil {
			return err
		}
		protectedInfo, statErr := os.Stat(candidate)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		sameFile := logInfo != nil && protectedInfo != nil && os.SameFile(logInfo, protectedInfo)
		if logFile == candidate || sameFile {
			return errors.New("log.path 不能覆盖配置、数据库、实例锁或 TLS 文件")
		}
	}
	return nil
}

func (c Config) validateTLS(file, mirrorRoot string) error {
	if (c.TLS.Cert == "") != (c.TLS.Key == "") {
		return errors.New("TLS 证书与私钥必须同时填写")
	}
	if c.TLS.Cert == "" {
		return nil
	}
	for _, tlsFile := range []string{c.TLS.Cert, c.TLS.Key} {
		resolved, err := Canonical(Resolve(file, tlsFile))
		if err != nil {
			return err
		}
		if Within(mirrorRoot, resolved) {
			return errors.New("TLS 文件不能位于镜像根中")
		}
	}
	if _, err := tls.LoadX509KeyPair(Resolve(file, c.TLS.Cert), Resolve(file, c.TLS.Key)); err != nil {
		return fmt.Errorf("TLS 加载失败: %w", err)
	}
	return nil
}

// reservedTopLevel lists the first dest segments owned by the service itself.
var reservedTopLevel = []string{"admin", "api", "favicon.ico"}

func validateSources(sources []Source, mirrorRoot string) error {
	ids := map[string]bool{}
	dests := []string{}
	resolvedDests := []string{}
	for _, s := range sources {
		if !idPattern.MatchString(s.ID) || ids[s.ID] {
			return fmt.Errorf("源 id 无效或重复: %s", s.ID)
		}
		ids[s.ID] = true
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("%s 缺少 name", s.ID)
		}
		if err := s.validateDest(); err != nil {
			return err
		}
		for _, existing := range dests {
			nested := strings.HasPrefix(existing, s.Dest+"/") || strings.HasPrefix(s.Dest, existing+"/")
			if existing == s.Dest || nested {
				return errors.New("dest 目录不能重复或互相嵌套")
			}
		}
		dests = append(dests, s.Dest)
		target, err := Canonical(filepath.Join(mirrorRoot, filepath.FromSlash(s.Dest)))
		if err != nil {
			return err
		}
		if !Within(mirrorRoot, target) {
			return fmt.Errorf("%s 的目标链接越界", s.ID)
		}
		for _, existing := range resolvedDests {
			if Within(existing, target) || Within(target, existing) {
				return errors.New("dest 解析符号链接后不能重复或互相嵌套")
			}
		}
		resolvedDests = append(resolvedDests, target)
		if err := s.validateAddresses(); err != nil {
			return err
		}
		if err := s.validateExecutor(); err != nil {
			return err
		}
		if err := s.Schedule.validate(s.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s Source) validateDest() error {
	clean := path.Clean(s.Dest) == s.Dest
	if s.Dest == "" || !clean || strings.HasPrefix(s.Dest, "/") || strings.ContainsAny(s.Dest, "\\\x00\r\n") {
		return fmt.Errorf("%s 的 dest 必须是规范相对目录", s.ID)
	}
	first, _, _ := strings.Cut(s.Dest, "/")
	if slices.Contains(reservedTopLevel, first) {
		return fmt.Errorf("%s 的 dest 包含保留路径", s.ID)
	}
	for segment := range strings.SplitSeq(s.Dest, "/") {
		if segment == ".." || segment == "." || strings.HasPrefix(segment, ".") {
			return fmt.Errorf("%s 的 dest 包含保留路径", s.ID)
		}
	}
	return nil
}

func (s Source) validateAddresses() error {
	for key, address := range s.Upstreams {
		if !srcPattern.MatchString(key) {
			return fmt.Errorf("%s: 不支持字段 %s，仅接受 src1/src2…", s.ID, key)
		}
		if _, err := strconv.Atoi(strings.TrimPrefix(key, "src")); err != nil {
			return fmt.Errorf("%s 的 src 编号超出整数范围", s.ID)
		}
		if strings.ContainsAny(address, "\x00\r\n") {
			return errors.New("来源地址包含控制字符")
		}
		if err := publicURL(address); err != nil {
			return err
		}
	}
	for _, address := range s.RepoSync.GPGKeys {
		if err := SourceURL(address); err != nil {
			return err
		}
	}
	return nil
}

func (s Source) validateExecutor() error {
	switch s.Type {
	case "rsync":
		return s.validateRsync()
	case "reposync":
		return s.validateRepoSync()
	case "github_release":
		return s.validateGitHubRelease()
	default:
		return fmt.Errorf("不支持源类型: %s", s.Type)
	}
}

func (s Source) validateRsync() error {
	candidates := s.Candidates()
	if len(candidates) == 0 {
		return fmt.Errorf("%s 缺少 src1", s.ID)
	}
	for _, candidate := range candidates {
		address, err := url.Parse(candidate)
		plain := err == nil && address.User == nil && address.RawQuery == "" && address.Fragment == ""
		if !plain || address.Scheme != "rsync" || address.Host == "" {
			return fmt.Errorf("%s 的来源必须是 rsync:// 地址", s.ID)
		}
		remote := strings.Trim(address.Path, "/")
		directoryWithoutSlash := strings.Contains(remote, "/") && !strings.HasSuffix(address.Path, "/")
		if remote == "" || directoryWithoutSlash {
			return fmt.Errorf("%s 的 rsync 来源必须包含模块，模块内目录须以 / 结尾", s.ID)
		}
	}
	for _, identity := range []*int{s.Rsync.UID, s.Rsync.GID} {
		if identity != nil && (*identity < 0 || int64(*identity) >= math.MaxUint32) {
			return errors.New("uid/gid 必须在 0–4294967294 之间")
		}
	}
	for _, pattern := range s.Exclude {
		if strings.ContainsAny(pattern, "\x00\r\n") {
			return errors.New("exclude 包含控制字符")
		}
	}
	return s.Rsync.validateProxy(s.ID)
}

func (r Rsync) validateProxy(sourceID string) error {
	switch r.ProxyMode {
	case "", ProxyGlobal, ProxyDirect:
		if r.Proxy != "" {
			return fmt.Errorf("%s 的 rsync.proxy 仅在 proxy_mode 为 custom 时填写", sourceID)
		}
		return nil
	case ProxyCustom:
		if r.Proxy == "" {
			return fmt.Errorf("%s 的 proxy_mode 为 custom 时须填写 rsync.proxy", sourceID)
		}
		_, err := ParseProxyField(sourceID+" 的 rsync.proxy", r.Proxy)
		return err
	default:
		return fmt.Errorf("%s 的 rsync.proxy_mode 只支持 global、direct、custom", sourceID)
	}
}

// RsyncProxy is the proxy URL the source's rsync connects through, read from
// the current settings; blank means direct.
func (s Source) RsyncProxy(settings RsyncSettings) string {
	switch s.Rsync.ProxyMode {
	case ProxyDirect:
		return ""
	case ProxyCustom:
		return s.Rsync.Proxy
	default:
		return settings.Proxy
	}
}

func (s Source) validateRepoSync() error {
	options := s.RepoSync
	if len(s.Candidates()) != 1 {
		return errors.New("reposync 当前只接受一个 src1")
	}
	if err := SourceURL(s.Upstreams["src1"]); err != nil {
		return err
	}
	identifiers := []string{options.RepoID, options.Releasever, options.Basearch}
	if slices.ContainsFunc(identifiers, func(v string) bool { return !idPattern.MatchString(v) }) {
		return errors.New("reposync 缺少有效 repo_id/releasever/basearch")
	}
	if options.NewestOnly && DefaultBool(options.DownloadMetadata) {
		return errors.New("完整 metadata 不能搭配 newest_only")
	}
	if DefaultBool(options.GPGCheck) && len(options.GPGKeys) == 0 {
		return errors.New("GPG 检查需要 gpg_keys")
	}
	for _, architecture := range options.Architectures {
		if !idPattern.MatchString(architecture) {
			return errors.New("无效 architecture")
		}
	}
	return nil
}

func (s Source) validateGitHubRelease() error {
	release := s.GitHubRelease
	if !repoPattern.MatchString(release.Repository) || release.ReleaseCount < 0 || release.ReleaseCount > 100 {
		return errors.New("GitHub 仓库或 Release 数量无效")
	}
	for _, pattern := range release.AssetPatterns {
		if _, err := path.Match(pattern, "test"); err != nil {
			return fmt.Errorf("无效 asset glob: %w", err)
		}
	}
	return nil
}

func (schedule Schedule) validate(sourceID string) error {
	if schedule.Overlap != "" && schedule.Overlap != "skip" {
		return errors.New("schedule.overlap 只支持 skip")
	}
	if !schedule.Enabled {
		return nil
	}
	if len(strings.Fields(schedule.Cron)) != 5 {
		return fmt.Errorf("%s cron 必须为五字段并使用程序启动时区", sourceID)
	}
	if _, err := cron.ParseStandard(schedule.Cron); err != nil {
		return fmt.Errorf("%s cron: %w", sourceID, err)
	}
	return nil
}

func HTTPURL(v string) error {
	address, err := url.Parse(v)
	if err != nil || strings.ContainsAny(v, "\x00\r\n") {
		return errors.New("需要合法的 HTTP/HTTPS 地址")
	}
	httpScheme := address.Scheme == "https" || address.Scheme == "http"
	if !httpScheme || address.Host == "" || address.User != nil {
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
	address, err := url.Parse(v)
	if err != nil || address.User != nil || address.RawQuery != "" || address.ForceQuery || address.Fragment != "" {
		return errors.New("来源及 GPG 公钥地址不能包含用户信息、查询参数或片段；不支持 URL 鉴权")
	}
	return nil
}

// ParseProxyURL parses the GitHub proxy; see ParseProxyField.
func ParseProxyURL(v string) (*url.URL, error) {
	return ParseProxyField("github.proxy", v)
}

// ParseProxyField parses a proxy URL setting named field. Blank means direct.
// Errors never include the input: proxy URLs may contain credentials.
func ParseProxyField(field, v string) (*url.URL, error) {
	if v == "" {
		return nil, nil
	}
	invalid := errors.New(field + " 必须为合法的代理地址，不能包含路径、查询或片段")
	if len(v) > 4096 || strings.TrimSpace(v) != v || strings.ContainsAny(v, "\x00\r\n\t") {
		return nil, invalid
	}
	proxy, err := url.Parse(v)
	if err != nil || proxy.Hostname() == "" || proxy.Opaque != "" {
		return nil, invalid
	}
	if proxy.RawQuery != "" || proxy.ForceQuery || proxy.Fragment != "" || (proxy.Path != "" && proxy.Path != "/") {
		return nil, invalid
	}
	if !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, proxy.Scheme) {
		return nil, errors.New(field + " 只支持 http、https、socks5、socks5h")
	}
	if port := proxy.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, errors.New(field + " 端口必须在 1–65535 之间")
		}
	}
	if proxy.User != nil {
		username := proxy.User.Username()
		password, _ := proxy.User.Password()
		socks := proxy.Scheme == "socks5" || proxy.Scheme == "socks5h"
		// SOCKS5 username/password authentication encodes each field in one byte.
		socksTooLong := socks && (len(username) > 255 || len(password) > 255)
		if strings.ContainsAny(username+password, "\x00\r\n") || socksTooLong {
			return nil, errors.New(field + " 的认证信息无效")
		}
	}
	return proxy, nil
}

// digest is the revision of a YAML file: the SHA-256 of its exact bytes.
func digest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func Parse(contents []byte, file string) (Config, error) {
	c := Defaults()
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
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
	contents, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	c, err := Parse(contents, abs)
	if err != nil {
		return nil, err
	}
	return &Manager{file: abs, cfg: c, startup: Clone(c), revision: digest(contents)}, nil
}

func (m *Manager) File() string { return m.file }

func (m *Manager) Current() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Clone(m.cfg)
}

func (m *Manager) Startup() Config { return Clone(m.startup) }

// restartRequired reports whether a deployment field differs from startup.
// These fields only take effect after the process restarts.
func restartRequired(current, startup Config) bool {
	return current.Listen != startup.Listen ||
		current.Port != startup.Port ||
		current.Path != startup.Path ||
		current.MirrorRoot != startup.MirrorRoot ||
		current.TLS != startup.TLS ||
		current.Auth != startup.Auth ||
		current.Log != startup.Log
}

// View is the configuration as returned by the read API. The administrator
// password and GitHub token are removed; the proxy URL is intentionally kept.
func (m *Manager) View() View {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := Clone(m.cfg)
	view := View{
		Revision:        m.revision,
		TokenConfigured: c.Settings.GitHub.Token != "",
		ProxyConfigured: c.Settings.GitHub.Proxy != "",
		RestartRequired: restartRequired(c, m.startup),
		File:            m.file,
	}
	c.Settings.GitHub.Token = ""
	c.Auth.Password = ""
	view.Config = c
	return view
}

func (m *Manager) Reload() error {
	return m.ReloadChecked(func(Config) error { return nil })
}

func (m *Manager) ReloadChecked(check func(Config) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	contents, err := os.ReadFile(m.file)
	if err != nil {
		return err
	}
	c, err := Parse(contents, m.file)
	if err != nil {
		return err
	}
	if err := check(c); err != nil {
		return err
	}
	m.cfg = c
	m.revision = digest(contents)
	return nil
}

func (m *Manager) Save(c Config, revision string, tokenUpdate, proxyUpdate *string) error {
	return m.save(c, revision, tokenUpdate, proxyUpdate, nil)
}

func (m *Manager) SaveNotifications(channels map[string]Notification, revision string) error {
	return m.save(Config{}, revision, nil, nil, channels)
}

func (m *Manager) save(
	c Config,
	revision string,
	tokenUpdate, proxyUpdate *string,
	channels map[string]Notification,
) error {
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
	written := out.Bytes()
	if err := AtomicWrite(m.file, written, 0600); err != nil {
		return err
	}
	m.cfg = Clone(c)
	m.revision = digest(written)
	return nil
}

// AtomicWrite replaces file with contents through a synced temporary file in
// the same directory, then syncs the directory entry.
func AtomicWrite(file string, contents []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(file), ".mirrordock-config-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(contents)
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
		// Sources are matched by id so comments follow a moved entry.
		for i, freshItem := range fresh.Content {
			key := nodeID(freshItem)
			for j, oldItem := range old.Content {
				if (key != "" && key == nodeID(oldItem)) || (key == "" && i == j) {
					preserveComments(oldItem, freshItem)
					break
				}
			}
		}
	}
}

func nodeID(node *yaml.Node) string {
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == "id" {
				return node.Content[i+1].Value
			}
		}
	}
	return ""
}
