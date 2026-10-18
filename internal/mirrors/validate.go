package mirrors

import (
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha1"
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
	"mirrordock/internal/config"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// ValidationError describes inconsistent upstream repository data. Local I/O
// and cancellation errors remain distinguishable to the execution engine.
type ValidationError struct{ Err error }

func (e *ValidationError) Error() string { return e.Err.Error() }

func (e *ValidationError) Unwrap() error { return e.Err }

func ValidateSource(ctx context.Context, dir string, source config.Source) error {
	kind := source.Type
	if kind == "reposync" && !config.DefaultBool(source.RepoSync.DownloadMetadata) {
		kind = "packages"
	}
	return Validate(ctx, dir, kind)
}

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
		if kind == "packages" {
			return nil
		}
		if d.Name() == "repomd.xml" && filepath.Base(filepath.Dir(p)) == "repodata" {
			repos++
			return repositoryError(validateRPM(ctx, root, filepath.ToSlash(rel)))
		}
		if (d.Name() == "Release" || d.Name() == "InRelease") && aptReleasePath(filepath.ToSlash(rel)) {
			if d.Name() == "Release" {
				inRelease := path.Join(path.Dir(filepath.ToSlash(rel)), "InRelease")
				if _, err := root.Stat(inRelease); err == nil {
					return nil
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			repos++
			return repositoryError(validateAPT(ctx, root, filepath.ToSlash(rel)))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if files == 0 {
		return &ValidationError{errors.New("同步目录为空，无法发布")}
	}
	if kind == "reposync" && repos == 0 {
		return &ValidationError{errors.New("仓库缺少 repodata 元数据")}
	}
	return nil
}

func aptReleasePath(name string) bool {
	parts := strings.Split(name, "/")
	return len(parts) == 1 || (len(parts) >= 3 && parts[len(parts)-3] == "dists")
}

func repositoryError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if _, ok := errors.AsType[*os.PathError](err); ok && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return &ValidationError{err}
}

func checkFile(root *os.Root, p string, size int64) error {
	if hidden(p) {
		return errors.New("metadata references private file")
	}
	file, err := root.Open(p)
	if err != nil {
		return fmt.Errorf("仓库引用文件缺失 %s: %w", p, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || (size >= 0 && info.Size() != size) {
		return fmt.Errorf("仓库引用文件大小/类型错误: %s", p)
	}
	return nil
}

// validateRPM checks every index listed by repomd.xml and the packages of
// its primary index.
func validateRPM(ctx context.Context, root *os.Root, p string) error {
	file, err := root.Open(p)
	if err != nil {
		return err
	}
	defer file.Close()
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
	if err = xml.NewDecoder(io.LimitReader(file, 8<<20)).Decode(&doc); err != nil {
		return err
	}
	base := path.Dir(path.Dir(p))
	primary := false
	for _, index := range doc.Data {
		if err = ctx.Err(); err != nil {
			return err
		}
		file := path.Join(base, index.Location.Href)
		if err = checkFile(root, file, index.Size); err != nil {
			return err
		}
		if index.Checksum.Value != "" {
			if err = checkHash(ctx, root, file, index.Checksum.Type, strings.TrimSpace(index.Checksum.Value)); err != nil {
				return err
			}
		}
		if index.Type == "primary" {
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
	var digest hash.Hash
	switch kind {
	case "sha", "sha1":
		// SHA-1 checks old repository metadata for consistency only; it is
		// not signature authentication.
		digest = sha1.New()
	case "sha256":
		digest = sha256.New()
	case "sha512":
		digest = sha512.New()
	default:
		return fmt.Errorf("不支持元数据校验算法 %s", kind)
	}
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := make([]byte, 64<<10)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			_, _ = digest.Write(buffer[:count])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if hex.EncodeToString(digest.Sum(nil)) != strings.ToLower(want) {
		return fmt.Errorf("元数据校验和不匹配: %s", name)
	}
	return nil
}

// compressed opens p and decompresses it by extension. Decoder memory is
// bounded so a crafted index cannot exhaust RAM.
func compressed(root *os.Root, p string) (io.ReadCloser, error) {
	file, err := root.Open(p)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasSuffix(p, ".gz"):
		reader, err := gzip.NewReader(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		return &closer{Reader: reader, close: func() error { return errors.Join(reader.Close(), file.Close()) }}, nil
	case strings.HasSuffix(p, ".bz2"):
		return &closer{Reader: bzip2.NewReader(file), close: file.Close}, nil
	case strings.HasSuffix(p, ".xz"):
		reader, err := (xz.ReaderConfig{DictCap: decoderMemory}).NewReader(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		return &closer{Reader: reader, close: file.Close}, nil
	case strings.HasSuffix(p, ".zst"):
		reader, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(decoderMemory))
		if err != nil {
			file.Close()
			return nil, err
		}
		closeAll := func() error {
			reader.Close()
			return file.Close()
		}
		return &closer{Reader: reader, close: closeAll}, nil
	default:
		return file, nil
	}
}

// decoderMemory bounds the xz dictionary and zstd window of index files.
const decoderMemory = 64 << 20

type closer struct {
	io.Reader
	close func() error
}

func (c *closer) Close() error { return c.close() }

// validatePrimary streams primary.xml and checks every package size.
func validatePrimary(ctx context.Context, root *os.Root, base, p string) error {
	reader, err := compressed(root, p)
	if err != nil {
		return err
	}
	defer reader.Close()
	decoder := xml.NewDecoder(reader)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "package" {
			var pkg struct {
				Location struct {
					Href string `xml:"href,attr"`
				} `xml:"location"`
				Size struct {
					Package int64 `xml:"package,attr"`
				} `xml:"size"`
			}
			if err = decoder.DecodeElement(&pkg, &start); err != nil {
				return err
			}
			if err = checkFile(root, path.Join(base, pkg.Location.Href), pkg.Size.Package); err != nil {
				return err
			}
		}
	}
}

// validateAPT checks the SHA256 section of a Release file against the
// listed indexes and the packages they reference.
func validateAPT(ctx context.Context, root *os.Root, p string) error {
	const maxReleaseBytes = 8 << 20
	file, err := root.Open(p)
	if err != nil {
		return err
	}
	defer file.Close()
	release, err := io.ReadAll(io.LimitReader(file, maxReleaseBytes+1))
	if err != nil {
		return err
	}
	if len(release) > maxReleaseBytes {
		return errors.New("APT Release 超过 8 MiB 校验上限")
	}
	inHashes := false
	declared, checked := 0, 0
	for line := range strings.SplitSeq(string(release), "\n") {
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
		if size < 0 || !fs.ValidPath(fields[2]) || strings.Contains(fields[2], "\\") || hidden(fields[2]) {
			return errors.New("APT 索引路径或大小无效")
		}
		declared++
		name := path.Join(path.Dir(p), fields[2])
		// Release also lists absent uncompressed forms and excluded architectures.
		if _, err := root.Stat(name); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		checked++
		if err = checkFile(root, name, size); err != nil {
			return err
		}
		if err = checkHash(ctx, root, name, "sha256", fields[0]); err != nil {
			return err
		}
		// Packages, Packages.gz, Packages.xz …: check every referenced package.
		baseName := path.Base(name)
		if baseName == "Packages" || strings.TrimSuffix(baseName, path.Ext(name)) == "Packages" {
			if err = validatePackages(ctx, root, name); err != nil {
				return err
			}
		}
	}
	if declared > 0 && checked == 0 {
		return fmt.Errorf("Release 中所有索引均缺失: %s", p)
	}
	return nil
}

// validatePackages checks the size of every package a Packages index lists.
// Packages indexes use repository-root-relative filenames.
func validatePackages(ctx context.Context, root *os.Root, p string) error {
	reader, err := compressed(root, p)
	if err != nil {
		return err
	}
	defer reader.Close()
	base := ""
	if before, _, ok := strings.Cut(p, "dists/"); ok {
		base = before
	}
	scanner := bufio.NewScanner(reader)
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
		if value, ok := strings.CutPrefix(line, "Filename: "); ok {
			filename = value
		}
		if value, ok := strings.CutPrefix(line, "Size: "); ok {
			size, err = strconv.ParseInt(value, 10, 64)
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
