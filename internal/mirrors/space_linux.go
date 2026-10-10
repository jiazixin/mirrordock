package mirrors

import "golang.org/x/sys/unix"

func diskSpace(dir string) (DiskSpace, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return DiskSpace{}, err
	}
	unit := st.Frsize
	if unit <= 0 {
		unit = st.Bsize
	}
	return DiskSpace{Total: uint64(st.Blocks) * uint64(unit), Available: uint64(st.Bavail) * uint64(unit)}, nil
}
