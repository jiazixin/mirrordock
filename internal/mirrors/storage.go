package mirrors

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

var ErrLowSpace = errors.New("镜像盘可用空间不足，等待释放空间")

const workerSpace = uint64(512 << 20)

type ReflinkCheck struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type DiskSpace struct {
	Total           uint64 `json:"total"`
	Available       uint64 `json:"available"`
	Reserve         uint64 `json:"reserve"`
	Required        uint64 `json:"required"`
	Workers         int    `json:"workers"`
	Previous        int    `json:"previous"`
	WorkDirectories int    `json:"work_directories"`
	Error           string `json:"error,omitempty"`
}

// Probe uses the same source and destination filesystems as real preparation.
// Only private, newly created probe directories are removed.
func (m *Manager) ProbeReflink(ctx context.Context) ReflinkCheck {
	m.probeMu.Lock()
	defer m.probeMu.Unlock()
	result := ReflinkCheck{Status: "error"}
	probe := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		source, err := os.MkdirTemp(filepath.Join(m.Root, ".mirrordock", "published"), ".reflink-probe-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(source)
		dest, err := os.MkdirTemp(filepath.Join(m.Root, ".mirrordock", "work"), ".reflink-probe-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dest)
		f, err := os.OpenFile(filepath.Join(source, "source"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(make([]byte, 4096)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return m.clone(f, filepath.Join(dest, "clone"))
	}
	if err := probe(); err != nil {
		if errors.Is(err, errors.ErrUnsupported) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EXDEV) {
			result.Status = "unsupported"
		}
		result.Detail = "镜像目录克隆未成功：" + err.Error()
	} else {
		result.Status = "supported"
		result.Detail = "镜像目录实测支持写时复制；未变化文件共享数据块，克隆失败将停止任务"
	}
	return result
}

func (m *Manager) StorageInfo() DiskSpace {
	space, err := m.space(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil {
		space.Error = err.Error()
		return space
	}
	space.Reserve = max(uint64(1<<30), space.Total/100)
	m.storageMu.Lock()
	space.Workers = len(m.syncing)
	m.storageMu.Unlock()
	space.Required = space.Reserve + workerSpace*uint64(space.Workers+1)
	for _, p := range m.Publications() {
		space.Previous += len(p.Previous)
	}
	entries, err := os.ReadDir(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil {
		space.Error = err.Error()
		return space
	}
	for _, entry := range entries {
		if id, err := strconv.ParseInt(entry.Name(), 10, 64); err == nil && id > 0 && entry.IsDir() {
			space.WorkDirectories++
		}
	}
	return space
}

// Workers reserve a shared admission margin. This is an early-stop policy,
// not a filesystem quota: upstream growth and other writers remain unbounded.
func (m *Manager) BeginSync(id int64) error {
	m.storageMu.Lock()
	defer m.storageMu.Unlock()
	space, err := m.space(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil {
		return fmt.Errorf("读取镜像盘容量：%w", err)
	}
	reserve := max(uint64(1<<30), space.Total/100)
	if space.Available < reserve+workerSpace*uint64(len(m.syncing)+1) {
		return ErrLowSpace
	}
	m.syncing[id] = true
	return nil
}

func (m *Manager) EndSync(id int64) {
	m.storageMu.Lock()
	delete(m.syncing, id)
	m.storageMu.Unlock()
}

func (m *Manager) CheckSpace() error {
	space, err := m.space(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil {
		return fmt.Errorf("读取镜像盘容量：%w", err)
	}
	if space.Available < max(uint64(1<<30), space.Total/100) {
		return ErrLowSpace
	}
	return nil
}

func (m *Manager) RemoveWork(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("无效任务编号")
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
		return errors.New("任务目录不是普通目录")
	}
	return removeGeneration(ctx, dir)
}

// CleanupWork consults persisted facts, including resumable interrupted runs.
// Unknown directories are preserved, never guessed to be disposable.
func (m *Manager) CleanupWork(ctx context.Context) error {
	entries, err := os.ReadDir(filepath.Join(m.Root, ".mirrordock", "work"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil || id <= 0 || !entry.IsDir() {
			continue
		}
		state, err := m.store.RunState(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		switch state {
		case "succeeded", "failed", "timed_out", "cancelled":
			if err := m.RemoveWork(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func fmtIdentity(dev, ino uint64, sec, nsec int64) string {
	return fmt.Sprintf("%d:%d:%d:%d", dev, ino, sec, nsec)
}
