// Package config loads, validates and persists the runtime-editable MirrorDock
// configuration.
package config

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	"go.yaml.in/yaml/v3"
)

// TLS names the certificate and key files used to serve HTTPS.
type TLS struct {
	Cert string `yaml:"cert" json:"cert"`
	Key  string `yaml:"key" json:"key"`
}

// Auth holds the administrator credentials; the password is never serialized to
// JSON.
type Auth struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"-"`
}

// Log configures the program log level and optional log file.
type Log struct {
	Level  string `yaml:"level" json:"level"`
	Path   string `yaml:"path" json:"path"`
	Format string `yaml:"format" json:"format"`
}

// RsyncSettings holds the global rsync defaults.
type RsyncSettings struct {
	IOTimeout      string  `yaml:"io_timeout" json:"io_timeout"`
	ConnectTimeout string  `yaml:"connect_timeout" json:"connect_timeout"`
	LimitMB        float64 `yaml:"limit_mb" json:"limit_mb"`
	Proxy          string  `yaml:"proxy" json:"proxy"`
}

// GitHubSettings holds the global GitHub release sync defaults.
type GitHubSettings struct {
	ReleaseCount       int    `yaml:"release_count" json:"release_count"`
	IncludePrereleases bool   `yaml:"include_prereleases" json:"include_prereleases"`
	PruneOldReleases   bool   `yaml:"prune_old_releases" json:"prune_old_releases"`
	Token              string `yaml:"token" json:"token"`
	Proxy              string `yaml:"proxy" json:"proxy"`
}

// decodeInteger requires a YAML integer; yaml.v3 otherwise truncates decimals.
func decodeInteger(node *yaml.Node, invalid error) (int, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return 0, invalid
	}
	var value int
	err := node.Decode(&value)
	return value, err
}

// RetryCount is the number of automatic retries of a failed run, from 0 to 10.
type RetryCount int

// UnmarshalYAML decodes an integer retry count and rejects values out of range.
func (c *RetryCount) UnmarshalYAML(node *yaml.Node) error {
	value, err := decodeInteger(node, fieldError("settings.retry_count", "retry_count_range", "must be an integer between 0 and 10"))
	*c = RetryCount(value)
	return err
}

// RetentionDays is how many days finished runs, their logs and delivered
// notification records are kept; 0 keeps them forever.
type RetentionDays int

// MaxRetentionDays bounds RetentionDays.
const MaxRetentionDays = 3650

// UnmarshalYAML decodes an integer day count; the range is checked by Validate.
func (d *RetentionDays) UnmarshalYAML(node *yaml.Node) error {
	value, err := decodeInteger(node, fieldError("settings.retention_days", "retention_days_range", "must be an integer between 0 and 3650"))
	*d = RetentionDays(value)
	return err
}

// Slots is the number of runs of one source type that may execute at once.
type Slots int

// UnmarshalYAML decodes an integer slot count and rejects values out of range.
func (s *Slots) UnmarshalYAML(node *yaml.Node) error {
	value, err := decodeInteger(node, fieldError("settings.concurrency", "concurrency_range", "each source type must be an integer between 1 and 16"))
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

// Settings holds the global run, sync and notification settings.
type Settings struct {
	RetryCount    RetryCount              `yaml:"retry_count" json:"retry_count"`
	TaskTimeout   string                  `yaml:"task_timeout" json:"task_timeout"`
	RetentionDays RetentionDays           `yaml:"retention_days" json:"retention_days"`
	Concurrency   Concurrency             `yaml:"concurrency" json:"concurrency"`
	Rsync         RsyncSettings           `yaml:"rsync" json:"rsync"`
	GitHub        GitHubSettings          `yaml:"github" json:"github"`
	Notifications map[string]Notification `yaml:"notifications,omitempty" json:"-"`
}

// Rsync holds the rsync options of one source.
type Rsync struct {
	PreserveLinks       *bool `yaml:"preserve_links,omitempty" json:"preserve_links,omitzero"`
	PreserveTimes       *bool `yaml:"preserve_times,omitempty" json:"preserve_times,omitzero"`
	PreservePermissions *bool `yaml:"preserve_permissions,omitempty" json:"preserve_permissions,omitzero"`
	PreserveHardlinks   *bool `yaml:"preserve_hardlinks,omitempty" json:"preserve_hardlinks,omitzero"`
	Delete              *bool `yaml:"delete,omitempty" json:"delete,omitzero"`
	UID                 *int  `yaml:"uid,omitempty" json:"uid,omitzero"`
	GID                 *int  `yaml:"gid,omitempty" json:"gid,omitzero"`
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

// RepoSync holds the DNF reposync options of one source.
type RepoSync struct {
	RepoID           string   `yaml:"repo_id" json:"repo_id"`
	Releasever       string   `yaml:"releasever" json:"releasever"`
	Basearch         string   `yaml:"basearch" json:"basearch"`
	Architectures    []string `yaml:"architectures" json:"architectures"`
	DownloadMetadata *bool    `yaml:"download_metadata,omitempty" json:"download_metadata,omitzero"`
	NewestOnly       bool     `yaml:"newest_only" json:"newest_only"`
	Delete           *bool    `yaml:"delete,omitempty" json:"delete,omitzero"`
	GPGCheck         *bool    `yaml:"gpgcheck,omitempty" json:"gpgcheck,omitzero"`
	GPGKeys          []string `yaml:"gpg_keys" json:"gpg_keys"`
	ModuleHotfixes   bool     `yaml:"module_hotfixes" json:"module_hotfixes"`
}

// GitHubRelease holds the GitHub release options of one source.
type GitHubRelease struct {
	Repository    string   `yaml:"repository" json:"repository"`
	ReleaseCount  int      `yaml:"release_count,omitempty" json:"release_count"`
	AssetPatterns []string `yaml:"asset_patterns" json:"asset_patterns"`
}

// Schedule is the cron schedule of one source.
type Schedule struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Cron    string `yaml:"cron" json:"cron"`
	Overlap string `yaml:"overlap" json:"overlap"`
}

// Source is one configured mirror source.
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

// Config is the complete MirrorDock configuration file.
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

// View is the configuration as shown to the admin console, with its revision.
type View struct {
	Config          Config `json:"config"`
	Revision        string `json:"-"` // sent as the ETag
	TokenConfigured bool   `json:"token_configured"`
	ProxyConfigured bool   `json:"proxy_configured"`
	RestartRequired bool   `json:"restart_required"`
	File            string `json:"file"`
}

// Manager holds the current configuration and the configuration loaded at
// startup, and reloads and saves the configuration file.
type Manager struct {
	mu   sync.RWMutex
	file string
	// cfg is the configuration in effect and revision the digest of the YAML
	// it was read from; startup keeps the version loaded at process start for
	// the settings that only apply after a restart.
	cfg      Config
	revision string
	startup  Config
}

// ErrMalformed reports a configuration file that is not one valid YAML
// document of the expected shape.
var ErrMalformed = errors.New("malformed configuration")

// ErrConflict reports that the configuration file changed after it was loaded.
var ErrConflict = errors.New("configuration file changed since it was loaded; reload it before saving")
var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)
var srcPattern = regexp.MustCompile(`^src([1-9][0-9]*)$`)
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// DefaultBool reports the value of an optional boolean that defaults to true.
func DefaultBool(v *bool) bool { return v == nil || *v }

// MarshalYAML encodes the source with its fields in a stable order.
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

// Defaults returns the built-in default configuration.
func Defaults() Config {
	return Config{
		Listen:     "0.0.0.0",
		Port:       8080,
		Path:       "../data",
		MirrorRoot: "/srv/mirrors",
		Log:        Log{Level: "info", Format: "json"},
		Settings: Settings{
			RetryCount:    3,
			TaskTimeout:   "8h",
			RetentionDays: 90,
			Concurrency:   Concurrency{Rsync: 4, RepoSync: 4, GitHubRelease: 4},
			Rsync:         RsyncSettings{IOTimeout: "5m", ConnectTimeout: "30s"},
			GitHub:        GitHubSettings{ReleaseCount: 5},
		},
	}
}

// Clone returns a deep copy of c.
func Clone(c Config) Config {
	out := c
	out.Settings.Notifications = maps.Clone(c.Settings.Notifications)
	out.Sources = slices.Clone(c.Sources)
	for i := range out.Sources {
		source := &out.Sources[i]
		source.Upstreams = maps.Clone(source.Upstreams)
		source.Exclude = slices.Clone(source.Exclude)
		source.RepoSync.Architectures = slices.Clone(source.RepoSync.Architectures)
		source.RepoSync.GPGKeys = slices.Clone(source.RepoSync.GPGKeys)
		source.GitHubRelease.AssetPatterns = slices.Clone(source.GitHubRelease.AssetPatterns)
		if source.Rsync.PreserveLinks != nil {
			source.Rsync.PreserveLinks = new(*source.Rsync.PreserveLinks)
		}
		if source.Rsync.PreserveTimes != nil {
			source.Rsync.PreserveTimes = new(*source.Rsync.PreserveTimes)
		}
		if source.Rsync.PreservePermissions != nil {
			source.Rsync.PreservePermissions = new(*source.Rsync.PreservePermissions)
		}
		if source.Rsync.PreserveHardlinks != nil {
			source.Rsync.PreserveHardlinks = new(*source.Rsync.PreserveHardlinks)
		}
		if source.Rsync.Delete != nil {
			source.Rsync.Delete = new(*source.Rsync.Delete)
		}
		if source.Rsync.UID != nil {
			source.Rsync.UID = new(*source.Rsync.UID)
		}
		if source.Rsync.GID != nil {
			source.Rsync.GID = new(*source.Rsync.GID)
		}
		if source.RepoSync.DownloadMetadata != nil {
			source.RepoSync.DownloadMetadata = new(*source.RepoSync.DownloadMetadata)
		}
		if source.RepoSync.Delete != nil {
			source.RepoSync.Delete = new(*source.RepoSync.Delete)
		}
		if source.RepoSync.GPGCheck != nil {
			source.RepoSync.GPGCheck = new(*source.RepoSync.GPGCheck)
		}
	}
	return out
}

// Candidates returns the upstream addresses of the source in priority order.
func (s Source) Candidates() []string {
	keys := s.upstreamKeys()
	addresses := make([]string, 0, len(keys))
	for _, key := range keys {
		addresses = append(addresses, s.Upstreams[key])
	}
	return addresses
}

// upstreamKeys returns the srcN keys with an address, in numeric order.
func (s Source) upstreamKeys() []string {
	sourceNumber := func(key string) int {
		number, _ := strconv.Atoi(strings.TrimPrefix(key, "src"))
		return number
	}
	keys := slices.SortedFunc(maps.Keys(s.Upstreams), func(a, b string) int {
		return cmp.Compare(sourceNumber(a), sourceNumber(b))
	})
	return slices.DeleteFunc(keys, func(key string) bool { return s.Upstreams[key] == "" })
}

// Resolve returns p as an absolute path, resolving a relative p against the
// directory of the configuration file.
func Resolve(file, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(filepath.Dir(file), p)
}

// Canonical resolves symlinks in p; when p does not exist it resolves the
// nearest existing parent and appends the rest.
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

// Within reports whether p is root or lies inside root.
func Within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// FieldError reports an invalid configuration field by its YAML path, such as
// "settings.rsync.io_timeout" or "sources.0.dest". The message never contains
// the field value, which may be a credential.
type FieldError struct {
	Path   string // dotted YAML path; list items use their index
	Code   string // snake_case reason code that clients map to their own text
	Reason string // what the value must satisfy; may be empty when Err is set
	Err    error  // underlying cause, if any
}

// Error implements error.
func (e *FieldError) Error() string {
	switch {
	case e.Err == nil:
		return e.Path + ": " + e.Reason
	case e.Reason == "":
		return e.Path + ": " + e.Err.Error()
	default:
		return e.Path + ": " + e.Reason + ": " + e.Err.Error()
	}
}

// Unwrap returns the underlying error.
func (e *FieldError) Unwrap() error { return e.Err }

func fieldError(field, code, reason string) error {
	return &FieldError{Path: field, Code: code, Reason: reason}
}

// fieldCause attributes err to the field at path.
func fieldCause(field, code string, err error) error {
	return &FieldError{Path: field, Code: code, Err: err}
}

// Validate checks c without modifying it. file is the configuration file the
// relative paths in c are resolved against; the filesystem checks (symlinks,
// TLS files, log file) depend on that location.
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
		return fieldError("log.level", "invalid_log_level", "must be debug, info, warn or error")
	}
	if c.Log.Format != "json" && c.Log.Format != "text" {
		return fieldError("log.format", "invalid_log_format", "must be json or text")
	}
	if !trimmedPrintable(c.Log.Path) {
		return fieldError("log.path", "untrimmed_text", "must not have surrounding whitespace or control characters")
	}
	username := c.Auth.Username
	if username == "" || len(username) > 128 || !trimmedPrintable(username) {
		return fieldError("auth.username", "invalid_username",
			"must be 1 to 128 bytes without surrounding whitespace or control characters")
	}
	if strings.TrimSpace(c.Auth.Password) == "" || len(c.Auth.Password) > maxPasswordBytes {
		return fieldError("auth.password", "invalid_password", "must not be blank and must be at most 72 bytes")
	}
	if net.ParseIP(c.Listen) == nil && c.Listen != "localhost" {
		return fieldError("listen", "invalid_listen", "must be an IP address or localhost")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fieldError("port", "port_range", "must be between 1 and 65535")
	}
	if strings.TrimSpace(c.Path) == "" {
		return fieldError("path", "required", "must not be empty")
	}
	if strings.TrimSpace(c.MirrorRoot) == "" {
		return fieldError("mirror_root", "required", "must not be empty")
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
	durations := []struct{ path, value string }{
		{"settings.task_timeout", s.TaskTimeout},
		{"settings.rsync.io_timeout", s.Rsync.IOTimeout},
		{"settings.rsync.connect_timeout", s.Rsync.ConnectTimeout},
	}
	for _, field := range durations {
		duration, err := time.ParseDuration(field.value)
		if err != nil || duration <= 0 {
			return fieldError(field.path, "invalid_duration", "must be a positive duration such as 30s or 8h")
		}
	}
	if s.RetryCount < 0 || s.RetryCount > 10 {
		return fieldError("settings.retry_count", "retry_count_range", "must be between 0 and 10")
	}
	if s.RetentionDays < 0 || s.RetentionDays > MaxRetentionDays {
		return fieldError("settings.retention_days", "retention_days_range", "must be between 0 and 3650")
	}
	slots := []struct {
		path  string
		value Slots
	}{
		{"settings.concurrency.rsync", s.Concurrency.Rsync},
		{"settings.concurrency.reposync", s.Concurrency.RepoSync},
		{"settings.concurrency.github_release", s.Concurrency.GitHubRelease},
	}
	for _, field := range slots {
		if field.value < 1 || field.value > MaxSlots {
			return fieldError(field.path, "concurrency_range", "must be between 1 and 16")
		}
	}
	limit := s.Rsync.LimitMB
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit < 0 {
		return fieldError("settings.rsync.limit_mb", "negative_number", "must be a non-negative number")
	}
	if s.GitHub.ReleaseCount < 1 || s.GitHub.ReleaseCount > 100 {
		return fieldError("settings.github.release_count", "release_count_range", "must be between 1 and 100")
	}
	token := s.GitHub.Token
	if strings.IndexFunc(token, unicode.IsSpace) >= 0 || strings.IndexFunc(token, unicode.IsControl) >= 0 {
		return fieldError("settings.github.token", "invalid_token",
			"must not contain whitespace or control characters; enter the token only")
	}
	if _, err := ParseProxyField("settings.github.proxy", s.GitHub.Proxy); err != nil {
		return err
	}
	_, err := ParseProxyField("settings.rsync.proxy", s.Rsync.Proxy)
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
		return resolvedPaths{}, fieldCause("mirror_root", "path_unresolvable", err)
	}
	data, err := Canonical(Resolve(file, c.Path))
	if err != nil {
		return resolvedPaths{}, fieldCause("path", "path_unresolvable", err)
	}
	configFile, err := Canonical(file)
	if err != nil {
		return resolvedPaths{}, fmt.Errorf("resolve configuration file: %w", err)
	}
	isolated := !Within(root, data) && !Within(data, root) && !Within(root, configFile)
	if root == string(filepath.Separator) || !isolated {
		return resolvedPaths{}, fieldError("mirror_root", "mirror_root_not_isolated",
			"must not be the filesystem root, overlap path, or contain the configuration file")
	}
	return resolvedPaths{mirrorRoot: root, data: data, configFile: configFile}, nil
}

// validateLogPath keeps the program log out of the mirror, the task log
// directories and every file the service owns.
func (c Config) validateLogPath(file string, paths resolvedPaths) error {
	logFile, err := Canonical(Resolve(file, c.Log.Path))
	if err != nil {
		return fieldCause("log.path", "path_unresolvable", err)
	}
	if Within(paths.mirrorRoot, logFile) {
		return fieldError("log.path", "inside_mirror_root", "must not be inside mirror_root")
	}
	taskLogs, err := Canonical(filepath.Join(paths.data, "logs"))
	if err != nil {
		return fieldCause("log.path", "path_unresolvable", err)
	}
	if Within(taskLogs, logFile) && filepath.Dir(logFile) != taskLogs {
		return fieldError("log.path", "inside_task_logs",
			"must not be inside a task log directory; <path>/logs/mirrordock.log is allowed")
	}
	logInfo, err := os.Stat(logFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fieldCause("log.path", "path_unresolvable", err)
	}
	if logInfo != nil && !logInfo.Mode().IsRegular() {
		return fieldError("log.path", "not_regular_file", "must be a regular file")
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
			return fieldCause("log.path", "path_unresolvable", err)
		}
		protectedInfo, statErr := os.Stat(candidate)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return fieldCause("log.path", "path_unresolvable", statErr)
		}
		sameFile := logInfo != nil && protectedInfo != nil && os.SameFile(logInfo, protectedInfo)
		if logFile == candidate || sameFile {
			return fieldError("log.path", "protected_file",
				"must not be the configuration, database, instance lock, notification key or TLS file")
		}
	}
	return nil
}

func (c Config) validateTLS(file, mirrorRoot string) error {
	if (c.TLS.Cert == "") != (c.TLS.Key == "") {
		return fieldError("tls", "tls_pair_incomplete", "cert and key must be set together")
	}
	if c.TLS.Cert == "" {
		return nil
	}
	for _, field := range []struct{ path, value string }{{"tls.cert", c.TLS.Cert}, {"tls.key", c.TLS.Key}} {
		resolved, err := Canonical(Resolve(file, field.value))
		if err != nil {
			return fieldCause(field.path, "path_unresolvable", err)
		}
		if Within(mirrorRoot, resolved) {
			return fieldError(field.path, "inside_mirror_root", "must not be inside mirror_root")
		}
	}
	if _, err := tls.LoadX509KeyPair(Resolve(file, c.TLS.Cert), Resolve(file, c.TLS.Key)); err != nil {
		return &FieldError{Path: "tls", Code: "tls_load_failed", Reason: "load certificate and key", Err: err}
	}
	return nil
}

// reservedTopLevel lists the first dest segments owned by the service itself.
var reservedTopLevel = []string{"admin", "api", "favicon.ico"}

// ReservedTopLevel reports whether a first path segment is routed to the
// service itself, so the mirror root can never serve it.
func ReservedTopLevel(segment string) bool { return slices.Contains(reservedTopLevel, segment) }

// identifierRule describes idPattern for validation messages.
const identifierRule = "must be 1 to 80 letters, digits, '.', '_' or '-', starting with a letter or digit"

func validateSources(sources []Source, mirrorRoot string) error {
	ids := map[string]bool{}
	dests := []string{}
	resolvedDests := []string{}
	for i, s := range sources {
		p := "sources." + strconv.Itoa(i)
		if !idPattern.MatchString(s.ID) {
			return fieldError(p+".id", "invalid_identifier", identifierRule)
		}
		if ids[s.ID] {
			return fieldError(p+".id", "duplicate_id", fmt.Sprintf("duplicate id %q", s.ID))
		}
		ids[s.ID] = true
		if strings.TrimSpace(s.Name) == "" {
			return fieldError(p+".name", "required", "must not be empty")
		}
		if err := s.validateDest(p); err != nil {
			return err
		}
		for _, existing := range dests {
			nested := strings.HasPrefix(existing, s.Dest+"/") || strings.HasPrefix(s.Dest, existing+"/")
			if existing == s.Dest || nested {
				return fieldError(p+".dest", "dest_overlap", "must not equal or nest with the dest of another source")
			}
		}
		dests = append(dests, s.Dest)
		target, err := Canonical(filepath.Join(mirrorRoot, filepath.FromSlash(s.Dest)))
		if err != nil {
			return fieldCause(p+".dest", "path_unresolvable", err)
		}
		if !Within(mirrorRoot, target) {
			return fieldError(p+".dest", "dest_outside_mirror_root", "resolves outside mirror_root through a symbolic link")
		}
		for _, existing := range resolvedDests {
			if Within(existing, target) || Within(target, existing) {
				return fieldError(p+".dest", "dest_overlap",
					"must not equal or nest with the dest of another source after resolving symbolic links")
			}
		}
		resolvedDests = append(resolvedDests, target)
		if err := s.validateAddresses(p); err != nil {
			return err
		}
		if err := s.validateExecutor(p); err != nil {
			return err
		}
		if err := s.Schedule.validate(p + ".schedule"); err != nil {
			return err
		}
	}
	return nil
}

func (s Source) validateDest(p string) error {
	clean := path.Clean(s.Dest) == s.Dest
	if s.Dest == "" || !clean || strings.HasPrefix(s.Dest, "/") || strings.ContainsAny(s.Dest, "\\\x00\r\n") {
		return fieldError(p+".dest", "invalid_dest", "must be a clean relative directory")
	}
	first, _, _ := strings.Cut(s.Dest, "/")
	if ReservedTopLevel(first) {
		return fieldError(p+".dest", "reserved_dest", "must not start with a reserved path (admin, api, favicon.ico)")
	}
	for segment := range strings.SplitSeq(s.Dest, "/") {
		if segment == ".." || segment == "." || strings.HasPrefix(segment, ".") {
			return fieldError(p+".dest", "hidden_dest_segment", "must not contain segments starting with '.'")
		}
	}
	return nil
}

func (s Source) validateAddresses(p string) error {
	for key, address := range s.Upstreams {
		if !srcPattern.MatchString(key) {
			return fieldError(p+"."+key, "unknown_source_field", "unknown field; only src1, src2, ... are accepted")
		}
		if _, err := strconv.Atoi(strings.TrimPrefix(key, "src")); err != nil {
			return fieldError(p+"."+key, "source_number_range", "source number is out of range")
		}
		if strings.ContainsAny(address, "\x00\r\n") {
			return fieldError(p+"."+key, "control_characters", "must not contain control characters")
		}
		if err := publicURL(address); err != nil {
			return fieldCause(p+"."+key, urlErrorCode(err), err)
		}
	}
	for i, address := range s.RepoSync.GPGKeys {
		if err := SourceURL(address); err != nil {
			return fieldCause(p+".reposync.gpg_keys."+strconv.Itoa(i), urlErrorCode(err), err)
		}
	}
	return nil
}

func (s Source) validateExecutor(p string) error {
	switch s.Type {
	case "rsync":
		return s.validateRsync(p)
	case "reposync":
		return s.validateRepoSync(p)
	case "github_release":
		return s.validateGitHubRelease(p)
	default:
		return fieldError(p+".type", "invalid_source_type", "must be rsync, reposync or github_release")
	}
}

func (s Source) validateRsync(p string) error {
	keys := s.upstreamKeys()
	if len(keys) == 0 {
		return fieldError(p+".src1", "required", "must be set")
	}
	for _, key := range keys {
		address, err := url.Parse(s.Upstreams[key])
		plain := err == nil && address.User == nil && address.RawQuery == "" && address.Fragment == ""
		if !plain || address.Scheme != "rsync" || address.Host == "" {
			return fieldError(p+"."+key, "invalid_rsync_url", "must be an rsync:// URL without user info, query or fragment")
		}
		remote := strings.Trim(address.Path, "/")
		directoryWithoutSlash := strings.Contains(remote, "/") && !strings.HasSuffix(address.Path, "/")
		if remote == "" || directoryWithoutSlash {
			return fieldError(p+"."+key, "missing_rsync_module", "must name an rsync module; a directory inside it must end with /")
		}
	}
	identities := []struct {
		path  string
		value *int
	}{{p + ".rsync.uid", s.Rsync.UID}, {p + ".rsync.gid", s.Rsync.GID}}
	for _, identity := range identities {
		if identity.value != nil && (*identity.value < 0 || int64(*identity.value) >= math.MaxUint32) {
			return fieldError(identity.path, "id_range", "must be between 0 and 4294967294")
		}
	}
	for _, pattern := range s.Exclude {
		if strings.ContainsAny(pattern, "\x00\r\n") {
			return fieldError(p+".exclude", "control_characters", "must not contain control characters")
		}
	}
	return s.Rsync.validateProxy(p + ".rsync")
}

func (r Rsync) validateProxy(p string) error {
	switch r.ProxyMode {
	case "", ProxyGlobal, ProxyDirect:
		if r.Proxy != "" {
			return fieldError(p+".proxy", "proxy_not_custom", "must be empty unless proxy_mode is custom")
		}
		return nil
	case ProxyCustom:
		if r.Proxy == "" {
			return fieldError(p+".proxy", "proxy_required", "must be set when proxy_mode is custom")
		}
		_, err := ParseProxyField(p+".proxy", r.Proxy)
		return err
	default:
		return fieldError(p+".proxy_mode", "invalid_proxy_mode", "must be global, direct or custom")
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

func (s Source) validateRepoSync(p string) error {
	options := s.RepoSync
	if len(s.Candidates()) != 1 {
		return fieldError(p, "reposync_single_source", "a reposync source accepts exactly one address, src1")
	}
	if err := SourceURL(s.Upstreams["src1"]); err != nil {
		return fieldCause(p+".src1", urlErrorCode(err), err)
	}
	identifiers := []struct{ path, value string }{
		{p + ".reposync.repo_id", options.RepoID},
		{p + ".reposync.releasever", options.Releasever},
		{p + ".reposync.basearch", options.Basearch},
	}
	for _, field := range identifiers {
		if !idPattern.MatchString(field.value) {
			return fieldError(field.path, "invalid_identifier", identifierRule)
		}
	}
	if options.NewestOnly && DefaultBool(options.DownloadMetadata) {
		return fieldError(p+".reposync.newest_only", "newest_only_with_metadata", "must be false while download_metadata is enabled")
	}
	if DefaultBool(options.GPGCheck) && len(options.GPGKeys) == 0 {
		return fieldError(p+".reposync.gpg_keys", "gpg_keys_required", "must not be empty while gpgcheck is enabled")
	}
	for i, architecture := range options.Architectures {
		if !idPattern.MatchString(architecture) {
			return fieldError(p+".reposync.architectures."+strconv.Itoa(i), "invalid_identifier", identifierRule)
		}
	}
	return nil
}

func (s Source) validateGitHubRelease(p string) error {
	release := s.GitHubRelease
	if !repoPattern.MatchString(release.Repository) {
		return fieldError(p+".github_release.repository", "invalid_repository", "must be <owner>/<repository>")
	}
	if release.ReleaseCount < 0 || release.ReleaseCount > 100 {
		return fieldError(p+".github_release.release_count", "source_release_count_range", "must be between 0 and 100")
	}
	for i, pattern := range release.AssetPatterns {
		if _, err := path.Match(pattern, "test"); err != nil {
			return &FieldError{
				Path:   p + ".github_release.asset_patterns." + strconv.Itoa(i),
				Code:   "invalid_glob",
				Reason: "invalid glob",
				Err:    err,
			}
		}
	}
	return nil
}

func (schedule Schedule) validate(p string) error {
	if schedule.Overlap != "" && schedule.Overlap != "skip" {
		return fieldError(p+".overlap", "invalid_overlap", "must be skip or empty")
	}
	if !schedule.Enabled {
		return nil
	}
	if len(strings.Fields(schedule.Cron)) != 5 {
		return fieldError(p+".cron", "invalid_cron_fields", "must have exactly five fields; the process time zone is used")
	}
	if _, err := cron.ParseStandard(schedule.Cron); err != nil {
		return fieldCause(p+".cron", "invalid_cron", err)
	}
	return nil
}

// errNotHTTPURL is returned by HTTPURL; it does not include the URL, which may
// carry a signature.
var errNotHTTPURL = errors.New("must be an http or https URL with a host and without user info")

// HTTPURL validates an absolute http or https URL without user information or
// control characters.
func HTTPURL(v string) error {
	address, err := url.Parse(v)
	if err != nil || strings.ContainsAny(v, "\x00\r\n") {
		return errNotHTTPURL
	}
	httpScheme := address.Scheme == "https" || address.Scheme == "http"
	if !httpScheme || address.Host == "" || address.User != nil {
		return errNotHTTPURL
	}
	return nil
}

// SourceURL validates a configured source address. Configured source addresses
// are public facts, unlike transient signed asset URLs received from GitHub. No
// credential-bearing URL component is accepted.
func SourceURL(v string) error {
	if err := HTTPURL(v); err != nil {
		return err
	}
	return publicURL(v)
}

// errURLCredentials is returned by SourceURL for addresses that carry user
// info, a query or a fragment.
var errURLCredentials = errors.New("must not contain user info, a query or a fragment; URL authentication is not supported")

func publicURL(v string) error {
	address, err := url.Parse(v)
	if err != nil || address.User != nil || address.RawQuery != "" || address.ForceQuery || address.Fragment != "" {
		return errURLCredentials
	}
	return nil
}

// urlErrorCode returns the FieldError code for an error from SourceURL.
func urlErrorCode(err error) string {
	if errors.Is(err, errURLCredentials) {
		return "url_has_credentials"
	}
	return "invalid_http_url"
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
	invalid := fieldError(field, "invalid_proxy_url", "must be a proxy URL without path, query or fragment")
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
		return nil, fieldError(field, "invalid_proxy_scheme", "scheme must be http, https, socks5 or socks5h")
	}
	if port := proxy.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, fieldError(field, "port_range", "port must be between 1 and 65535")
		}
	}
	if proxy.User != nil {
		username := proxy.User.Username()
		password, _ := proxy.User.Password()
		socks := proxy.Scheme == "socks5" || proxy.Scheme == "socks5h"
		// SOCKS5 username/password authentication encodes each field in one byte.
		socksTooLong := socks && (len(username) > 255 || len(password) > 255)
		if strings.ContainsAny(username+password, "\x00\r\n") || socksTooLong {
			return nil, fieldError(field, "invalid_proxy_userinfo", "user info is invalid")
		}
	}
	return proxy, nil
}

// digest is the revision of a YAML file: the SHA-256 of its exact bytes.
func digest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

// Parse decodes and validates configuration file contents; relative paths
// resolve against file.
func Parse(contents []byte, file string) (Config, error) {
	c := Defaults()
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		if _, isField := errors.AsType[*FieldError](err); isField {
			return c, err
		}
		return c, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("%w: the file must contain exactly one YAML document", ErrMalformed)
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

// LoadSource describes where configuration comes from. It is the Source of
// the configuration convention, renamed because Source names a mirror source
// in this package.
type LoadSource struct {
	Path     string // selected file; relative to WorkDir unless absolute
	Explicit bool   // chosen by --config or CONFIG_FILE
	WorkDir  string // base for a relative Path
	// LookupEnv is os.LookupEnv in production. MirrorDock has no field-level
	// environment overrides, so configuration loading does not consult it.
	LookupEnv func(string) (string, bool)
}

// Open loads and validates the configuration file selected by src and returns
// a Manager that serves, reloads and writes back that file. The file must
// exist even when src is not explicit: the built-in defaults have no
// administrator account, which is required.
func Open(src LoadSource) (*Manager, error) {
	file := src.Path
	if !filepath.IsAbs(file) {
		if src.WorkDir == "" {
			return nil, fmt.Errorf("relative configuration path %q needs a work directory", file)
		}
		file = filepath.Join(src.WorkDir, file)
	}
	contents, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) && !src.Explicit {
		return nil, fmt.Errorf("%w; built-in defaults do not set the required auth.username and auth.password", err)
	}
	if err != nil {
		return nil, err
	}
	// The manager keeps the resolved file: writes replace the target of a
	// symbolic link, never the link itself.
	file, err = filepath.EvalSymlinks(file)
	if err != nil {
		return nil, err
	}
	c, err := Parse(contents, file)
	if err != nil {
		return nil, err
	}
	return &Manager{file: file, cfg: c, startup: Clone(c), revision: digest(contents)}, nil
}

// File returns the path of the configuration file.
func (m *Manager) File() string { return m.file }

// Current returns a copy of the current configuration.
func (m *Manager) Current() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Clone(m.cfg)
}

// Startup returns a copy of the configuration loaded at startup.
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

// StorageChanged reports whether the state directory or the mirror root in
// the current configuration differs from the one this process runs on. Until
// the restart, sources are validated against a root that is not in use, so
// nothing may be synchronized; other deployment fields do not affect runs.
func (m *Manager) StorageChanged() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Path != m.startup.Path || m.cfg.MirrorRoot != m.startup.MirrorRoot
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

// ReloadChecked reads the configuration file again and adopts it only when
// check accepts it.
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

// Save writes c to the configuration file if revision still matches. Non-nil
// tokenUpdate and proxyUpdate replace the stored GitHub token and proxy.
func (m *Manager) Save(params SaveOptions) error {
	c := params.Config
	revision := params.Revision
	tokenUpdate := params.TokenUpdate
	proxyUpdate := params.ProxyUpdate

	return m.save(saveOptions{c: c, revision: revision, tokenUpdate: tokenUpdate, proxyUpdate: proxyUpdate, channels: nil})
}

// SaveNotifications writes the notification channels to the configuration file
// if revision still matches.
func (m *Manager) SaveNotifications(channels map[string]Notification, revision string) error {
	return m.save(saveOptions{c: Config{}, revision: revision, tokenUpdate: nil, proxyUpdate: nil, channels: channels})
}

func (m *Manager) save(params saveOptions) error {
	c := params.c
	revision := params.revision
	tokenUpdate := params.tokenUpdate
	proxyUpdate := params.proxyUpdate
	channels := params.channels

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
	if err := AtomicWrite(m.file, written, 0o600); err != nil {
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
	defer dir.Close() // Directory fsync is checked before this descriptor is released.
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

// SaveOptions groups the inputs to Save.
type SaveOptions struct {
	Config      Config
	Revision    string
	TokenUpdate *string
	ProxyUpdate *string
}

// saveOptions groups the inputs to save.
type saveOptions struct {
	c           Config
	revision    string
	tokenUpdate *string
	proxyUpdate *string
	channels    map[string]Notification
}
