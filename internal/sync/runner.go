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
}
type Failure struct {
	Code      string
	Message   string
	Retryable bool
	ExitCode  int
}

func (e *Failure) Error() string { return e.Code + ": " + e.Message }

type Runner interface {
	Run(context.Context, Task, func(Progress), io.Writer) error
}
type SystemRunner struct{}

func (SystemRunner) Run(ctx context.Context, t Task, emit func(Progress), log io.Writer) error {
	switch t.Source.Type {
	case "rsync":
		args := RsyncArgs(t)
		fmt.Fprintf(log, "rsync %s\n", strings.Join(args, " "))
		return command(ctx, "rsync", args, emit, log)
	case "reposync":
		return reposync(ctx, t, emit, log)
	case "github_release":
		return GitHub(ctx, t, emit, log)
	default:
		return &Failure{Code: "INVALID_TYPE", Message: t.Source.Type}
	}
}
func seconds(v string) string {
	d, _ := time.ParseDuration(v)
	return strconv.FormatInt(max(1, int64(math.Ceil(d.Seconds()))), 10)
}
func RsyncArgs(t Task) []string {
	opts := t.Source.Rsync
	args := []string{"--recursive", "--partial-dir=.rsync-partial", "--delay-updates", "--info=progress2", "--stats", "--outbuf=L", "--timeout=" + seconds(t.Settings.Rsync.IOTimeout), "--contimeout=" + seconds(t.Settings.Rsync.ConnectTimeout)}
	for _, o := range []struct {
		v    *bool
		flag string
	}{{opts.PreserveLinks, "--links"}, {opts.PreserveTimes, "--times"}, {opts.PreservePermissions, "--perms"}, {opts.PreserveHardlinks, "--hard-links"}, {opts.Delete, "--delete"}} {
		if config.DefaultBool(o.v) {
			args = append(args, o.flag)
		}
	}
	if opts.UID != nil || opts.GID != nil {
		uid, gid := "", ""
		if opts.UID != nil {
			args = append(args, "--owner")
			uid = strconv.Itoa(*opts.UID)
		}
		if opts.GID != nil {
			args = append(args, "--group")
			gid = strconv.Itoa(*opts.GID)
		}
		chown := uid
		if opts.GID != nil {
			chown = uid + ":" + gid
		}
		args = append(args, "--chown="+chown)
	}
	if t.Settings.Rsync.LimitMB > 0 {
		args = append(args, "--bwlimit="+strconv.FormatInt(max(1, int64(math.Round(t.Settings.Rsync.LimitMB*1e6/1024))), 10))
	}
	for _, p := range t.Source.Exclude {
		args = append(args, "--exclude="+p)
	}
	return append(args, "--", t.Candidate, t.Directory+string(filepath.Separator))
}
func Classify(code int, message string) error {
	switch code {
	case 5:
		permanent := rsyncPermanentLine(message) || strings.Contains(strings.ToLower(message), "permission denied")
		return &Failure{Code: "UPSTREAM_ERROR", Message: message, Retryable: !permanent, ExitCode: code}
	case 10, 12, 30, 35:
		return &Failure{Code: "UPSTREAM_ERROR", Message: message, Retryable: true, ExitCode: code}
	case 23, 24:
		return &Failure{Code: "PARTIAL_TRANSFER", Message: message, Retryable: !localFailureLine(message), ExitCode: code}
	default:
		return &Failure{Code: "PROCESS_FAILED", Message: message, ExitCode: code}
	}
}

func localFailureLine(message string) bool {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "[sender]") || strings.Contains(lower, "@error:") {
		return false
	}
	return strings.Contains(lower, "no space left") || strings.Contains(lower, "read-only file system") || (strings.Contains(lower, "permission denied") && (strings.Contains(lower, "[receiver]") || strings.Contains(lower, "[generator]")))
}
func reposync(ctx context.Context, t Task, emit func(Progress), log io.Writer) error {
	cfg := t.Source.RepoSync
	private, err := os.MkdirTemp(filepath.Dir(t.Directory), "dnf-private-")
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
	repos := fmt.Sprintf("[%s]\nname=%s\nbaseurl=%s\nenabled=1\ngpgcheck=%t\ngpgkey=%s\nmodule_hotfixes=%t\n", cfg.RepoID, cfg.RepoID, t.Candidate, config.DefaultBool(cfg.GPGCheck), strings.Join(cfg.GPGKeys, " "), cfg.ModuleHotfixes)
	if err = os.WriteFile(filepath.Join(repoDir, "source.repo"), []byte(repos), 0600); err != nil {
		return err
	}
	conf := filepath.Join(private, "dnf.conf")
	contents := fmt.Sprintf("[main]\nreposdir=%s\ncachedir=%s\npersistdir=%s\nlogdir=%s\ninstallroot=%s\n", repoDir, filepath.Join(private, "cache"), filepath.Join(private, "state"), private, filepath.Join(private, "root"))
	if err = os.WriteFile(conf, []byte(contents), 0600); err != nil {
		return err
	}
	args := []string{"-y", "--config=" + conf, "--installroot=" + filepath.Join(private, "root"), "--setopt=reposdir=" + repoDir, "--setopt=varsdir=" + filepath.Join(private, "vars"), "--releasever=" + cfg.Releasever, "--forcearch=" + cfg.Basearch, "reposync", "--repoid=" + cfg.RepoID, "--download-path=" + t.Directory, "--norepopath"}
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
	for _, a := range cfg.Architectures {
		args = append(args, "--arch="+a)
	}
	fmt.Fprintf(log, "dnf %s\n", strings.Join(args, " "))
	return command(ctx, "dnf", args, emit, log)
}

// Reposync verifies against the installroot RPM database but does not import
// gpgkey URLs itself. Keep key downloads bounded and the keyring task-private.
func importRepoKeys(ctx context.Context, private string, keys []string, emit func(Progress), log io.Writer) error {
	const maxKeyBytes = 1 << 20
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 30 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout: 30 * time.Second, MaxIdleConns: 4, MaxIdleConnsPerHost: 2,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	args := []string{"--root", filepath.Join(private, "root"), "--import"}
	for i, address := range keys {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return &Failure{Code: "GPG_KEY_DOWNLOAD", Message: "GPG 公钥地址无效"}
		}
		res, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return httpNetworkError(err, "", "GPG_KEY_DOWNLOAD", "GPG 公钥下载")
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return &Failure{Code: "GPG_KEY_DOWNLOAD", Message: fmt.Sprintf("GPG 公钥下载 HTTP %d", res.StatusCode), Retryable: transientHTTP(res.StatusCode)}
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, maxKeyBytes+1))
		res.Body.Close()
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
	return command(ctx, "rpmkeys", args, emit, log)
}
func AsFailure(err error) *Failure {
	if e, ok := errors.AsType[*Failure](err); ok {
		return e
	}
	return &Failure{Code: "LOCAL_ERROR", Message: err.Error()}
}

func rsyncPermanentLine(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "auth failed") || strings.Contains(lower, "unknown module") || strings.Contains(lower, "access denied") || (strings.Contains(lower, "@error:") && strings.Contains(lower, "permission denied"))
}
