package mirror

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func cloneFile(src *os.File, target string) error {
	return unix.Fclonefileat(int(src.Fd()), unix.AT_FDCWD, target, unix.CLONE_NOOWNERCOPY)
}

func fileIdentity(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmtIdentity(fmtIdentityOptions{dev: uint64(st.Dev), ino: st.Ino, sec: st.Ctimespec.Sec, nsec: st.Ctimespec.Nsec})
}

func fileOwnership(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmtIdentity(fmtIdentityOptions{dev: uint64(st.Uid), ino: uint64(st.Gid), sec: 0, nsec: 0})
}

func hardlinkIdentity(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	if st.Nlink < 2 {
		return ""
	}
	return fmtIdentity(fmtIdentityOptions{dev: uint64(st.Dev), ino: st.Ino, sec: 0, nsec: 0})
}
