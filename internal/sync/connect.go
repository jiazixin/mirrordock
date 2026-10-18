package syncer

import (
	"bufio"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mirrordock/internal/config"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// rsync starts RSYNC_CONNECT_PROG through /bin/sh instead of opening the
// daemon socket itself, and talks the daemon protocol over its stdin/stdout.
// MirrorDock points it at its own rsync-connect command, which reaches the
// daemon through the configured proxy. Everything but the quoted executable
// path travels in the environment, so no configured value is parsed by a
// shell and proxy credentials never appear in process arguments.
const (
	ConnectCommand    = "rsync-connect"
	connectProxyEnv   = "MIRRORDOCK_RSYNC_PROXY"
	connectTargetEnv  = "MIRRORDOCK_RSYNC_TARGET"
	connectTimeoutEnv = "MIRRORDOCK_RSYNC_CONNECT_TIMEOUT"
	// connectMarker prefixes the helper's diagnostic on rsync's stderr.
	connectMarker = "mirrordock rsync-connect: "
)

// Diagnostic kinds the helper reports after connectMarker.
const (
	connectProxyUnreachable = "proxy-unreachable"
	connectProxyAuth        = "proxy-auth"
	connectProxyProtocol    = "proxy-protocol"
	connectTargetFailed     = "target-failed"
	connectTargetDenied     = "target-denied"
	connectInvalid          = "invalid"
)

// rsyncProxyVariables make rsync bypass or replace the configured proxy; they
// are never inherited from the service environment.
var rsyncProxyVariables = []string{"RSYNC_PROXY", "RSYNC_CONNECT_PROG"}

const defaultRsyncPort = "873"

// rsyncEnv is the rsync environment: the service's own without inherited
// rsync proxy variables, plus the helper settings when t.Proxy is set.
func rsyncEnv(t Task) ([]string, error) {
	env := slices.DeleteFunc(processEnv(), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return slices.Contains(rsyncProxyVariables, name) || strings.HasPrefix(name, "MIRRORDOCK_RSYNC_")
	})
	if t.Proxy == "" {
		return env, nil
	}
	if _, err := config.ParseProxyField("rsync.proxy", t.Proxy); err != nil {
		return nil, &Failure{Code: "RSYNC_PROXY", Message: err.Error(), ProxyFailure: true}
	}
	target, err := rsyncTarget(t.Candidate)
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, &Failure{Code: "RSYNC_PROXY", Message: "无法定位 MirrorDock 可执行文件，不能经代理连接: " + err.Error()}
	}
	return append(env,
		"RSYNC_CONNECT_PROG="+connectProgram(executable),
		connectProxyEnv+"="+t.Proxy,
		connectTargetEnv+"="+target,
		connectTimeoutEnv+"="+seconds(t.Settings.Rsync.ConnectTimeout),
	), nil
}

// rsyncTarget is the daemon host:port of an rsync:// candidate.
func rsyncTarget(candidate string) (string, error) {
	address, err := url.Parse(candidate)
	if err != nil || address.Scheme != "rsync" || address.Hostname() == "" {
		return "", &Failure{Code: "INVALID_SOURCE", Message: "经代理连接需要 rsync:// 来源地址"}
	}
	port := cmp.Or(address.Port(), defaultRsyncPort)
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return "", &Failure{Code: "INVALID_SOURCE", Message: "rsync 来源端口必须在 1–65535 之间"}
	}
	return net.JoinHostPort(address.Hostname(), port), nil
}

// connectProgram quotes executable for /bin/sh. rsync expands %H and %% in
// the value before the shell sees it, so a literal % is doubled first.
func connectProgram(executable string) string {
	quoted := "'" + strings.ReplaceAll(executable, "'", `'\''`) + "'"
	return strings.ReplaceAll(quoted, "%", "%%") + " " + ConnectCommand
}

// RunConnect is the rsync-connect command. It connects to the target named in
// its environment through the proxy, then relays stdin and stdout to it until
// the daemon closes the connection.
func RunConnect() int {
	conn, err := connectFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, connectMarker+err.Error())
		return 1
	}
	defer conn.Close()
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		// rsync closes its end only once it is finished or gone. Pass the EOF
		// on and give the daemon a short grace to close, so the helper never
		// outlives rsync while holding its stderr open.
		if half, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = half.CloseWrite()
		}
		_ = conn.SetReadDeadline(time.Now().Add(connectLinger))
	}()
	// Relay errors are left unclassified: rsync reports a broken connection
	// itself, and its diagnosis must not be replaced by the helper's.
	_, _ = io.Copy(os.Stdout, conn)
	return 0
}

// connectLinger bounds how long the helper waits for the daemon to close
// after rsync has closed its end.
const connectLinger = 5 * time.Second

func connectFromEnv() (net.Conn, error) {
	proxy, err := config.ParseProxyField("rsync.proxy", os.Getenv(connectProxyEnv))
	if err != nil || proxy == nil {
		return nil, connectError(connectInvalid, "代理地址无效")
	}
	target := os.Getenv(connectTargetEnv)
	_, portText, err := net.SplitHostPort(target)
	if port, convErr := strconv.Atoi(portText); err != nil || convErr != nil || port < 1 || port > 65535 {
		return nil, connectError(connectInvalid, "上游地址无效")
	}
	timeout, err := strconv.Atoi(os.Getenv(connectTimeoutEnv))
	if err != nil || timeout < 1 {
		return nil, connectError(connectInvalid, "连接超时无效")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	return dialProxy(ctx, proxy, target, nil)
}

// connectFailure is a classified helper diagnostic. Its text never contains
// the proxy URL or credentials, only the proxy host and port.
type connectFailure struct {
	kind, message string
}

func (e *connectFailure) Error() string { return e.kind + ": " + e.message }

func connectError(kind, message string) error { return &connectFailure{kind: kind, message: message} }

// dialProxy opens a tunnel to target through proxy within ctx. tlsConfig is
// used for an https:// proxy; nil verifies the proxy against system roots.
func dialProxy(ctx context.Context, proxy *url.URL, target string, tlsConfig *tls.Config) (net.Conn, error) {
	address := proxyAddress(proxy)
	conn, err := (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, connectError(connectProxyUnreachable, "无法连接代理 "+address+"（"+networkReason(ctx, err)+"）")
	}
	// The deadline bounds every handshake step; the relay itself has none.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	if proxy.Scheme == "https" {
		settings := &tls.Config{ServerName: proxy.Hostname(), MinVersion: tls.VersionTLS12}
		if tlsConfig != nil {
			settings = tlsConfig.Clone()
			settings.ServerName = proxy.Hostname()
		}
		secure := tls.Client(conn, settings)
		if err = secure.HandshakeContext(ctx); err != nil {
			conn.Close()
			if ctx.Err() != nil {
				return nil, connectError(connectProxyUnreachable, "代理 "+address+" TLS 握手超时")
			}
			return nil, connectError(connectProxyProtocol, "代理 "+address+" TLS 握手失败（"+err.Error()+"）；https:// 要求代理入口本身支持 TLS")
		}
		conn = secure
	}
	var tunnel net.Conn
	if proxy.Scheme == "socks5" || proxy.Scheme == "socks5h" {
		tunnel, err = socksConnect(conn, proxy, target)
	} else {
		tunnel, err = httpConnect(conn, proxy, target)
	}
	if err != nil {
		conn.Close()
		if _, classified := errors.AsType[*connectFailure](err); !classified {
			err = connectError(connectProxyUnreachable, "与代理 "+address+" 握手失败（"+networkReason(ctx, err)+"）")
		}
		return nil, err
	}
	if !stop() {
		conn.Close()
		return nil, connectError(connectProxyUnreachable, "与代理 "+address+" 握手超时")
	}
	_ = conn.SetDeadline(time.Time{})
	return tunnel, nil
}

// proxyAddress applies the default port of the proxy scheme.
func proxyAddress(proxy *url.URL) string {
	port := proxy.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443", "socks5": "1080", "socks5h": "1080"}[proxy.Scheme]
	}
	return net.JoinHostPort(proxy.Hostname(), port)
}

func networkReason(ctx context.Context, err error) string {
	if ctx.Err() != nil || errors.Is(err, os.ErrDeadlineExceeded) {
		return "连接超时"
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return "代理域名解析失败"
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr.Err != nil {
		return opErr.Err.Error()
	}
	return err.Error()
}

// httpConnect asks an HTTP proxy for a CONNECT tunnel to target.
func httpConnect(conn net.Conn, proxy *url.URL, target string) (net.Conn, error) {
	request := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: target},
		Host:   target,
		Header: http.Header{},
	}
	if proxy.User != nil {
		password, _ := proxy.User.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(proxy.User.Username() + ":" + password))
		request.Header.Set("Proxy-Authorization", "Basic "+credentials)
	}
	if err := request.Write(conn); err != nil {
		return nil, err
	}
	// The daemon greets as soon as the tunnel opens, so bytes past the
	// response header may already be buffered.
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return nil, err
		}
		return nil, connectError(connectProxyProtocol, "代理 "+proxyAddress(proxy)+" 没有返回有效的 HTTP 响应；请确认代理协议与端口")
	}
	response.Body.Close()
	address := proxyAddress(proxy)
	switch code := response.StatusCode; {
	case code == http.StatusOK:
		return &bufferedConn{Conn: conn, reader: reader}, nil
	case code == http.StatusProxyAuthRequired:
		return nil, connectError(connectProxyAuth, "代理 "+address+" 要求认证（HTTP 407），请检查代理用户名和密码")
	case code == http.StatusForbidden:
		return nil, connectError(connectTargetDenied, fmt.Sprintf("代理 %s 拒绝连接 %s（HTTP 403），请确认代理允许 CONNECT 到该端口", address, target))
	case code >= 500:
		return nil, connectError(connectTargetFailed, fmt.Sprintf("代理 %s 无法连接 %s（HTTP %d）", address, target, code))
	default:
		return nil, connectError(connectProxyProtocol, fmt.Sprintf("代理 %s 不支持 CONNECT 到 %s（HTTP %d）", address, target, code))
	}
}

// bufferedConn reads first from the bytes buffered while parsing a response.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (c *bufferedConn) CloseWrite() error {
	if half, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return nil
}

// socksConnect performs a SOCKS5 CONNECT (RFC 1928) with optional
// username/password authentication (RFC 1929). Like the GitHub transport, both
// socks5 and socks5h pass the host name for the proxy to resolve.
func socksConnect(conn net.Conn, proxy *url.URL, target string) (net.Conn, error) {
	address := proxyAddress(proxy)
	methods := []byte{0x00}
	if proxy.User != nil {
		methods = []byte{0x00, 0x02}
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return nil, err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return nil, err
	}
	if reply[0] != 0x05 {
		return nil, connectError(connectProxyProtocol, "代理 "+address+" 不是 SOCKS5 代理；请确认代理协议与端口")
	}
	switch reply[1] {
	case 0x00:
	case 0x02:
		if proxy.User == nil {
			return nil, connectError(connectProxyAuth, "SOCKS5 代理 "+address+" 要求用户名和密码")
		}
		if err := socksAuthenticate(conn, proxy, address); err != nil {
			return nil, err
		}
	default:
		return nil, connectError(connectProxyAuth, "SOCKS5 代理 "+address+" 不接受可用的认证方式")
	}
	host, portText, _ := net.SplitHostPort(target)
	port, _ := strconv.Atoi(portText)
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip == nil {
		if len(host) > 255 {
			return nil, connectError(connectInvalid, "上游主机名过长")
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, host...)
	} else if ip4 := ip.To4(); ip4 != nil {
		request = append(append(request, 0x01), ip4...)
	} else {
		request = append(append(request, 0x04), ip.To16()...)
	}
	request = append(request, byte(port>>8), byte(port))
	if _, err := conn.Write(request); err != nil {
		return nil, err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	if header[0] != 0x05 {
		return nil, connectError(connectProxyProtocol, "SOCKS5 代理 "+address+" 返回了无效响应")
	}
	if header[1] != 0x00 {
		return nil, socksReplyError(header[1], address, target)
	}
	var bound int
	switch header[3] {
	case 0x01:
		bound = net.IPv4len
	case 0x04:
		bound = net.IPv6len
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return nil, err
		}
		bound = int(length[0])
	default:
		return nil, connectError(connectProxyProtocol, "SOCKS5 代理 "+address+" 返回了无效地址类型")
	}
	if _, err := io.ReadFull(conn, make([]byte, bound+2)); err != nil {
		return nil, err
	}
	return conn, nil
}

func socksAuthenticate(conn net.Conn, proxy *url.URL, address string) error {
	username := proxy.User.Username()
	password, _ := proxy.User.Password()
	request := append([]byte{0x01, byte(len(username))}, username...)
	request = append(append(request, byte(len(password))), password...)
	if _, err := conn.Write(request); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[1] != 0x00 {
		return connectError(connectProxyAuth, "SOCKS5 代理 "+address+" 认证失败，请检查代理用户名和密码")
	}
	return nil
}

func socksReplyError(code byte, address, target string) error {
	reasons := map[byte]string{
		0x01: "代理内部错误",
		0x02: "代理规则不允许该连接",
		0x03: "网络不可达",
		0x04: "主机不可达",
		0x05: "连接被拒绝",
		0x06: "连接超时",
		0x07: "代理不支持 CONNECT",
		0x08: "代理不支持该地址类型",
	}
	reason := reasons[code]
	if reason == "" {
		reason = fmt.Sprintf("代码 %d", code)
	}
	message := fmt.Sprintf("SOCKS5 代理 %s 无法连接 %s（%s）", address, target, reason)
	switch code {
	case 0x02:
		return connectError(connectTargetDenied, message)
	case 0x07, 0x08:
		return connectError(connectProxyProtocol, message)
	default:
		return connectError(connectTargetFailed, message)
	}
}

// connectLineFailure classifies a helper diagnostic printed by rsync. A
// failure of the proxy itself skips the remaining candidates, which would use
// the same proxy; a refused target is an ordinary source failure.
func connectLineFailure(line string, exitCode int) (*Failure, bool) {
	detail, ok := strings.CutPrefix(line, connectMarker)
	if !ok {
		return nil, false
	}
	kind, message, _ := strings.Cut(detail, ": ")
	failure := &Failure{Code: "RSYNC_PROXY", Message: "经代理连接失败: " + message, ExitCode: exitCode}
	switch kind {
	case connectProxyUnreachable:
		failure.ProxyFailure, failure.Retryable = true, true
	case connectProxyAuth, connectProxyProtocol, connectInvalid:
		failure.ProxyFailure = true
	case connectTargetFailed:
		failure.SourceFailure, failure.Retryable = true, true
	case connectTargetDenied:
		failure.SourceFailure = true
	default:
		return nil, false
	}
	return failure, true
}
