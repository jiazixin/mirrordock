package syncer

import (
	"golang.org/x/sys/unix"
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func stopProcess(pid int, force bool) {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, sig)
}

// I/O priority encoding of ioprio_set(2).
const (
	ioprioWhoProcess = 1
	ioprioClassShift = 13
	ioprioClassBest  = 2
	ioprioLowest     = 7
)

// lowerPriority runs executors at nice 10 and the lowest best-effort I/O
// priority, so synchronization yields to public downloads.
func lowerPriority(pid int) {
	_ = unix.Setpriority(unix.PRIO_PROCESS, pid, 10)
	priority := ioprioClassBest<<ioprioClassShift | ioprioLowest
	_, _, _ = unix.Syscall(unix.SYS_IOPRIO_SET, ioprioWhoProcess, uintptr(pid), uintptr(priority))
}
