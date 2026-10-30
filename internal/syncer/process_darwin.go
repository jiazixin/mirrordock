package syncer

import (
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopProcess(pid int, force bool) {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, sig) // The process may already have exited; Wait determines its result.
}

func lowerPriority(pid int) {
	// Priority is a best-effort optimization; sandbox or permission failure must not cancel a sync.
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, 10)
}
