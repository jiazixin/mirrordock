package syncer

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// findStrays scans /proc for processes of the current user whose arguments
// reference a task work directory.
func findStrays(workRoot string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	self, uid := os.Getpid(), uint32(os.Getuid())
	found := map[int]bool{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		dir := "/proc/" + entry.Name()
		if info, err := os.Stat(dir); err != nil || info.Sys().(*syscall.Stat_t).Uid != uid {
			continue
		}
		// Exited processes, zombies and kernel threads have no arguments.
		cmdline, err := os.ReadFile(dir + "/cmdline")
		if err != nil || len(cmdline) == 0 {
			continue
		}
		if referencesWork(workRoot, strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")) {
			found[pid] = true
		}
	}
	return sortedPIDs(found), nil
}
