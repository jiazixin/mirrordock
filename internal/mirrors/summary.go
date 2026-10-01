package mirrors

import (
	"context"
	"golang.org/x/sys/unix"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
				r := ReleaseSummary{}
				f, e := root.OpenFile(filepath.Join(rel, ".mirrordock-release-sync"), os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
				if e == nil {
					// Bound marker reads, including malformed legacy/imported markers.
					if st, err := f.Stat(); err == nil && st.Mode().IsRegular() {
						b, err := io.ReadAll(io.LimitReader(f, 128))
						if err == nil {
							r.SyncedAt, _ = time.Parse(time.RFC3339Nano, string(b))
						}
					}
					f.Close()
				}
				summary.Releases[tag] = r
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
		if r, ok := summary.Releases[tag]; nested && ok {
			r.Bytes += info.Size()
			summary.Releases[tag] = r
		}
		return nil
	})
	return summary, err
}
