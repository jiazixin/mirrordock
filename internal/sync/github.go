package syncer

import (
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/google/go-github/v88/github"
	"io"
	"mirrordock/internal/config"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

func githubHTTP(proxy string) (*http.Client, *http.Transport, error) {
	u, err := config.ParseProxyURL(proxy)
	if err != nil {
		return nil, nil, err
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 30 * time.Second, ResponseHeaderTimeout: time.Minute, IdleConnTimeout: time.Minute, MaxIdleConns: 16, MaxIdleConnsPerHost: 4, ForceAttemptHTTP2: true}
	// Explicit configuration only: blank means direct, with no environment fallback.
	if u != nil {
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: transport, CheckRedirect: githubRedirect}, transport, nil
}

func githubRedirect(r *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("too many redirects")
	}
	if r.URL.Scheme != "https" && r.URL.Scheme != "http" {
		return errors.New("unsupported redirect protocol")
	}
	if r.URL.Scheme == "http" && len(via) > 0 && via[len(via)-1].URL.Scheme == "https" {
		return errors.New("HTTPS downgrade rejected")
	}
	r.Header.Del("Authorization")
	return nil
}

// The SDK auth transport adds the token on each request, including redirects.
// Keep API redirects on the original origin; asset redirects use the unauthenticated client.
func githubAPIRedirect(r *http.Request, via []*http.Request) error {
	if len(via) > 0 && (r.URL.Scheme != via[0].URL.Scheme || r.URL.Host != via[0].URL.Host) {
		return errors.New("GitHub API cross-origin redirect rejected")
	}
	return githubRedirect(r, via)
}

type AssetIdentity struct {
	ID      int64  `json:"id"`
	Size    int64  `json:"size"`
	Updated string `json:"updated"`
	Digest  string `json:"digest"`
}
type selectedAsset struct {
	Tag   string
	Asset *github.ReleaseAsset
}

func GitHub(ctx context.Context, t Task, emit func(Progress), log io.Writer) error {
	httpClient, transport, err := githubHTTP(t.Settings.GitHub.Proxy)
	if err != nil {
		return err
	}
	defer transport.CloseIdleConnections()
	apiHTTP := *httpClient
	apiHTTP.CheckRedirect = githubAPIRedirect
	opts := []github.ClientOptionsFunc{github.WithHTTPClient(&apiHTTP), github.WithTimeout(time.Minute)}
	if t.Token != "" {
		opts = append(opts, github.WithAuthToken(t.Token))
	}
	client, err := github.NewClient(opts...)
	if err != nil {
		return err
	}
	return GitHubWithClient(ctx, client, httpClient, t, emit, log)
}
func GitHubWithClient(ctx context.Context, client *github.Client, httpClient *http.Client, t Task, emit func(Progress), log io.Writer) (result error) {
	defer func() {
		if failure, ok := errors.AsType[*Failure](result); ok {
			fmt.Fprintf(log, "GitHub: %s\n", failure)
		}
	}()
	owner, repo, _ := strings.Cut(t.Source.GitHubRelease.Repository, "/")
	emit(Progress{Phase: "scan", ReceivedBytes: new(int64(0))})
	fmt.Fprintln(log, "GitHub API: scanning Release list")
	releases := []*github.RepositoryRelease{}
	for page := 1; page <= 100; page++ {
		list, res, err := client.Repositories.ListReleases(ctx, owner, repo, &github.ListOptions{Page: page, PerPage: 100})
		if err != nil {
			return githubError(err, t.Settings.GitHub.Proxy)
		}
		fmt.Fprintf(log, "GitHub API HTTP %d: Release page %d\n", res.StatusCode, page)
		for _, r := range list {
			if !r.GetDraft() && (!r.GetPrerelease() || t.Settings.GitHub.IncludePrereleases) {
				releases = append(releases, r)
			}
		}
		if res.NextPage == 0 {
			break
		}
		if page == 100 {
			return &Failure{Code: "RELEASE_LIMIT", Message: "Release 列表超过查询上限，请缩小仓库范围"}
		}
	}
	slices.SortFunc(releases, func(a, b *github.RepositoryRelease) int { return b.GetPublishedAt().Compare(a.GetPublishedAt().Time) })
	count := cmp.Or(t.Source.GitHubRelease.ReleaseCount, t.Settings.GitHub.ReleaseCount)
	releases = releases[:min(count, len(releases))]
	assets := []selectedAsset{}
	keep := map[string]bool{}
	total := int64(0)
	for _, r := range releases {
		tag := url.PathEscape(r.GetTagName())
		if tag == "" || tag == "." || tag == ".." {
			return &Failure{Code: "INVALID_TAG", Message: "Release tag 无法转换成安全目录"}
		}
		keep[tag] = true
		for page := 1; ; page++ {
			list, res, err := client.Repositories.ListReleaseAssets(ctx, owner, repo, r.GetID(), &github.ListOptions{Page: page, PerPage: 100})
			if err != nil {
				return githubError(err, t.Settings.GitHub.Proxy)
			}
			fmt.Fprintf(log, "GitHub API HTTP %d: Release %d asset page %d\n", res.StatusCode, r.GetID(), page)
			for _, a := range list {
				matched := len(t.Source.GitHubRelease.AssetPatterns) == 0
				for _, p := range t.Source.GitHubRelease.AssetPatterns {
					ok, _ := path.Match(p, a.GetName())
					matched = matched || ok
				}
				if matched {
					assets = append(assets, selectedAsset{Tag: tag, Asset: a})
					total += int64(a.GetSize())
				}
			}
			if res.NextPage == 0 {
				break
			}
			if page >= 100 {
				return &Failure{Code: "ASSET_LIMIT", Message: "资产列表超过查询上限"}
			}
		}
	}
	if len(assets) == 0 {
		return &Failure{Code: "NO_ASSETS", Message: "所选 Release 没有匹配的附件，不发布空镜像"}
	}
	done := int64(0)
	networkBytes := int64(0)
	success := 0
	skipped := 0
	started := time.Now()
	for _, entry := range assets {
		a := entry.Asset
		name := a.GetName()
		if name == "" || name == "." || name == ".." || path.Base(name) != name || strings.ContainsAny(name, "\\\x00\r\n") {
			return &Failure{Code: "INVALID_ASSET", Message: "资产名称不是安全文件名"}
		}
		dir := filepath.Join(t.Directory, entry.Tag)
		if st, err := os.Lstat(dir); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return &Failure{Code: "INVALID_ASSET", Message: "Release 目录不能是符号链接"}
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		target := filepath.Join(dir, name)
		identity := AssetIdentity{ID: a.GetID(), Size: int64(a.GetSize()), Updated: a.GetUpdatedAt().String(), Digest: a.GetDigest()}
		base := done

		tick := func(n int64) {
			percent := float64(0)
			if total > 0 {
				percent = 100 * float64(base+n) / float64(total)
			}
			speed := float64(networkBytes) / max(time.Since(started).Seconds(), .001)
			var eta *float64
			if speed > 0 {
				eta = new(float64(total-base-n) / speed)
			}
			emit(Progress{Phase: "transfer", ReceivedBytes: new(networkBytes), Bytes: base + n, Total: total, Percent: new(percent), Speed: speed, ETA: eta, Files: len(assets), Success: success, Skipped: skipped})
		}
		reused, err := downloadAsset(ctx, client, httpClient, owner, repo, a.GetID(), identity, target, tick, t.Settings.GitHub.Proxy, func(n int64) { networkBytes += n })
		if err != nil {
			return err
		}
		done += int64(a.GetSize())
		success++
		if reused {
			skipped++
		}
		fmt.Fprintf(log, "asset %d %s/%s: verified (%d bytes)\n", a.GetID(), entry.Tag, name, a.GetSize())
		tick(int64(a.GetSize()))
	}
	// Record only releases actually verified in this successful attempt. The
	// private marker becomes public listing metadata only after publication.
	verifiedTags := map[string]bool{}
	for _, entry := range assets {
		verifiedTags[entry.Tag] = true
	}
	for tag := range verifiedTags {
		if err := ctx.Err(); err != nil {
			return err
		}
		marker := filepath.Join(t.Directory, tag, ".mirrordock-release-sync")
		if err := config.AtomicWrite(marker, []byte(time.Now().UTC().Format(time.RFC3339Nano)), 0600); err != nil {
			return err
		}
	}
	if t.Settings.GitHub.PruneOldReleases {
		entries, err := os.ReadDir(t.Directory)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() && !keep[e.Name()] && !strings.HasPrefix(e.Name(), ".") {
				if err = os.RemoveAll(filepath.Join(t.Directory, e.Name())); err != nil {
					return err
				}
			}
		}
	}
	emit(Progress{Phase: "verify", ReceivedBytes: new(networkBytes), Bytes: total, Total: total, Percent: new(float64(100)), Files: len(assets), Success: success, Skipped: skipped})
	return nil
}
func githubError(err error, proxy string) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if _, ok := errors.AsType[*github.RateLimitError](err); ok {
		return &Failure{Code: "GITHUB_RATE_LIMIT", Message: "GitHub API 配额已用尽，请等待限流重置或检查 Token"}
	}
	if _, ok := errors.AsType[*github.AbuseRateLimitError](err); ok {
		return &Failure{Code: "GITHUB_RATE_LIMIT", Message: "GitHub API 触发临时限流，请稍后重试", Retryable: true}
	}
	if e, ok := errors.AsType[*github.ErrorResponse](err); ok && e.Response != nil {
		// Do not echo upstream messages: a proxy can return arbitrary text or credentials.
		detail := "请求未成功，请检查 GitHub 服务状态"
		switch e.Response.StatusCode {
		case http.StatusUnauthorized:
			detail = "Token 无效或已过期，请重新配置"
		case http.StatusForbidden:
			detail = "访问被拒绝，请检查 Token 权限或 API 限流"
		case http.StatusNotFound:
			detail = "仓库或附件不存在，或 Token 无权访问"
		case http.StatusProxyAuthRequired:
			detail = "代理要求认证，请检查代理用户名和密码"
		}
		return &Failure{Code: "GITHUB_API", Message: fmt.Sprintf("GitHub API HTTP %d：%s", e.Response.StatusCode, detail), Retryable: transientHTTP(e.Response.StatusCode)}
	}
	return githubNetworkError(err, proxy, "GITHUB_API", "GitHub API 查询")
}

func githubNetworkError(err error, proxy, code, operation string) error {
	if proxy == "" {
		operation += "直连"
	}
	return httpNetworkError(err, proxy, code, operation)
}

// Classify failures without emitting URLs, signed query strings, credentials or raw upstream text.
func httpNetworkError(err error, proxy, code, operation string) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	detail := "网络连接失败，请检查网络"
	retryable := false
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		detail = "DNS 解析失败，请检查目标或代理的域名解析"
		retryable = true
	} else if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		detail = "TLS 证书校验失败，请检查目标或 HTTPS 代理的证书"
	} else if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		detail = "TLS 证书不受信任，请检查目标或 HTTPS 代理的证书"
	} else if n, ok := errors.AsType[net.Error](err); ok && n.Timeout() {
		detail = "网络等待超时，请检查目标或代理的连接"
		retryable = true
	} else if errors.Is(err, syscall.ECONNREFUSED) {
		detail = "连接被拒绝，请检查目标或代理的监听地址与端口"
		retryable = true
	} else if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		detail = "连接被提前关闭（EOF）"
		retryable = true
	} else if strings.Contains(err.Error(), "Proxy Authentication Required") {
		detail = "代理要求认证（HTTP 407），请检查代理用户名和密码"
	} else if _, ok := errors.AsType[*net.OpError](err); ok {
		retryable = true
	}
	if proxy != "" {
		code = "GITHUB_PROXY"
		if e, ok := errors.AsType[*net.OpError](err); ok && e.Op == "proxyconnect" {
			operation += "未能建立代理连接"
		} else {
			operation += "经代理失败"
		}
		u, _ := config.ParseProxyURL(proxy)
		if u != nil && u.Scheme == "https" {
			detail += "；https:// 要求代理入口本身支持 TLS，HTTP/mixed 入口应使用 http://"
		} else {
			detail += "；请确认代理协议、端口及 CONNECT/SOCKS 转发是否可用"
		}
	} else {
		operation += "失败"
	}
	return &Failure{Code: code, Message: operation + "：" + detail, Retryable: retryable}
}
func identityFile(target string) string {
	return filepath.Join(filepath.Dir(target), ".mirrordock-asset-"+filepath.Base(target)+".json")
}
func sameIdentity(target string, id AssetIdentity) bool {
	b, err := os.ReadFile(identityFile(target))
	if err != nil {
		return false
	}
	var old AssetIdentity
	if json.Unmarshal(b, &old) != nil {
		return false
	}
	return old == id
}
func verifyAsset(ctx context.Context, target string, id AssetIdentity) error {
	st, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("资产不是普通文件")
	}
	f, err := os.Open(target)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err = f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() != id.Size {
		return errors.New("资产大小不匹配")
	}
	if digest, ok := strings.CutPrefix(id.Digest, "sha256:"); ok {
		h := sha256.New()
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
		if hex.EncodeToString(h.Sum(nil)) != digest {
			return errors.New("资产 SHA-256 不匹配")
		}
	}
	return nil
}
func downloadAsset(ctx context.Context, client *github.Client, httpClient *http.Client, owner, repo string, assetID int64, id AssetIdentity, target string, tick func(int64), proxy string, received ...func(int64)) (bool, error) {
	if sameIdentity(target, id) && verifyAsset(ctx, target, id) == nil {
		tick(id.Size)
		return true, nil
	}
	partial := target + ".part"
	for _, name := range []string{target, partial} {
		if st, e := os.Lstat(name); e == nil && !st.Mode().IsRegular() {
			return false, &Failure{Code: "INVALID_ASSET", Message: "资产路径必须是普通文件"}
		}
	}
	offset := int64(0)
	if sameIdentity(partial, id) {
		if st, err := os.Stat(partial); err == nil && st.Mode().IsRegular() && st.Size() < id.Size {
			offset = st.Size()
		}
	} else {
		_ = os.Remove(partial)
	}
	b, err := json.Marshal(id)
	if err != nil {
		return false, err
	}
	// A partial identity must never describe the previous complete asset. This
	// matters when an upstream replaces an asset with another of the same size.
	if err = config.AtomicWrite(identityFile(partial), b, 0600); err != nil {
		return false, err
	}
	stream, redirect, err := client.Repositories.DownloadReleaseAsset(ctx, owner, repo, assetID, nil)
	if err != nil {
		return false, githubError(err, proxy)
	}
	if stream != nil {
		offset = 0
	}
	if redirect != "" {
		if err = config.HTTPURL(redirect); err != nil {
			return false, err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", redirect, nil)
		if err != nil {
			return false, err
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		res, err := httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, githubNetworkError(err, proxy, "DOWNLOAD_FAILED", "资产下载")
		}
		if offset > 0 && (res.StatusCode == 416 || (res.StatusCode == 206 && !validRange(res.Header.Get("Content-Range"), offset, id.Size))) {
			res.Body.Close()
			offset = 0
			req.Header.Del("Range")
			res, err = httpClient.Do(req)
			if err != nil {
				return false, githubNetworkError(err, proxy, "DOWNLOAD_FAILED", "资产重新下载")
			}
		}
		if offset > 0 && res.StatusCode == 206 && validRange(res.Header.Get("Content-Range"), offset, id.Size) {
		} else if res.StatusCode == 200 {
			offset = 0
		} else {
			res.Body.Close()
			return false, &Failure{Code: "DOWNLOAD_HTTP", Message: fmt.Sprintf("资产下载 HTTP %d", res.StatusCode), Retryable: transientHTTP(res.StatusCode)}
		}
		stream = res.Body
	}
	if stream == nil {
		return false, errors.New("资产下载无响应流")
	}
	defer stream.Close()
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(partial, flags, 0644)
	if err != nil {
		return false, err
	}
	defer out.Close()
	if _, err = out.Seek(offset, io.SeekStart); err != nil {
		return false, err
	}
	tick(offset)
	n := offset
	defer func() { tick(n) }()
	buf := make([]byte, 64<<10)
	last := time.Time{}
	for {
		if err = ctx.Err(); err != nil {
			return false, err
		}
		count, readErr := stream.Read(buf)
		if count > 0 {
			for _, report := range received {
				report(int64(count))
			}
			if n+int64(count) > id.Size {
				return false, &Failure{Code: "ASSET_SIZE", Message: "下载超过声明大小"}
			}
			w, err := out.Write(buf[:count])
			n += int64(w)
			if err != nil {
				return false, err
			}
			if time.Since(last) > time.Second {
				tick(n)
				last = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, githubNetworkError(readErr, proxy, "DOWNLOAD_FAILED", "资产响应流中断，已保存暂存文件")
		}
	}
	if n < id.Size {
		return false, &Failure{Code: "DOWNLOAD_FAILED", Message: "资产响应流提前结束，已保存暂存文件", Retryable: true}
	}
	if err = out.Sync(); err != nil {
		return false, err
	}
	if err = out.Close(); err != nil {
		return false, err
	}
	if err = verifyAsset(ctx, partial, id); err != nil {
		return false, &Failure{Code: "ASSET_VERIFY", Message: err.Error()}
	}
	if err = os.Rename(partial, target); err != nil {
		return false, err
	}
	if err = config.AtomicWrite(identityFile(target), b, 0600); err != nil {
		return false, err
	}
	_ = os.Remove(identityFile(partial))
	tick(n)
	return false, nil
}

func validRange(value string, offset, total int64) bool {
	var start, end, size int64
	var suffix string
	n, _ := fmt.Sscanf(value, "bytes %d-%d/%d%s", &start, &end, &size, &suffix)
	return n == 3 && start == offset && end == total-1 && size == total && start <= end
}

// Retry only statuses that describe a transient upstream condition.
func transientHTTP(status int) bool {
	return status == 408 || status == 429 || status == 500 || status == 502 || status == 503 || status == 504
}
