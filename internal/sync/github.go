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

// GitHub listing and download limits.
const (
	githubPageSize  = 100
	githubPageLimit = 100
	releaseMarker   = ".mirrordock-release-sync"
	copyBufferBytes = 64 << 10
)

// githubHTTP builds the transport shared by API and asset requests. Only an
// explicitly configured proxy is used: blank means direct, with no
// environment fallback.
func githubHTTP(proxy string) (*http.Client, *http.Transport, error) {
	proxyURL, err := config.ParseProxyURL(proxy)
	if err != nil {
		return nil, nil, err
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: time.Minute,
		IdleConnTimeout:       time.Minute,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   4,
		ForceAttemptHTTP2:     true,
	}
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: transport, CheckRedirect: downloadRedirect}, transport, nil
}

// downloadRedirect allows at most ten HTTP(S) redirects, refuses an HTTPS to
// HTTP downgrade and never forwards an Authorization header.
func downloadRedirect(r *http.Request, via []*http.Request) error {
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
	return downloadRedirect(r, via)
}

// AssetIdentity is the sidecar record that proves a local file is the
// asset GitHub currently publishes.
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
	options := []github.ClientOptionsFunc{github.WithHTTPClient(&apiHTTP), github.WithTimeout(time.Minute)}
	if t.Token != "" {
		options = append(options, github.WithAuthToken(t.Token))
	}
	client, err := github.NewClient(options...)
	if err != nil {
		return err
	}
	return GitHubWithClient(ctx, client, httpClient, t, emit, log)
}

// githubSync holds the state of one GitHub Release synchronization attempt.
type githubSync struct {
	client      *github.Client
	httpClient  *http.Client
	task        Task
	owner, repo string
	emit        func(Progress)
	log         io.Writer
}

// GitHubWithClient mirrors the newest matching releases' assets into the
// task directory. Only assets are downloaded, never source archives.
func GitHubWithClient(
	ctx context.Context,
	client *github.Client,
	httpClient *http.Client,
	t Task,
	emit func(Progress),
	log io.Writer,
) (result error) {
	defer func() {
		if failure, ok := errors.AsType[*Failure](result); ok {
			fmt.Fprintf(log, "GitHub: %s\n", failure)
		}
	}()
	owner, repo, _ := strings.Cut(t.Source.GitHubRelease.Repository, "/")
	sync := &githubSync{client: client, httpClient: httpClient, task: t, owner: owner, repo: repo, emit: emit, log: log}
	emit(Progress{Phase: "scan", ReceivedBytes: new(int64(0))})
	releases, err := sync.releases(ctx)
	if err != nil {
		return err
	}
	assets, kept, err := sync.selectAssets(ctx, releases)
	if err != nil {
		return err
	}
	if len(assets) == 0 {
		return &Failure{Code: "NO_ASSETS", Message: "所选 Release 没有匹配的附件，不发布空镜像"}
	}
	totals, err := sync.download(ctx, assets)
	if err != nil {
		return err
	}
	if err := sync.markReleases(ctx, assets); err != nil {
		return err
	}
	if t.Settings.GitHub.PruneOldReleases {
		if err := sync.prune(ctx, assets, kept); err != nil {
			return err
		}
	}
	emit(Progress{
		Phase:         "verify",
		ReceivedBytes: new(totals.received),
		Bytes:         totals.bytes,
		Total:         totals.bytes,
		Percent:       new(float64(100)),
		Files:         len(assets),
		Success:       totals.success,
		Skipped:       totals.reused,
	})
	return nil
}

// releases lists every published release, newest first, limited to the
// configured count. Drafts and, unless enabled, prereleases are skipped.
func (g *githubSync) releases(ctx context.Context) ([]*github.RepositoryRelease, error) {
	settings := g.task.Settings.GitHub
	fmt.Fprintln(g.log, "GitHub API: scanning Release list")
	releases := []*github.RepositoryRelease{}
	for page := 1; page <= githubPageLimit; page++ {
		options := &github.ListOptions{Page: page, PerPage: githubPageSize}
		list, response, err := g.client.Repositories.ListReleases(ctx, g.owner, g.repo, options)
		if err != nil {
			return nil, githubError(err, settings.Proxy)
		}
		fmt.Fprintf(g.log, "GitHub API HTTP %d: Release page %d\n", response.StatusCode, page)
		for _, release := range list {
			if !release.GetDraft() && (!release.GetPrerelease() || settings.IncludePrereleases) {
				releases = append(releases, release)
			}
		}
		if response.NextPage == 0 {
			break
		}
		if page == githubPageLimit {
			return nil, &Failure{Code: "RELEASE_LIMIT", Message: "Release 列表超过查询上限，请缩小仓库范围"}
		}
	}
	slices.SortFunc(releases, func(a, b *github.RepositoryRelease) int {
		return b.GetPublishedAt().Compare(a.GetPublishedAt().Time)
	})
	count := cmp.Or(g.task.Source.GitHubRelease.ReleaseCount, settings.ReleaseCount)
	return releases[:min(count, len(releases))], nil
}

// matchesPatterns reports whether name matches any glob; no glob means all.
func matchesPatterns(patterns []string, name string) bool {
	if len(patterns) == 0 {
		return true
	}
	return slices.ContainsFunc(patterns, func(pattern string) bool {
		matched, _ := path.Match(pattern, name)
		return matched
	})
}

// selectAssets lists the matching assets of each release. kept holds the
// directory name of every selected release.
func (g *githubSync) selectAssets(
	ctx context.Context,
	releases []*github.RepositoryRelease,
) ([]selectedAsset, map[string]bool, error) {
	patterns := g.task.Source.GitHubRelease.AssetPatterns
	assets := []selectedAsset{}
	kept := map[string]bool{}
	for _, release := range releases {
		tag := url.PathEscape(release.GetTagName())
		if !safeGitHubName(tag) {
			return nil, nil, &Failure{Code: "INVALID_TAG", Message: "Release tag 无法转换成安全目录"}
		}
		kept[tag] = true
		for page := 1; ; page++ {
			options := &github.ListOptions{Page: page, PerPage: githubPageSize}
			list, response, err := g.client.Repositories.ListReleaseAssets(ctx, g.owner, g.repo, release.GetID(), options)
			if err != nil {
				return nil, nil, githubError(err, g.task.Settings.GitHub.Proxy)
			}
			fmt.Fprintf(g.log, "GitHub API HTTP %d: Release %d asset page %d\n", response.StatusCode, release.GetID(), page)
			for _, asset := range list {
				if matchesPatterns(patterns, asset.GetName()) {
					assets = append(assets, selectedAsset{Tag: tag, Asset: asset})
				}
			}
			if response.NextPage == 0 {
				break
			}
			if page >= githubPageLimit {
				return nil, nil, &Failure{Code: "ASSET_LIMIT", Message: "资产列表超过查询上限"}
			}
		}
	}
	return assets, kept, nil
}

// downloadTotals summarizes the download phase.
type downloadTotals struct {
	bytes    int64 // declared size of every selected asset
	received int64 // response body bytes actually read from the network
	success  int
	reused   int
}

// download fetches or reuses every selected asset, reporting progress.
func (g *githubSync) download(ctx context.Context, assets []selectedAsset) (downloadTotals, error) {
	totals := downloadTotals{}
	for _, entry := range assets {
		totals.bytes += int64(entry.Asset.GetSize())
	}
	completed := int64(0)
	started := time.Now()
	for _, entry := range assets {
		asset := entry.Asset
		name := asset.GetName()
		if !safeGitHubName(name) {
			return totals, &Failure{Code: "INVALID_ASSET", Message: "资产名称不安全或与内部保留名称冲突"}
		}
		dir := filepath.Join(g.task.Directory, entry.Tag)
		if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return totals, &Failure{Code: "INVALID_ASSET", Message: "Release 目录不能是符号链接"}
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return totals, err
		}
		base := completed
		report := func(written int64) {
			percent := float64(0)
			if totals.bytes > 0 {
				percent = 100 * float64(base+written) / float64(totals.bytes)
			}
			speed := float64(totals.received) / max(time.Since(started).Seconds(), .001)
			var eta *float64
			if speed > 0 {
				eta = new(float64(totals.bytes-base-written) / speed)
			}
			g.emit(Progress{
				Phase:         "transfer",
				ReceivedBytes: new(totals.received),
				Bytes:         base + written,
				Total:         totals.bytes,
				Percent:       new(percent),
				Speed:         speed,
				ETA:           eta,
				Files:         len(assets),
				Success:       totals.success,
				Skipped:       totals.reused,
			})
		}
		download := assetDownload{
			client:     g.client,
			httpClient: g.httpClient,
			owner:      g.owner,
			repo:       g.repo,
			identity: AssetIdentity{
				ID:      asset.GetID(),
				Size:    int64(asset.GetSize()),
				Updated: asset.GetUpdatedAt().String(),
				Digest:  asset.GetDigest(),
			},
			target:   filepath.Join(dir, name),
			proxy:    g.task.Settings.GitHub.Proxy,
			idle:     assetIdleTimeout,
			progress: report,
			received: func(n int64) { totals.received += n },
		}
		reused, err := download.run(ctx)
		if err != nil {
			return totals, err
		}
		completed += int64(asset.GetSize())
		totals.success++
		if reused {
			totals.reused++
		}
		fmt.Fprintf(g.log, "asset %d %s/%s: verified (%d bytes)\n", asset.GetID(), entry.Tag, name, asset.GetSize())
		report(int64(asset.GetSize()))
	}
	return totals, nil
}

// markReleases records only releases actually verified in this successful
// attempt. The private marker becomes public listing metadata only after
// publication.
func (g *githubSync) markReleases(ctx context.Context, assets []selectedAsset) error {
	verifiedTags := map[string]bool{}
	for _, entry := range assets {
		verifiedTags[entry.Tag] = true
	}
	for tag := range verifiedTags {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join(g.task.Directory, tag)
		parent, err := os.Stat(dir)
		if err != nil {
			return err
		}
		stamp := []byte(time.Now().UTC().Format(time.RFC3339Nano))
		if err := config.AtomicWrite(filepath.Join(dir, releaseMarker), stamp, 0600); err != nil {
			return err
		}
		// This private timestamp does not change public directory metadata.
		if err := os.Chtimes(dir, parent.ModTime(), parent.ModTime()); err != nil {
			return err
		}
	}
	return nil
}

// prune removes releases outside the selection and files inside kept
// releases that are no longer selected assets or their identity sidecars.
func (g *githubSync) prune(ctx context.Context, assets []selectedAsset, kept map[string]bool) error {
	selected := map[string]map[string]bool{}
	for _, entry := range assets {
		if selected[entry.Tag] == nil {
			selected[entry.Tag] = map[string]bool{releaseMarker: true}
		}
		name := entry.Asset.GetName()
		selected[entry.Tag][name] = true
		selected[entry.Tag][filepath.Base(identityFile(name))] = true
	}
	for tag := range kept {
		dir := filepath.Join(g.task.Directory, tag)
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return &Failure{Code: "INVALID_ASSET", Message: "Release 目录必须为真实目录"}
		}
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if selected[tag][entry.Name()] {
				continue
			}
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	entries, err := os.ReadDir(g.task.Directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && !kept[entry.Name()] && !strings.HasPrefix(entry.Name(), ".") {
			if err := os.RemoveAll(filepath.Join(g.task.Directory, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// Public serving hides these paths; accepting them also lets an asset collide
// with another asset's partial file or its private identity sidecar.
func safeGitHubName(name string) bool {
	return name != "" && name != "." && name != ".." && path.Base(name) == name &&
		!strings.ContainsAny(name, "\\\x00\r\n") && !strings.HasPrefix(name, ".mirrordock") &&
		!strings.HasSuffix(name, ".part") && name != ".rsync-partial" && name != ".~tmp~"
}

func githubError(err error, proxy string) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if limit, ok := errors.AsType[*github.RateLimitError](err); ok {
		return &Failure{
			Code:      "GITHUB_RATE_LIMIT",
			Message:   "GitHub API 配额已用尽，等待限流重置后重试",
			Retryable: true,
			RetryAt:   limit.Rate.Reset.Time,
		}
	}
	if limit, ok := errors.AsType[*github.AbuseRateLimitError](err); ok {
		retryAt := time.Now().Add(time.Minute)
		if limit.RetryAfter != nil {
			retryAt = time.Now().Add(*limit.RetryAfter)
		}
		return &Failure{
			Code:      "GITHUB_RATE_LIMIT",
			Message:   "GitHub API 触发临时限流，请稍后重试",
			Retryable: true,
			RetryAt:   retryAt,
		}
	}
	if response, ok := errors.AsType[*github.ErrorResponse](err); ok && response.Response != nil {
		status := response.Response.StatusCode
		// Do not echo upstream messages: a proxy can return arbitrary text or credentials.
		detail := "请求未成功，请检查 GitHub 服务状态"
		switch status {
		case http.StatusUnauthorized:
			detail = "Token 无效或已过期，请重新配置"
		case http.StatusForbidden:
			detail = "访问被拒绝，请检查 Token 权限或 API 限流"
		case http.StatusNotFound:
			detail = "仓库或附件不存在，或 Token 无权访问"
		case http.StatusProxyAuthRequired:
			detail = "代理要求认证，请检查代理用户名和密码"
		}
		return &Failure{
			Code:      "GITHUB_API",
			Message:   fmt.Sprintf("GitHub API HTTP %d：%s", status, detail),
			Retryable: transientHTTP(status),
		}
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
	} else if networkErr, ok := errors.AsType[net.Error](err); ok && networkErr.Timeout() {
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
		if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr.Op == "proxyconnect" {
			operation += "未能建立代理连接"
		} else {
			operation += "经代理失败"
		}
		proxyURL, _ := config.ParseProxyURL(proxy)
		if proxyURL != nil && proxyURL.Scheme == "https" {
			detail += "；https:// 要求代理入口本身支持 TLS，HTTP/mixed 入口应使用 http://"
		} else {
			detail += "；请确认代理协议、端口及 CONNECT/SOCKS 转发是否可用"
		}
	} else {
		operation += "失败"
	}
	return &Failure{Code: code, Message: operation + "：" + detail, Retryable: retryable}
}

// identityFile is the private sidecar that records target's AssetIdentity.
func identityFile(target string) string {
	return filepath.Join(filepath.Dir(target), ".mirrordock-asset-"+filepath.Base(target)+".json")
}

func sameIdentity(target string, id AssetIdentity) bool {
	recorded, err := os.ReadFile(identityFile(target))
	if err != nil {
		return false
	}
	var old AssetIdentity
	if json.Unmarshal(recorded, &old) != nil {
		return false
	}
	return old == id
}

// verifyAsset checks that target is a regular file of the declared size and,
// when GitHub publishes a SHA-256 digest, that its contents match.
func verifyAsset(ctx context.Context, target string, id AssetIdentity) error {
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("资产不是普通文件")
	}
	file, err := os.Open(target)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != id.Size {
		return errors.New("资产大小不匹配")
	}
	digest, hasDigest := strings.CutPrefix(id.Digest, "sha256:")
	if !hasDigest {
		return nil
	}
	hash := sha256.New()
	buffer := make([]byte, copyBufferBytes)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			_, _ = hash.Write(buffer[:count])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("资产 SHA-256 不匹配")
	}
	return nil
}

const assetIdleTimeout = 5 * time.Minute

var errAssetIdle = errors.New("asset body idle timeout")

// assetDownload describes one asset transfer into target. The body is
// written to target.part first and renamed only after verification.
type assetDownload struct {
	client      *github.Client
	httpClient  *http.Client
	owner, repo string
	identity    AssetIdentity
	target      string
	proxy       string
	// idle aborts the transfer when no body bytes arrive for this long.
	idle time.Duration
	// progress receives the number of bytes of the asset written so far.
	progress func(written int64)
	// received, when set, receives each count of body bytes read from the network.
	received func(n int64)
}

// run reuses a verified local copy, resumes a partial file with Range when
// the server supports it, or downloads the asset from the start. It reports
// whether the existing file was reused.
func (d assetDownload) run(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if sameIdentity(d.target, d.identity) && verifyAsset(ctx, d.target, d.identity) == nil {
		d.progress(d.identity.Size)
		return true, nil
	}
	partial := d.target + ".part"
	for _, name := range []string{d.target, partial} {
		if info, err := os.Lstat(name); err == nil && !info.Mode().IsRegular() {
			return false, &Failure{Code: "INVALID_ASSET", Message: "资产路径必须是普通文件"}
		}
	}
	offset := int64(0)
	if sameIdentity(partial, d.identity) {
		if info, err := os.Stat(partial); err == nil && info.Mode().IsRegular() && info.Size() < d.identity.Size {
			offset = info.Size()
		}
	} else {
		_ = os.Remove(partial)
	}
	identity, err := json.Marshal(d.identity)
	if err != nil {
		return false, err
	}
	// A partial identity must never describe the previous complete asset. This
	// matters when an upstream replaces an asset with another of the same size.
	if err = config.AtomicWrite(identityFile(partial), identity, 0600); err != nil {
		return false, err
	}
	stream, offset, err := d.open(ctx, offset)
	if err != nil {
		return false, err
	}
	defer stream.Close()
	written, err := d.copyBody(ctx, cancel, stream, partial, offset)
	defer func() { d.progress(written) }()
	if err != nil {
		return false, err
	}
	if err = verifyAsset(ctx, partial, d.identity); err != nil {
		return false, &Failure{Code: "ASSET_VERIFY", Message: err.Error()}
	}
	if err = os.Rename(partial, d.target); err != nil {
		return false, err
	}
	if err = config.AtomicWrite(identityFile(d.target), identity, 0600); err != nil {
		return false, err
	}
	_ = os.Remove(identityFile(partial))
	return false, nil
}

// open starts the response body. GitHub either streams the asset directly or
// redirects to a signed URL, which is fetched without the API token. The
// returned offset is where the body continues the partial file.
func (d assetDownload) open(ctx context.Context, offset int64) (io.ReadCloser, int64, error) {
	stream, redirect, err := d.client.Repositories.DownloadReleaseAsset(ctx, d.owner, d.repo, d.identity.ID, nil)
	if err != nil {
		return nil, 0, githubError(err, d.proxy)
	}
	if stream != nil {
		return stream, 0, nil
	}
	if redirect == "" {
		return nil, 0, errors.New("资产下载无响应流")
	}
	if err = config.HTTPURL(redirect); err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, redirect, nil)
	if err != nil {
		return nil, 0, err
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := d.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, githubNetworkError(err, d.proxy, "DOWNLOAD_FAILED", "资产下载")
	}
	resumed := func() bool {
		return response.StatusCode == http.StatusPartialContent &&
			validRange(response.Header.Get("Content-Range"), offset, d.identity.Size)
	}
	if offset > 0 && (response.StatusCode == http.StatusRequestedRangeNotSatisfiable ||
		(response.StatusCode == http.StatusPartialContent && !resumed())) {
		// The server cannot continue this partial file: start over.
		response.Body.Close()
		offset = 0
		request.Header.Del("Range")
		response, err = d.httpClient.Do(request)
		if err != nil {
			return nil, 0, githubNetworkError(err, d.proxy, "DOWNLOAD_FAILED", "资产重新下载")
		}
	}
	switch {
	case offset > 0 && resumed():
		return response.Body, offset, nil
	case response.StatusCode == http.StatusOK:
		return response.Body, 0, nil
	default:
		response.Body.Close()
		return nil, 0, &Failure{
			Code:      "DOWNLOAD_HTTP",
			Message:   fmt.Sprintf("资产下载 HTTP %d", response.StatusCode),
			Retryable: transientHTTP(response.StatusCode),
		}
	}
}

// copyBody appends the stream to partial from offset and returns the file
// length reached. Only network reads consume the idle allowance.
func (d assetDownload) copyBody(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	stream io.Reader,
	partial string,
	offset int64,
) (int64, error) {
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(partial, flags, 0644)
	if err != nil {
		return offset, err
	}
	defer out.Close()
	if _, err = out.Seek(offset, io.SeekStart); err != nil {
		return offset, err
	}
	d.progress(offset)
	written := offset
	buffer := make([]byte, copyBufferBytes)
	lastReport := time.Time{}
	for {
		if err = ctx.Err(); err != nil {
			return written, err
		}
		fired := make(chan struct{})
		timer := time.AfterFunc(d.idle, func() {
			cancel(errAssetIdle)
			close(fired)
		})
		count, readErr := stream.Read(buffer)
		if !timer.Stop() {
			<-fired
		}
		if errors.Is(context.Cause(ctx), errAssetIdle) {
			return written, &Failure{Code: "DOWNLOAD_IDLE", Message: "资产正文长时间无数据，已保存暂存文件", Retryable: true}
		}
		if count > 0 {
			if d.received != nil {
				d.received(int64(count))
			}
			if written+int64(count) > d.identity.Size {
				return written, &Failure{Code: "ASSET_SIZE", Message: "下载超过声明大小"}
			}
			stored, err := out.Write(buffer[:count])
			written += int64(stored)
			if err != nil {
				return written, err
			}
			if time.Since(lastReport) > time.Second {
				d.progress(written)
				lastReport = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return written, ctx.Err()
			}
			return written, githubNetworkError(readErr, d.proxy, "DOWNLOAD_FAILED", "资产响应流中断，已保存暂存文件")
		}
	}
	if written < d.identity.Size {
		return written, &Failure{Code: "DOWNLOAD_FAILED", Message: "资产响应流提前结束，已保存暂存文件", Retryable: true}
	}
	if err = out.Sync(); err != nil {
		return written, err
	}
	return written, out.Close()
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
