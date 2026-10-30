package syncer

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Process inventories and argv are bounded independently of host process count.
const processPageSize = 256
const maxCommandLineBytes = 8 << 20

// findStrays scans /proc for processes of the current user whose arguments
// reference a task work directory. Disappearing processes are expected.
func findStrays(ctx context.Context, workRoot string) ([]int, error) {
	directory, err := os.Open("/proc")
	if err != nil {
		return nil, err
	}
	defer directory.Close() // Read-only directory; no buffered writes.
	self, uid := os.Getpid(), uint32(os.Getuid())
	found := map[int]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, readErr := directory.ReadDir(processPageSize)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, readErr
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			pid, err := strconv.Atoi(entry.Name())
			if err != nil || pid == self {
				continue
			}
			dir := "/proc/" + entry.Name()
			if info, err := os.Stat(dir); err != nil || info.Sys().(*syscall.Stat_t).Uid != uid {
				continue
			}
			argv, err := readCommandLine(dir + "/cmdline")
			if err != nil || len(argv) == 0 {
				continue
			} // Exited processes and kernel threads have no argv.
			if referencesWork(workRoot, strings.Split(strings.TrimRight(string(argv), "\x00"), "\x00")) {
				found[pid] = true
			}
		}
		if errors.Is(readErr, io.EOF) {
			return sortedPIDs(found), nil
		}
	}
}

func readCommandLine(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close() // Read-only proc file, not a persistent output.
	content, err := io.ReadAll(io.LimitReader(file, maxCommandLineBytes+1))
	if len(content) > maxCommandLineBytes {
		return nil, errors.New("process command line exceeds limit")
	}
	return content, err
}
