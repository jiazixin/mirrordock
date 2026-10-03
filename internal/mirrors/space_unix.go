//go:build linux || darwin

package mirrors

import "golang.org/x/sys/unix"

func diskSpace(dir string) (DiskSpace, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return DiskSpace{}, err
	}
	return DiskSpace{Total: uint64(st.Blocks) * uint64(st.Bsize), Available: uint64(st.Bavail) * uint64(st.Bsize)}, nil
}
