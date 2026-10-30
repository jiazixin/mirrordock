package mirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrLowSpace reports that the mirror disk is below its free-space reserve.
var ErrLowSpace = errors.New("mirror disk below its free-space reserve")

const workerSpace = uint64(512 << 20)

// ReflinkCheck is the result of ProbeReflink: Status is supported,
// unsupported or error; Detail is the English cause of a failed probe.
type ReflinkCheck struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// DiskSpace describes the capacity of the mirror disk.
type DiskSpace struct {
	Total           uint64 `json:"total_bytes"`
	Available       uint64 `json:"available_bytes"`
	Reserve         uint64 `json:"reserve_bytes"`
	Required        uint64 `json:"required_bytes"`
	Workers         int    `json:"workers"`
	Previous        int    `json:"previous"`
	WorkDirectories int    `json:"work_directories"`
	Error           string `json:"error,omitzero"`
}

// ProbeReflink checks whether the mirror storage supports reflink clones. The
// probe uses the same source and destination filesystems as real preparation.
// Only private, newly created probe directories are removed.
func (m *Manager) ProbeReflink(ctx context.Context) ReflinkCheck {
	m.probeMu.Lock()
	defer m.probeMu.Unlock()
	result := ReflinkCheck{Status: "error"}
	probe := func() (resultErr error) {
		if err := ctx.Err(); err != nil {
			return err
		}
		source, err := os.MkdirTemp(filepath.Join(m.Root, ".mirrordock", "published"), ".reflink-probe-")
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(source)) }()
		dest, err := os.MkdirTemp(filepath.Join(m.Root, ".mirrordock", "work"), ".reflink-probe-")
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(dest)) }()
		f, err := os.OpenFile(filepath.Join(source, "source"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
		if _, err := f.Write(make([]byte, 4096)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return m.clone(f, filepath.Join(dest, "clone"))
	}
	if err := probe(); err != nil {
		if cloneUnsupported(err) {
			result.Status = "unsupported"
		}
		result.Detail = err.Error()
	} else {
		result.Status = "supported"
	}
	return result
}

// cloneUnsupported recognizes errors of a filesystem without reflink, as
// opposed to a failed probe.
func cloneUnsupported(err error) bool {
	for _, unsupported := range []error{errors.ErrUnsupported, syscall.EOPNOTSUPP, syscall.ENOTTY, syscall.EXDEV} {
		if errors.Is(err, unsupported) {
			return true
		}
	}
	return false
}

// safetyReserve is the free space kept on the mirror filesystem: 1% of its
// size, at least 1 GiB.
func safetyReserve(space DiskSpace) uint64 { return max(uint64(1<<30), space.Total/100) }

// StorageInfo returns the current capacity of the mirror disk.
func (m *Manager) StorageInfo(ctx context.Context) DiskSpace {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	space, err := m.space(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil {
		space.Error = err.Error()
		return space
	}
	space.Reserve = safetyReserve(space)
	m.storageMu.Lock()
	space.Workers = len(m.syncing)
	m.storageMu.Unlock()
	space.Required = space.Reserve + workerSpace*uint64(space.Workers+1)
	for _, p := range m.Publications() {
		space.Previous += len(p.Previous)
	}
	dir, err := os.Open(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil {
		space.Error = err.Error()
		return space
	}
	defer dir.Close() // Read-only directory descriptor.
	for {
		if err := ctx.Err(); err != nil {
			space.Error = err.Error()
			return space
		}
		entries, err := dir.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			space.Error = err.Error()
			return space
		}
		for _, entry := range entries {
			if id, err := strconv.ParseInt(entry.Name(), 10, 64); err == nil && id > 0 && entry.IsDir() {
				space.WorkDirectories++
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return space
}

// BeginSync admits sync id against the free-space margin that workers reserve
// together. This is an early-stop policy, not a filesystem quota: upstream
// growth and other writers remain unbounded.
func (m *Manager) BeginSync(id int64) error {
	m.storageMu.Lock()
	defer m.storageMu.Unlock()
	short, err := m.admissionShortLocked()
	if err != nil {
		return err
	}
	if short {
		return ErrLowSpace
	}
	m.syncing[id] = true
	return nil
}

// admissionShort reports whether one more worker would be refused admission.
func (m *Manager) admissionShort() (bool, error) {
	m.storageMu.Lock()
	defer m.storageMu.Unlock()
	return m.admissionShortLocked()
}

func (m *Manager) admissionShortLocked() (bool, error) {
	space, err := m.space(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil {
		return false, fmt.Errorf("read mirror disk capacity: %w", err)
	}
	required := safetyReserve(space) + workerSpace*uint64(len(m.syncing)+1)
	return space.Available < required, nil
}

// EndSync releases the admission taken by BeginSync for run id.
func (m *Manager) EndSync(id int64) {
	m.storageMu.Lock()
	delete(m.syncing, id)
	m.storageMu.Unlock()
}

// CheckSpace returns ErrLowSpace when free space is below the safety reserve.
func (m *Manager) CheckSpace() error {
	space, err := m.space(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil {
		return fmt.Errorf("read mirror disk capacity: %w", err)
	}
	if space.Available < safetyReserve(space) {
		return ErrLowSpace
	}
	return nil
}

// RemoveWork removes the work directory of run id if it exists.
func (m *Manager) RemoveWork(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("invalid run ID")
	}
	dir := m.Work(id)
	st, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return errors.New("work directory is not a regular directory")
	}
	return removeGeneration(ctx, dir)
}

func fmtIdentity(params fmtIdentityOptions) string {
	dev := params.dev
	ino := params.ino
	sec := params.sec
	nsec := params.nsec

	return fmt.Sprintf("%d:%d:%d:%d", dev, ino, sec, nsec)
}

// Only called during startup before probes and task execution. These reserved
// prefixes are generated exclusively by MirrorDock; unknown work stays intact.
func (m *Manager) cleanupStaleTemporary(ctx context.Context) error {
	for _, area := range []string{"work", "published"} {
		dir := filepath.Join(m.Root, ".mirrordock", area)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			probe := strings.HasPrefix(entry.Name(), ".reflink-probe-")
			discarded := area == "work" && strings.HasPrefix(entry.Name(), discardPrefix)
			if entry.IsDir() && (probe || discarded) {
				if err := removeGeneration(ctx, filepath.Join(dir, entry.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// fmtIdentityOptions groups the inputs to fmtIdentity.
type fmtIdentityOptions struct {
	dev  uint64
	ino  uint64
	sec  int64
	nsec int64
}
