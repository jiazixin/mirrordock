package mirrors

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type preparation struct {
	Fingerprint    string `json:"fingerprint"`
	BaseGeneration string `json:"base_generation"`
}

func readPreparation(work string) preparation {
	f, err := os.Open(filepath.Join(work, ".mirrordock-prepared"))
	if err != nil {
		return preparation{}
	}
	defer f.Close()
	var p preparation
	// An unreadable marker disables the no-change shortcut for this run.
	if err := json.UnmarshalRead(io.LimitReader(f, 4096), &p); err != nil {
		return preparation{}
	}
	return p
}

// This is a metadata/mutation fingerprint of this private working tree, not
// a content hash. Inode and ctime detect writes even if size/mtime are restored.
// File bodies are never read. Memory is bounded by directory traversal.
func treeFingerprint(ctx context.Context, dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel != "." && hidden(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%q:%d:%s:", rel, st.Mode(), fileOwnership(st))
		if d.IsDir() {
			if rel != "." {
				fmt.Fprintf(h, "%d", st.ModTime().UnixNano())
			}
		} else if st.Mode().IsRegular() {
			fmt.Fprintf(h, "%d:%d:%s", st.Size(), st.ModTime().UnixNano(), fileIdentity(st))
		} else if st.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "%q:%d", link, st.ModTime().UnixNano())
		} else {
			return fmt.Errorf("工作目录包含非常规文件: %s", rel)
		}
		fmt.Fprintln(h)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
