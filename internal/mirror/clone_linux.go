package mirror

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func cloneFile(src *os.File, target string) error {
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = errors.Join(unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())), dst.Close())
	if err != nil {
		err = errors.Join(err, os.Remove(target))
	}
	return err
}

func fileIdentity(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmtIdentity(fmtIdentityOptions{dev: uint64(st.Dev), ino: st.Ino, sec: st.Ctim.Sec, nsec: st.Ctim.Nsec})
}

func fileOwnership(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmtIdentity(fmtIdentityOptions{dev: uint64(st.Uid), ino: uint64(st.Gid)})
}

func hardlinkIdentity(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	if st.Nlink < 2 {
		return ""
	}
	return fmtIdentity(fmtIdentityOptions{dev: uint64(st.Dev), ino: st.Ino})
}
