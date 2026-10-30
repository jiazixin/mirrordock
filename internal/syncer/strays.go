package syncer

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// TerminateStrays stops executor processes left behind by a previous service
// process that was killed before it could stop their process groups. Pdeathsig
// reaches only the direct child: rsync's forked receiver survives and keeps
// writing into a task work directory. Callers must hold the instance lock, so
// any process of this user still referencing workRoot belongs to an earlier
// instance. Processes of other users are never considered.
func TerminateStrays(ctx context.Context, workRoot string) (int, error) {
	killed := map[int]bool{}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		pids, err := findStrays(ctx, workRoot)
		if err != nil {
			return len(killed), err
		}
		if len(pids) == 0 {
			return len(killed), nil
		}
		if ctx.Err() != nil {
			return len(killed), fmt.Errorf("leftover executor processes %v: %w", pids, ctx.Err())
		}
		for _, pid := range pids {
			if syscall.Kill(pid, syscall.SIGKILL) == nil {
				killed[pid] = true
			}
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return len(killed), ctx.Err()
		}
	}
}

// referencesWork matches executor arguments such as rsync's destination,
// --download-path and --config, which all embed a task work directory. A
// forked rsync receiver keeps its parent's arguments. Working directories are
// deliberately not matched: an administrator's shell may sit in a work tree,
// and an rsync daemon may serve a published generation.
func referencesWork(workRoot string, args []string) bool {
	prefix := filepath.Clean(workRoot) + string(filepath.Separator)
	return slices.ContainsFunc(args, func(arg string) bool { return strings.Contains(arg, prefix) })
}

func sortedPIDs(set map[int]bool) []int { return slices.Sorted(maps.Keys(set)) }
