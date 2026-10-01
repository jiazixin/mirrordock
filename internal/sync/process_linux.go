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
func lowerPriority(pid int) {
	_ = unix.Setpriority(unix.PRIO_PROCESS, pid, 10)
	_, _, _ = unix.Syscall(unix.SYS_IOPRIO_SET, 1, uintptr(pid), (2<<13)|7)
}
