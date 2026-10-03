package mirrors

import "golang.org/x/sys/unix"

// DescriptorLimits partitions the same process limit between accepted TCP
// connections, file-serving requests (up to three transient descriptors each),
// and a reserve for SQLite, workers, logs and publication operations.
func DescriptorLimits() (files, connections int) {
	var r unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_NOFILE, &r) != nil {
		return 32, 128
	}
	reserve := min(uint64(256), r.Cur/2)
	available := r.Cur - reserve
	return int(min(uint64(328), max(uint64(1), available/6))), int(min(uint64(4096), max(uint64(1), available/2)))
}
