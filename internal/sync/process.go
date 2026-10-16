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
func ParseProgress(line string) (Progress, bool) {
	m := progressLine.FindStringSubmatch(line)
	if m == nil {
		return Progress{}, false
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64)
	if err != nil {
		return Progress{}, false
	}
	percent, err := strconv.ParseFloat(m[2], 64)
	if err != nil || percent > 100 {
		return Progress{}, false
	}
	speed, err := strconv.ParseFloat(m[3], 64)
	if err != nil {
		return Progress{}, false
	}
	unit := strings.Index("KMGT", strings.ToUpper(m[4]))
	if m[4] != "" && unit >= 0 {
		for range unit + 1 {
			speed *= 1024
		}
	}
	parts := strings.Split(m[5], ":")
	remaining := float64(0)
	for _, p := range parts {
		v, _ := strconv.ParseFloat(p, 64)
		remaining = remaining*60 + v
	}
	total := int64(0)
	if percent > 0 {
		value := float64(n) * 100 / percent
		if value < float64(math.MaxInt64) {
			total = int64(value)
		}
	}
	if math.IsInf(speed, 0) || math.IsInf(remaining, 0) {
		return Progress{}, false
	}
	p := Progress{Phase: "transfer", Bytes: n, Total: total, Percent: new(percent), Speed: speed, ETA: new(remaining)}
	if strings.Contains(line, "ir-chk=") {
		p.Phase = "scan"
		p.Total = 0
		p.Percent = nil
		p.ETA = nil
	}
	return p, true
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
}

func (o *processOutput) scan(reader io.Reader) {
	s := bufio.NewScanner(reader)
	s.Split(splitCR)
	s.Buffer(make([]byte, 4096), 1<<20)
	for s.Scan() {
		line := CleanLog(s.Text())
		o.mu.Lock()
		if _, err := fmt.Fprintln(o.log, line); err != nil {
			o.err = err
			o.cancel(err)
		}
		if localFailureLine(line) || (o.name == "dnf" && strings.Contains(strings.ToLower(line), "permission denied")) {
			o.localError = line
		}
		if (o.name == "rsync" && rsyncPermanentLine(line)) || (o.name == "dnf" && reposyncPermanentLine(line)) {
			o.upstreamError = line
		}
		if o.name == "dnf" && reposyncNetworkLine(line) {
			o.networkError = line
		}
		if line != "" {
			o.last = line
		}
		if p, ok := ParseProgress(line); ok {
			p.ReceivedBytes = o.progress.ReceivedBytes
			p.Files = o.progress.Files
			p.Success = o.progress.Success
			o.progress = p
			o.emit(o.progress)
		} else if n, ok := statNumber(line, "Total bytes received:"); ok && o.name == "rsync" {
			o.progress.ReceivedBytes = new(n)
			o.emit(o.progress)
		} else if n, ok := statNumber(line, "Number of files:"); ok {
			o.progress.Files = int(n)
			o.emit(o.progress)
		} else if n, ok := statNumber(line, "Number of regular files transferred:"); ok {
			o.progress.Success = int(n)
			o.emit(o.progress)
		}
		o.mu.Unlock()
	}
	if err := s.Err(); err != nil {
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
	n, err := strconv.ParseInt(strings.ReplaceAll(fields[0], ",", ""), 10, 64)
	return n, err == nil && n >= 0
}
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
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	errout, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return &Failure{Code: "DEPENDENCY_MISSING", Message: err.Error()}
	}
	lowerPriority(cmd.Process.Pid)
	o := &processOutput{log: log, emit: emit, cancel: cancel, name: name}
	var wg sync.WaitGroup
	wg.Go(func() { o.scan(out) })
	wg.Go(func() { o.scan(errout) })
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		stopProcess(cmd.Process.Pid, false)
		select {
		case <-done:
			return
		case <-time.After(10 * time.Second):
			stopProcess(cmd.Process.Pid, true)
		}
	})
	wg.Wait()
	err = cmd.Wait()
	close(done)
	stop()
	// Descendants that outlive the direct child, such as an rsync receiver,
	// share its process group; never leave them writing into the work tree.
	stopProcess(cmd.Process.Pid, true)
	if parent.Err() != nil {
		return parent.Err()
	}
	if o.err != nil {
		return &Failure{Code: "OUTPUT_FAILED", Message: o.err.Error()}
	}
	if err != nil {
		code := -1
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exit.ExitCode()
		}
		if o.localError != "" {
			return &Failure{Code: "LOCAL_TRANSFER", Message: o.localError, ExitCode: code}
		}
		if o.upstreamError != "" {
			return &Failure{Code: "UPSTREAM_ERROR", Message: o.upstreamError, SourceFailure: true, ExitCode: code}
		}
		if name == "dnf" {
			return &Failure{Code: "REPOSYNC_FAILED", Message: o.last, Retryable: o.networkError != "", ExitCode: code}
		}
		return Classify(code, o.last)
	}
	return nil
}

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

func reposyncPermanentLine(line string) bool {
	lower := strings.ToLower(line)
	for _, text := range []string{"no space left", "read-only file system", "permission denied", "gpg check failed", "gpg signature", "signature verification failed", "public key for", "curl error (3)", "curl error (60)", "curl error (77)", "status code: 401", "status code: 403", "status code: 404", "status code: 407"} {
		if strings.Contains(lower, text) {
			return true
		}
	}
	return false
}
