package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mirrordock/internal/config"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Progress struct {
	ReceivedBytes      *int64   `json:"received_bytes"`
	ReceivedIncomplete bool     `json:"received_incomplete,omitzero"`
	Phase              string   `json:"phase"`
	Bytes              int64    `json:"bytes"`
	Total              int64    `json:"total"`
	Percent            *float64 `json:"percent"`
	Speed              float64  `json:"speed"`
	ETA                *float64 `json:"eta_seconds"`
	Files              int      `json:"files"`
	Success            int      `json:"success"`
	Failed             int      `json:"failed"`
	Skipped            int      `json:"skipped"`
}

type Task struct {
	Source    config.Source
	Settings  config.Settings
	Directory string
	Candidate string
	Token     string
	// Proxy is the rsync proxy URL resolved at execution; blank is direct.
	Proxy string
}

type Failure struct {
	Code          string
	Message       string
	Retryable     bool
	SourceFailure bool
	// ProxyFailure means the proxy itself failed, so the other candidates,
	// which use the same proxy, are not tried in this round.
	ProxyFailure bool
	RetryAt      time.Time
	ExitCode     int
}

func (e *Failure) Error() string { return e.Code + ": " + e.Message }

type Runner interface {
	Run(context.Context, Task, func(Progress), io.Writer) error
}

type SystemRunner struct{}

// Run dispatches the task to the executor of its source type.
func (SystemRunner) Run(ctx context.Context, t Task, emit func(Progress), log io.Writer) error {
	switch t.Source.Type {
	case "rsync":
		env, err := rsyncEnv(t)
		if err != nil {
			return err
		}
		if t.Proxy != "" {
			proxy, _ := config.ParseProxyField("rsync.proxy", t.Proxy)
			fmt.Fprintf(log, "经 %s 代理 %s 连接上游\n", proxy.Scheme, proxyAddress(proxy))
		}
		args := RsyncArgs(t)
		fmt.Fprintf(log, "rsync %s\n", strings.Join(args, " "))
		return command(ctx, "rsync", args, env, emit, log)
	case "reposync":
		return reposync(ctx, t, emit, log)
	case "github_release":
		return GitHub(ctx, t, emit, log)
	default:
		return &Failure{Code: "INVALID_TYPE", Message: t.Source.Type}
	}
}

// seconds converts a duration setting to whole seconds, at least one.
func seconds(setting string) string {
	duration, _ := time.ParseDuration(setting)
	return strconv.FormatInt(max(1, int64(math.Ceil(duration.Seconds()))), 10)
}

// RsyncArgs builds the rsync argument list. Every value is a separate argv
// element after its option; the source address follows "--" so it can never
// be parsed as an option.
func RsyncArgs(t Task) []string {
	options := t.Source.Rsync
	args := []string{
		"--recursive",
		"--partial-dir=.rsync-partial",
		"--exclude=.mirrordock*",
		"--info=progress2",
		"--stats",
		"--outbuf=L",
		"--timeout=" + seconds(t.Settings.Rsync.IOTimeout),
		"--contimeout=" + seconds(t.Settings.Rsync.ConnectTimeout),
	}
	toggles := []struct {
		enabled *bool
		flag    string
	}{
		{options.PreserveLinks, "--links"},
		{options.PreserveTimes, "--times"},
		{options.PreservePermissions, "--perms"},
		{options.PreserveHardlinks, "--hard-links"},
		{options.Delete, "--delete"},
	}
	for _, toggle := range toggles {
		if config.DefaultBool(toggle.enabled) {
			args = append(args, toggle.flag)
		}
	}
	if options.UID != nil || options.GID != nil {
		owner := ""
		if options.UID != nil {
			args = append(args, "--owner")
			owner = strconv.Itoa(*options.UID)
		}
		if options.GID != nil {
			args = append(args, "--group")
			owner += ":" + strconv.Itoa(*options.GID)
		}
		args = append(args, "--chown="+owner)
	}
	if limit := t.Settings.Rsync.LimitMB; limit > 0 {
		// limit_mb is decimal MB/s; --bwlimit takes KiB/s.
		kibibytes := max(1, int64(math.Round(limit*1e6/1024)))
		args = append(args, "--bwlimit="+strconv.FormatInt(kibibytes, 10))
	}
	for _, pattern := range t.Source.Exclude {
		args = append(args, "--exclude="+pattern)
	}
	return append(args, "--", t.Candidate, t.Directory+string(filepath.Separator))
}

// Classify maps an rsync exit code to a failure. SourceFailure lets the run
// try the next mirror; Retryable lets a later round try again.
func Classify(code int, message string) error {
	failure := &Failure{Code: "UPSTREAM_ERROR", Message: message, ExitCode: code, SourceFailure: true}
	switch code {
	case 2, 3: // protocol incompatibility, file selection error
	case 5: // error starting the client-server protocol
		permanent := rsyncPermanentLine(message) || strings.Contains(strings.ToLower(message), "permission denied")
		failure.Retryable = !permanent
	case 10, 12, 30, 35: // socket I/O, protocol stream, timeouts
		failure.Retryable = true
	case 23, 24: // partial transfer, vanished source files
		local := localFailureLine(message)
		failure.Code = "PARTIAL_TRANSFER"
		failure.Retryable = !local
		failure.SourceFailure = !local
	default:
		failure.Code = "PROCESS_FAILED"
		failure.SourceFailure = false
	}
	return failure
}

// localFailureLine recognizes receiver-side errors of this host, which no
// other mirror or retry can fix.
func localFailureLine(message string) bool {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "[sender]") || strings.Contains(lower, "@error:") {
		return false
	}
	if strings.Contains(lower, "no space left") || strings.Contains(lower, "read-only file system") {
		return true
	}
	receiverSide := strings.Contains(lower, "[receiver]") || strings.Contains(lower, "[generator]")
	return receiverSide && strings.Contains(lower, "permission denied")
}

func reposync(ctx context.Context, t Task, emit func(Progress), log io.Writer) error {
	cfg := t.Source.RepoSync
	if !config.DefaultBool(cfg.DownloadMetadata) {
		// Do not expose metadata cloned from an earlier complete-repository run.
		if err := os.RemoveAll(filepath.Join(t.Directory, "repodata")); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(t.Directory, 0755); err != nil {
		return err
	}
	private := filepath.Join(t.Directory, ".mirrordock-dnf")
	// A previous process may have stopped before its deferred cleanup.
	if err := os.RemoveAll(private); err != nil {
		return err
	}
	err := os.Mkdir(private, 0700)
	if err != nil {
		return err
	}
	defer os.RemoveAll(private)
	if config.DefaultBool(cfg.GPGCheck) {
		if err := importRepoKeys(ctx, private, cfg.GPGKeys, emit, log); err != nil {
			return err
		}
	}
	repoDir := filepath.Join(private, "repos")
	if err = os.Mkdir(repoDir, 0700); err != nil {
		return err
	}
	// Every interpolated value was validated: identifiers match the source ID
	// pattern and URLs contain no line breaks, so no INI key can be injected.
	repository := strings.Join([]string{
		"[" + cfg.RepoID + "]",
		"name=" + cfg.RepoID,
		"baseurl=" + t.Candidate,
		"enabled=1",
		fmt.Sprintf("gpgcheck=%t", config.DefaultBool(cfg.GPGCheck)),
		"gpgkey=" + strings.Join(cfg.GPGKeys, " "),
		fmt.Sprintf("module_hotfixes=%t", cfg.ModuleHotfixes),
	}, "\n") + "\n"
	if err = os.WriteFile(filepath.Join(repoDir, "source.repo"), []byte(repository), 0600); err != nil {
		return err
	}
	installRoot := filepath.Join(private, "root")
	conf := filepath.Join(private, "dnf.conf")
	mainSection := strings.Join([]string{
		"[main]",
		"reposdir=" + repoDir,
		"cachedir=" + filepath.Join(private, "cache"),
		"persistdir=" + filepath.Join(private, "state"),
		"logdir=" + private,
		"installroot=" + installRoot,
	}, "\n") + "\n"
	if err = os.WriteFile(conf, []byte(mainSection), 0600); err != nil {
		return err
	}
	args := []string{
		"-y",
		"--config=" + conf,
		"--installroot=" + installRoot,
		"--setopt=reposdir=" + repoDir,
		"--setopt=varsdir=" + filepath.Join(private, "vars"),
		"--releasever=" + cfg.Releasever,
		"--forcearch=" + cfg.Basearch,
		"reposync",
		"--repoid=" + cfg.RepoID,
		"--download-path=" + t.Directory,
		"--norepopath",
	}
	if config.DefaultBool(cfg.DownloadMetadata) {
		args = append(args, "--download-metadata")
	}
	if cfg.NewestOnly {
		args = append(args, "--newest-only")
	}
	if config.DefaultBool(cfg.Delete) {
		args = append(args, "--delete")
	}
	if config.DefaultBool(cfg.GPGCheck) {
		args = append(args, "--gpgcheck")
	}
	for _, architecture := range cfg.Architectures {
		args = append(args, "--arch="+architecture)
	}
	fmt.Fprintf(log, "dnf %s\n", strings.Join(args, " "))
	return command(ctx, "dnf", args, processEnv(), emit, log)
}

// Reposync verifies against the installroot RPM database but does not import
// gpgkey URLs itself. Keep key downloads bounded and the keyring task-private.
func importRepoKeys(ctx context.Context, private string, keys []string, emit func(Progress), log io.Writer) error {
	const maxKeyBytes = 1 << 20
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: downloadRedirect}
	args := []string{"--root", filepath.Join(private, "root"), "--import"}
	for i, address := range keys {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return &Failure{Code: "GPG_KEY_DOWNLOAD", Message: "GPG 公钥地址无效"}
		}
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return httpNetworkError(err, "", "GPG_KEY_DOWNLOAD", "GPG 公钥下载")
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return &Failure{
				Code:      "GPG_KEY_DOWNLOAD",
				Message:   fmt.Sprintf("GPG 公钥下载 HTTP %d", response.StatusCode),
				Retryable: transientHTTP(response.StatusCode),
			}
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, maxKeyBytes+1))
		response.Body.Close()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return httpNetworkError(err, "", "GPG_KEY_DOWNLOAD", "GPG 公钥读取")
		}
		if len(data) == 0 || len(data) > maxKeyBytes {
			return &Failure{Code: "GPG_KEY_DOWNLOAD", Message: "GPG 公钥必须非空且不超过 1 MiB"}
		}
		file := filepath.Join(private, fmt.Sprintf("key-%d.asc", i))
		if err := os.WriteFile(file, data, 0600); err != nil {
			return err
		}
		args = append(args, file)
	}
	if _, err := fmt.Fprintln(log, "rpmkeys: 将配置的公钥导入任务私有 RPM 数据库"); err != nil {
		return err
	}
	return command(ctx, "rpmkeys", args, processEnv(), emit, log)
}

func AsFailure(err error) *Failure {
	if failure, ok := errors.AsType[*Failure](err); ok {
		return failure
	}
	return &Failure{Code: "LOCAL_ERROR", Message: err.Error()}
}

// rsyncPermanentLine recognizes daemon refusals that retrying the same
// mirror cannot fix.
func rsyncPermanentLine(message string) bool {
	lower := strings.ToLower(message)
	for _, refusal := range []string{"auth failed", "unknown module", "access denied"} {
		if strings.Contains(lower, refusal) {
			return true
		}
	}
	return strings.Contains(lower, "@error:") && strings.Contains(lower, "permission denied")
}
