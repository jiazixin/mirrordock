package mirror

import (
	"errors"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// preserveOwner gives target the owner and group of the published entry
// described by info. A clone starts with the identity of this process (and,
// on BSD systems, the group of its directory), which differs when a source
// maps ownership with rsync.uid or rsync.gid.
func preserveOwner(target string, info os.FileInfo) error {
	want, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	current, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if got, ok := current.Sys().(*syscall.Stat_t); ok && got.Uid == want.Uid && got.Gid == want.Gid {
		return nil
	}
	err = os.Lchown(target, int(want.Uid), int(want.Gid))
	if errors.Is(err, os.ErrPermission) {
		// The service no longer may assign this owner, for example after it
		// stopped running as root. The synchronizer then decides the owner as
		// on a first run; only the unchanged-publication shortcut is lost.
		return nil
	}
	return err
}

// setLinkTime sets the modification time of the symbolic link itself;
// os.Chtimes would follow it.
func setLinkTime(target string, modified time.Time) error {
	stamp := unix.NsecToTimespec(modified.UnixNano())
	err := unix.UtimesNanoAt(unix.AT_FDCWD, target, []unix.Timespec{stamp, stamp}, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return &os.PathError{Op: "utimensat", Path: target, Err: err}
	}
	return nil
}
