//go:build darwin

package mirrors

import (
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func cloneFile(src *os.File, target string) error {
	return unix.Fclonefileat(int(src.Fd()), unix.AT_FDCWD, target, unix.CLONE_NOOWNERCOPY)
}

func fileIdentity(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmtIdentity(uint64(st.Dev), st.Ino, st.Ctimespec.Sec, st.Ctimespec.Nsec)
}

func fileOwnership(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmtIdentity(uint64(st.Uid), uint64(st.Gid), 0, 0)
}

func hardlinkIdentity(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	if st.Nlink < 2 {
		return ""
	}
	return fmtIdentity(uint64(st.Dev), st.Ino, 0, 0)
}
