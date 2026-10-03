//go:build linux

package mirrors

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

func cloneFile(src *os.File, target string) error {
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = errors.Join(unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())), dst.Close())
	if err != nil {
		_ = os.Remove(target)
	}
	return err
}

func fileIdentity(info os.FileInfo) string {
	st := info.Sys().(*syscall.Stat_t)
	return fmtIdentity(uint64(st.Dev), st.Ino, st.Ctim.Sec, st.Ctim.Nsec)
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
