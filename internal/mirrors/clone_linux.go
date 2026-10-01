//go:build linux

package mirrors

import (
	"golang.org/x/sys/unix"
	"os"
)

func cloneFile(dst, src *os.File) bool {
	return unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())) == nil
}
