package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mirrordock/internal/config"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Progress struct {
	Phase   string   `json:"phase"`
	Bytes   int64    `json:"bytes"`
	Total   int64    `json:"total"`
	Percent *float64 `json:"percent"`
	Speed   float64  `json:"speed"`
	ETA     *float64 `json:"eta_seconds"`
	Files   int      `json:"files"`
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
	Skipped int      `json:"skipped"`
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
	case 5, 10, 12, 30, 35:
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
func AsFailure(err error) *Failure {
	if e, ok := errors.AsType[*Failure](err); ok {
		return e
	}
	return &Failure{Code: "LOCAL_ERROR", Message: err.Error()}
}
