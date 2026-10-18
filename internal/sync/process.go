package syncer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

var progressLine = regexp.MustCompile(`([\d,]+)\s+(\d+)%\s+([\d.]+)([kKMGT]?)B/s\s+([\d:]+)`)
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func CleanLog(line string) string {
	line = ansi.ReplaceAllString(line, "")
	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, line)
}

// ParseProgress reads one rsync --info=progress2 line, for example
// "1,234,567  42%  1.50MB/s  0:01:02 (xfr#3, to-chk=10/20)".
func ParseProgress(line string) (Progress, bool) {
	match := progressLine.FindStringSubmatch(line)
	if match == nil {
		return Progress{}, false
	}
	bytesText, percentText, speedText, unitText, timeText := match[1], match[2], match[3], match[4], match[5]
	transferred, err := strconv.ParseInt(strings.ReplaceAll(bytesText, ",", ""), 10, 64)
	if err != nil {
		return Progress{}, false
	}
	percent, err := strconv.ParseFloat(percentText, 64)
	if err != nil || percent > 100 {
		return Progress{}, false
	}
	speed, err := strconv.ParseFloat(speedText, 64)
	if err != nil {
		return Progress{}, false
	}
	unit := strings.Index("KMGT", strings.ToUpper(unitText))
	if unitText != "" && unit >= 0 {
		for range unit + 1 {
			speed *= 1024
		}
	}
	remaining := float64(0)
	for part := range strings.SplitSeq(timeText, ":") {
		value, _ := strconv.ParseFloat(part, 64)
		remaining = remaining*60 + value
	}
	total := int64(0)
	if percent > 0 {
		estimate := float64(transferred) * 100 / percent
		if estimate < float64(math.MaxInt64) {
			total = int64(estimate)
		}
	}
	if math.IsInf(speed, 0) || math.IsInf(remaining, 0) {
		return Progress{}, false
	}
	p := Progress{
		Phase:   "transfer",
		Bytes:   transferred,
		Total:   total,
		Percent: new(percent),
		Speed:   speed,
		ETA:     new(remaining),
	}
	// File-completion lines report elapsed time, not ETA.
	if strings.Contains(line, "-chk=") {
		p.ETA = nil
	}
	if strings.Contains(line, "ir-chk=") {
		incrementalProgress(&p)
	}
	return p, true
}

// incrementalPercentCap bounds the percent reported while incremental
// recursion is still growing the file list: rsync divides by the part of the
// list known so far, so its percent can read 100 early and fall back later.
const incrementalPercentCap = 99

// incrementalProgress keeps rsync's approximate percent below completion and
// drops the total and ETA, which cover only part of the file list.
func incrementalProgress(p *Progress) {
	p.Total, p.ETA = 0, nil
	if p.Percent != nil {
		p.Percent = new(min(*p.Percent, incrementalPercentCap))
	}
}

func splitCR(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

type processOutput struct {
	mu            sync.Mutex
	log           io.Writer
	last          string
	emit          func(Progress)
	name          string
	upstreamError string
	networkError  string
	localError    string
	err           error
	cancel        context.CancelCauseFunc
	progress      Progress
	incremental   bool
	samples       []progressSample
	now           func() time.Time
}

// progressSample records processed bytes at one point in time; samples are
// taken at most once per second and cover the last etaWindow.
type progressSample struct {
	at    time.Time
	bytes int64
}

// rsync derives its ETA from roughly the last five seconds of throughput, so
// the estimate swings with every burst. Throughput over etaWindow steadies it;
// no estimate is shown until etaMinimum of history exists.
const (
	etaWindow  = 2 * time.Minute
	etaMinimum = 30 * time.Second
)

func (o *processOutput) clock() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

// sample adds bytes to the throughput history and drops samples no longer
// needed: the oldest kept sample is the newest one at least etaWindow old.
func (o *processOutput) sample(bytes int64) {
	now := o.clock()
	if n := len(o.samples); n > 0 && bytes < o.samples[n-1].bytes {
		o.samples = o.samples[:0]
	}
	if n := len(o.samples); n == 0 || now.Sub(o.samples[n-1].at) >= time.Second {
		o.samples = append(o.samples, progressSample{at: now, bytes: bytes})
	}
	drop := 0
	for drop+1 < len(o.samples) && now.Sub(o.samples[drop+1].at) >= etaWindow {
		drop++
	}
	o.samples = slices.Delete(o.samples, 0, drop)
}

// smoothETA divides the remaining bytes by the throughput across the sample
// window. It returns nil until the window is long enough or while no bytes
// moved, rather than an estimate built on a few seconds of data.
func (o *processOutput) smoothETA(bytes int64, remaining float64) *float64 {
	if len(o.samples) == 0 || remaining <= 0 {
		return nil
	}
	first := o.samples[0]
	elapsed := o.clock().Sub(first.at)
	if elapsed < etaMinimum || bytes <= first.bytes {
		return nil
	}
	rate := float64(bytes-first.bytes) / elapsed.Seconds()
	return new(remaining / rate)
}

func (o *processOutput) scan(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Split(splitCR)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := CleanLog(scanner.Text())
		o.mu.Lock()
		if _, err := fmt.Fprintln(o.log, line); err != nil {
			o.err = err
			o.cancel(err)
		}
		dnf, rsync := o.name == "dnf", o.name == "rsync"
		if localFailureLine(line) || (dnf && strings.Contains(strings.ToLower(line), "permission denied")) {
			o.localError = line
		}
		if (rsync && rsyncPermanentLine(line)) || (dnf && reposyncPermanentLine(line)) {
			o.upstreamError = line
		}
		if dnf && reposyncNetworkLine(line) {
			o.networkError = line
		}
		if line != "" {
			o.last = line
		}
		if p, ok := ParseProgress(line); ok {
			if strings.Contains(line, "ir-chk=") {
				o.incremental = true
			} else if strings.Contains(line, "to-chk=") {
				o.incremental = false
			}
			// Throughput is sampled during incremental recursion too, so an
			// estimate is available as soon as the file list is complete.
			o.sample(p.Bytes)
			switch {
			case o.incremental:
				incrementalProgress(&p)
			case p.ETA == nil:
				p.ETA = o.progress.ETA
			default:
				// rsync's ETA is remaining bytes over its own rate; recover the
				// remaining bytes from it, as the integer percent is too coarse.
				remaining := *p.ETA * p.Speed
				if remaining <= 0 && p.Total > p.Bytes {
					remaining = float64(p.Total - p.Bytes)
				}
				p.ETA = o.smoothETA(p.Bytes, remaining)
			}
			p.ReceivedBytes = o.progress.ReceivedBytes
			p.Files = o.progress.Files
			p.Success = o.progress.Success
			o.progress = p
			o.emit(o.progress)
		} else if received, ok := statNumber(line, "Total bytes received:"); ok && rsync {
			o.progress.ReceivedBytes = new(received)
			o.emit(o.progress)
		} else if files, ok := statNumber(line, "Number of files:"); ok {
			o.progress.Files = int(files)
			o.emit(o.progress)
		} else if transferred, ok := statNumber(line, "Number of regular files transferred:"); ok {
			o.progress.Success = int(transferred)
			o.emit(o.progress)
		}
		o.mu.Unlock()
	}
	if err := scanner.Err(); err != nil {
		o.mu.Lock()
		o.err = err
		o.cancel(err)
		o.mu.Unlock()
	}
}

func statNumber(line, prefix string) (int64, bool) {
	value, ok := strings.CutPrefix(line, prefix)
	if !ok {
		return 0, false
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0, false
	}
	number, err := strconv.ParseInt(strings.ReplaceAll(fields[0], ",", ""), 10, 64)
	return number, err == nil && number >= 0
}

// command runs an executor in its own process group with output parsed for
// progress and failure classification. Cancellation sends TERM to the group,
// then KILL after a grace period.
func command(ctx context.Context, name string, args []string, emit func(Progress), log io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent := ctx
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	cmd := exec.Command(name, args...)
	// Progress and diagnostics are parsed in a stable locale.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	configureProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return &Failure{Code: "DEPENDENCY_MISSING", Message: err.Error()}
	}
	lowerPriority(cmd.Process.Pid)
	output := &processOutput{log: log, emit: emit, cancel: cancel, name: name}
	var readers sync.WaitGroup
	readers.Go(func() { output.scan(stdout) })
	readers.Go(func() { output.scan(stderr) })
	exited := make(chan struct{})
	stopOnCancel := context.AfterFunc(ctx, func() {
		stopProcess(cmd.Process.Pid, false)
		select {
		case <-exited:
			return
		case <-time.After(terminationGrace):
			stopProcess(cmd.Process.Pid, true)
		}
	})
	readers.Wait()
	err = cmd.Wait()
	close(exited)
	stopOnCancel()
	// Descendants that outlive the direct child, such as an rsync receiver,
	// share its process group; never leave them writing into the work tree.
	stopProcess(cmd.Process.Pid, true)
	if parent.Err() != nil {
		return parent.Err()
	}
	if output.err != nil {
		return &Failure{Code: "OUTPUT_FAILED", Message: output.err.Error()}
	}
	if err == nil {
		return nil
	}
	code := -1
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exit.ExitCode()
	}
	switch {
	case output.localError != "":
		return &Failure{Code: "LOCAL_TRANSFER", Message: output.localError, ExitCode: code}
	case output.upstreamError != "":
		return &Failure{Code: "UPSTREAM_ERROR", Message: output.upstreamError, SourceFailure: true, ExitCode: code}
	case name == "dnf":
		return &Failure{Code: "REPOSYNC_FAILED", Message: output.last, Retryable: output.networkError != "", ExitCode: code}
	default:
		return Classify(code, output.last)
	}
}

// terminationGrace is how long a process group may handle TERM before KILL.
const terminationGrace = 10 * time.Second

// DNF exits with a generic status; retain transient curl diagnostics even when
// the final summary only says that all mirrors were tried.
func reposyncNetworkLine(line string) bool {
	lower := strings.ToLower(line)
	for _, code := range []string{"5", "6", "7", "18", "28", "52", "55", "56", "92"} {
		if strings.Contains(lower, "curl error ("+code+")") {
			return true
		}
	}
	for _, status := range []string{"408", "429", "500", "502", "503", "504"} {
		if strings.Contains(lower, "status code: "+status) {
			return true
		}
	}
	return false
}

// reposyncPermanentDiagnostics are DNF messages that a retry cannot fix:
// local storage errors, signature failures, malformed URLs, TLS trust
// errors and authorization or not-found responses.
var reposyncPermanentDiagnostics = []string{
	"no space left",
	"read-only file system",
	"permission denied",
	"gpg check failed",
	"gpg signature",
	"signature verification failed",
	"public key for",
	"curl error (3)",
	"curl error (60)",
	"curl error (77)",
	"status code: 401",
	"status code: 403",
	"status code: 404",
	"status code: 407",
}

func reposyncPermanentLine(line string) bool {
	lower := strings.ToLower(line)
	for _, text := range reposyncPermanentDiagnostics {
		if strings.Contains(lower, text) {
			return true
		}
	}
	return false
}
