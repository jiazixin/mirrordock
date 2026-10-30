package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mirrordock/internal/config"
)

// Progress reports transfer progress of a running task.
type Progress struct {
	ReceivedBytes      *int64   `json:"received_bytes,omitzero"`
	ReceivedIncomplete bool     `json:"received_incomplete,omitzero"`
	Phase              string   `json:"phase"`
	Bytes              int64    `json:"transferred_bytes"`
	Total              int64    `json:"total_bytes"`
	Percent            *float64 `json:"percent,omitzero"`
	Speed              float64  `json:"speed_bytes_per_second"`
	ETA                *int64   `json:"eta_ms,omitzero"`
	Files              int      `json:"files"`
	Success            int      `json:"success"`
	Failed             int      `json:"failed"`
	Skipped            int      `json:"skipped"`
}

// Task is one sync to run for a source.
type Task struct {
	Source    config.Source
	Settings  config.Settings
	Directory string
	Candidate string
	Token     string
	// Proxy is the rsync proxy URL resolved at execution; blank is direct.
	Proxy string
}

// Failure is a classified sync failure with an error code. Message is English;
// Details carries the values clients need to show their own text, with raw
// tool or upstream output in "detail".
type Failure struct {
	Code          string
	Message       string
	Details       map[string]any
	Retryable     bool
	SourceFailure bool
	// ProxyFailure means the proxy itself failed, so the other candidates,
	// which use the same proxy, are not tried in this round.
	ProxyFailure bool
	RetryAt      time.Time
	ExitCode     int
}

// Error implements error.
func (e *Failure) Error() string { return e.Code + ": " + e.Message }

// Runner runs sync tasks.
type Runner interface {
	Run(context.Context, Task, func(Progress), io.Writer) error
}

// SystemRunner runs tasks with the system rsync and dnf tools and the GitHub
// API.
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
			fmt.Fprintf(log, "connecting to the upstream through %s proxy %s\n", proxy.Scheme, proxyAddress(proxy))
		}
		args := RsyncArgs(t)
		fmt.Fprintf(log, "rsync %s\n", strings.Join(args, " "))
		return command(ctx, commandOptions{name: "rsync", args: args, env: env, emit: emit, log: log})
	case "reposync":
		return reposync(ctx, t, emit, log)
	case "github_release":
		return GitHub(ctx, t, emit, log)
	default:
		return &Failure{Code: "invalid_type", Message: "unsupported source type " + t.Source.Type, Details: map[string]any{"type": t.Source.Type}}
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
	failure := &Failure{Code: "upstream_error", Message: message, Details: rawDetail(message), ExitCode: code, SourceFailure: true}
	switch code {
	case 2, 3: // protocol incompatibility, file selection error
	case 5: // error starting the client-server protocol
		permanent := rsyncPermanentLine(message) || strings.Contains(strings.ToLower(message), "permission denied")
		failure.Retryable = !permanent
	case 10, 12, 30, 35: // socket I/O, protocol stream, timeouts
		failure.Retryable = true
	case 23, 24: // partial transfer, vanished source files
		local := localFailureLine(message)
		failure.Code = "partial_transfer"
		failure.Retryable = !local
		failure.SourceFailure = !local
	default:
		failure.Code = "process_failed"
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
	if err := os.MkdirAll(t.Directory, 0o755); err != nil {
		return err
	}
	private := filepath.Join(t.Directory, ".mirrordock-dnf")
	// A previous process may have stopped before its deferred cleanup.
	if err := os.RemoveAll(private); err != nil {
		return err
	}
	err := os.Mkdir(private, 0o700)
	if err != nil {
		return err
	}
	defer os.RemoveAll(private)
	if config.DefaultBool(cfg.GPGCheck) {
		if err := importRepoKeys(ctx, importRepoKeysOptions{private: private, keys: cfg.GPGKeys, emit: emit, log: log}); err != nil {
			return err
		}
	}
	repoDir := filepath.Join(private, "repos")
	if err = os.Mkdir(repoDir, 0o700); err != nil {
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
	if err = os.WriteFile(filepath.Join(repoDir, "source.repo"), []byte(repository), 0o600); err != nil {
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
	if err = os.WriteFile(conf, []byte(mainSection), 0o600); err != nil {
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
	return command(ctx, commandOptions{name: "dnf", args: args, env: processEnv(), emit: emit, log: log})
}

// Reposync verifies against the installroot RPM database but does not import
// gpgkey URLs itself. Keep key downloads bounded and the keyring task-private.
func importRepoKeys(ctx context.Context, params importRepoKeysOptions) error {
	private := params.private
	keys := params.keys
	emit := params.emit
	log := params.log

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
			return &Failure{Code: "gpg_key_download", Message: "invalid GPG key URL", Details: map[string]any{"reason": "invalid_url"}}
		}
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return httpNetworkError(httpNetworkErrorOptions{err: err, proxy: "", code: "gpg_key_download", operation: "gpg_key_download"})
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return &Failure{
				Code:      "gpg_key_download",
				Message:   fmt.Sprintf("GPG key download returned HTTP %d", response.StatusCode),
				Details:   map[string]any{"operation": "gpg_key_download", "http_status": response.StatusCode},
				Retryable: transientHTTP(response.StatusCode),
			}
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, maxKeyBytes+1))
		response.Body.Close()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return httpNetworkError(httpNetworkErrorOptions{err: err, proxy: "", code: "gpg_key_download", operation: "gpg_key_read"})
		}
		if len(data) == 0 || len(data) > maxKeyBytes {
			return &Failure{Code: "gpg_key_download", Message: "GPG key must be non-empty and at most 1 MiB", Details: map[string]any{"reason": "invalid_size"}}
		}
		file := filepath.Join(private, fmt.Sprintf("key-%d.asc", i))
		if err := os.WriteFile(file, data, 0o600); err != nil {
			return err
		}
		args = append(args, file)
	}
	if _, err := fmt.Fprintln(log, "rpmkeys: importing the configured keys into the private RPM database of this run"); err != nil {
		return err
	}
	return command(ctx, commandOptions{name: "rpmkeys", args: args, env: processEnv(), emit: emit, log: log})
}

// AsFailure returns the Failure in the chain of err, or wraps err as a
// local_error failure.
func AsFailure(err error) *Failure {
	if failure, ok := errors.AsType[*Failure](err); ok {
		return failure
	}
	return &Failure{Code: "local_error", Message: err.Error(), Details: rawDetail(err.Error())}
}

// rawDetail keeps raw tool or upstream output for clients to show after their
// own text.
func rawDetail(text string) map[string]any {
	if text == "" {
		return nil
	}
	return map[string]any{"detail": text}
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

// importRepoKeysOptions groups the inputs to importRepoKeys.
type importRepoKeysOptions struct {
	private string
	keys    []string
	emit    func(Progress)
	log     io.Writer
}
