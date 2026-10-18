package mirrors

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"io/fs"
	"mirrordock/internal/config"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Refresh only durable listing facts for an unchanged GitHub body generation.
// Never mutate the immutable published files or summaries held by readers.
func (m *Manager) refreshReleaseSummary(ctx context.Context, s config.Source, work, generation string) error {
	summary, err := summarizeDirectory(ctx, work, s.Type)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.table.Load().entries[s.Dest]
	if old == nil || old.Publication.Current.ID != generation {
		return errors.New("基础发布代已改变")
	}
	p := old.Publication
	p.Current.Summary = summary
	o, err := m.openPublication(p)
	if err != nil {
		return err
	}
	if err := m.store.SavePublicationContext(ctx, s.ID, p); err != nil {
		return err
	}
	m.installOpened(o)
	return nil
}

type ReleaseSummary struct {
	Bytes    int64     `json:"bytes"`
	SyncedAt time.Time `json:"synced_at,omitzero"`
}

type DirectorySummary struct {
	Bytes    int64                     `json:"bytes"`
	Releases map[string]ReleaseSummary `json:"releases,omitempty"`
}

// Summaries belong to the immutable publication, never to a download request.
// Only regular public files count; private markers and symlink targets do not.
func summarizeDirectory(ctx context.Context, dir, kind string) (*DirectorySummary, error) {
	summary := &DirectorySummary{}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if kind == "github_release" {
		summary.Releases = map[string]ReleaseSummary{}
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if hidden(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "." {
			return nil
		}
		tag, _, nested := strings.Cut(filepath.ToSlash(rel), "/")
		if d.IsDir() {
			if summary.Releases != nil && !nested && len(summary.Releases) < 1000 {
				release := ReleaseSummary{}
				markerFlags := os.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW
				marker, err := root.OpenFile(filepath.Join(rel, ".mirrordock-release-sync"), markerFlags, 0)
				if err == nil {
					// Bound marker reads, including malformed or imported markers.
					if info, err := marker.Stat(); err == nil && info.Mode().IsRegular() {
						stamp, err := io.ReadAll(io.LimitReader(marker, 128))
						if err == nil {
							release.SyncedAt, _ = time.Parse(time.RFC3339Nano, string(stamp))
						}
					}
					marker.Close()
				}
				summary.Releases[tag] = release
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		summary.Bytes += info.Size()
		if release, ok := summary.Releases[tag]; nested && ok {
			release.Bytes += info.Size()
			summary.Releases[tag] = release
		}
		return nil
	})
	return summary, err
}
