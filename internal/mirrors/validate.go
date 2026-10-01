package mirrors

import (
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Validate checks repository indexes and referenced file sizes without hashing
// each package during every download. The upstream package signatures remain
// the responsibility of reposync/client signature verification.
func Validate(ctx context.Context, dir, kind string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	files := 0
	repos := 0
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
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		files++
		if d.Name() == "repomd.xml" && filepath.Base(filepath.Dir(p)) == "repodata" {
			repos++
			return validateRPM(ctx, root, filepath.ToSlash(rel))
		}
		if d.Name() == "Release" || d.Name() == "InRelease" {
			repos++
			return validateAPT(ctx, root, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if files == 0 {
		return errors.New("同步目录为空，无法发布")
	}
	if kind == "reposync" && repos == 0 {
		return errors.New("仓库缺少 repodata 元数据")
	}
	return nil
}
func checkFile(root *os.Root, p string, size int64) error {
	if hidden(p) {
		return errors.New("metadata references private file")
	}
	f, err := root.Open(p)
	if err != nil {
		return fmt.Errorf("仓库引用文件缺失 %s: %w", p, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || (size >= 0 && st.Size() != size) {
		return fmt.Errorf("仓库引用文件大小/类型错误: %s", p)
	}
	return nil
}
func validateRPM(ctx context.Context, root *os.Root, p string) error {
	f, err := root.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	var doc struct {
		Data []struct {
			Type     string `xml:"type,attr"`
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
			Size     int64 `xml:"size"`
			Checksum struct {
				Type  string `xml:"type,attr"`
				Value string `xml:",chardata"`
			} `xml:"checksum"`
		} `xml:"data"`
	}
	if err = xml.NewDecoder(io.LimitReader(f, 8<<20)).Decode(&doc); err != nil {
		return err
	}
	base := path.Dir(path.Dir(p))
	primary := false
	for _, d := range doc.Data {
		if err = ctx.Err(); err != nil {
			return err
		}
		file := path.Join(base, d.Location.Href)
		if err = checkFile(root, file, d.Size); err != nil {
			return err
		}
		if d.Checksum.Value != "" {
			if err = checkHash(ctx, root, file, d.Checksum.Type, strings.TrimSpace(d.Checksum.Value)); err != nil {
				return err
			}
		}
		if d.Type == "primary" {
			primary = true
			if err = validatePrimary(ctx, root, base, file); err != nil {
				return err
			}
		}
	}
	if !primary {
		return errors.New("repomd 缺少 primary 索引")
	}
	return nil
}

func checkHash(ctx context.Context, root *os.Root, name, kind, want string) error {
	var h hash.Hash
	switch kind {
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		return fmt.Errorf("不支持元数据校验算法 %s", kind)
	}
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		n, e := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.ToLower(want) {
		return fmt.Errorf("元数据校验和不匹配: %s", name)
	}
	return nil
}
func compressed(root *os.Root, p string) (io.ReadCloser, error) {
	f, err := root.Open(p)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasSuffix(p, ".gz"):
		g, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &closer{Reader: g, close: func() error { return errors.Join(g.Close(), f.Close()) }}, nil
	case strings.HasSuffix(p, ".bz2"):
		return &closer{Reader: bzip2.NewReader(f), close: f.Close}, nil
	case strings.HasSuffix(p, ".xz"):
		x, err := (xz.ReaderConfig{DictCap: 64 << 20}).NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &closer{Reader: x, close: f.Close}, nil
	case strings.HasSuffix(p, ".zst"):
		z, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
		if err != nil {
			f.Close()
			return nil, err
		}
		return &closer{Reader: z, close: func() error { z.Close(); return f.Close() }}, nil
	default:
		return f, nil
	}
}

type closer struct {
	io.Reader
	close func() error
}

func (c *closer) Close() error { return c.close() }
func validatePrimary(ctx context.Context, root *os.Root, base, p string) error {
	f, err := compressed(root, p)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "package" {
			var pkg struct {
				Location struct {
					Href string `xml:"href,attr"`
				} `xml:"location"`
				Size struct {
					Package int64 `xml:"package,attr"`
				} `xml:"size"`
			}
			if err = dec.DecodeElement(&pkg, &start); err != nil {
				return err
			}
			if err = checkFile(root, path.Join(base, pkg.Location.Href), pkg.Size.Package); err != nil {
				return err
			}
		}
	}
}
func validateAPT(ctx context.Context, root *os.Root, p string) error {
	f, err := root.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8<<20))
	if err != nil {
		return err
	}
	inHashes := false
	for line := range strings.SplitSeq(string(b), "\n") {
		if err = ctx.Err(); err != nil {
			return err
		}
		if line == "SHA256:" {
			inHashes = true
			continue
		}
		if !inHashes {
			continue
		}
		fields := strings.Fields(line)
		if !strings.HasPrefix(line, " ") {
			inHashes = false
			continue
		}
		if len(fields) != 3 {
			continue
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return err
		}
		name := path.Join(path.Dir(p), fields[2])
		if err = checkFile(root, name, size); err != nil {
			return err
		}
		if err = checkHash(ctx, root, name, "sha256", fields[0]); err != nil {
			return err
		}
		if strings.Contains(path.Base(name), "Packages") && !strings.HasSuffix(name, ".diff/Index") {
			if err = validatePackages(ctx, root, name); err != nil {
				return err
			}
		}
	}
	return nil
}
func validatePackages(ctx context.Context, root *os.Root, p string) error {
	f, err := compressed(root, p)
	if err != nil {
		return err
	}
	defer f.Close() // Packages indexes use repository-root-relative filenames.
	base := ""
	if before, _, ok := strings.Cut(p, "dists/"); ok {
		base = before
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	filename := ""
	size := int64(-1)
	check := func() error {
		if filename == "" {
			return nil
		}
		if size < 0 {
			return fmt.Errorf("Packages 文件缺少有效 Size: %s", filename)
		}
		return checkFile(root, path.Join(base, filename), size)
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if err = ctx.Err(); err != nil {
			return err
		}
		if v, ok := strings.CutPrefix(line, "Filename: "); ok {
			filename = v
		}
		if v, ok := strings.CutPrefix(line, "Size: "); ok {
			size, err = strconv.ParseInt(v, 10, 64)
			if err != nil {
				return err
			}
		}
		if line == "" {
			if err = check(); err != nil {
				return err
			}
			filename = ""
			size = -1
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	return check()
}
