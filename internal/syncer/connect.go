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
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"mirrordock/internal/config"
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
		return nil, &Failure{Code: "rsync_proxy", Message: err.Error(), Details: map[string]any{"reason": "invalid_proxy"}, ProxyFailure: true}
	}
	target, err := rsyncTarget(t.Candidate)
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, &Failure{Code: "rsync_proxy", Message: "cannot locate the MirrorDock executable for the proxy helper: " + err.Error(), Details: map[string]any{"reason": "executable_missing", "detail": err.Error()}}
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
		return "", &Failure{Code: "invalid_source", Message: "connecting through a proxy needs an rsync:// upstream", Details: map[string]any{"reason": "proxy_needs_rsync_url"}}
	}
	port := cmp.Or(address.Port(), defaultRsyncPort)
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return "", &Failure{Code: "invalid_source", Message: "rsync upstream port must be from 1 to 65535", Details: map[string]any{"reason": "invalid_port"}}
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
func RunConnect(ctx context.Context) int {
	conn, err := connectFromEnv(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, connectMarker+err.Error())
		return 1
	}
	// This helper exclusively owns process stdin. Closing both ends releases
	// either relay when its peer exits or the parent cancels the process.
	stop := context.AfterFunc(ctx, func() { conn.Close(); os.Stdin.Close() })
	defer stop()
	var workers sync.WaitGroup
	workers.Go(func() {
		_, _ = io.Copy(conn, os.Stdin)
		// rsync closes its end only once it is finished or gone. Pass the EOF
		// on and give the daemon a short grace to close, so the helper never
		// outlives rsync while holding its stderr open.
		if half, ok := conn.(interface{ CloseWrite() error }); ok {
			_ = half.CloseWrite()
		}
		_ = conn.SetReadDeadline(time.Now().Add(connectLinger))
	})
	// Relay errors are left unclassified: rsync reports a broken connection
	// itself, and its diagnosis must not be replaced by the helper's.
	_, _ = io.Copy(os.Stdout, conn)
	// Shutdown errors cannot change rsync's own protocol result.
	_ = conn.Close()
	_ = os.Stdin.Close()
	workers.Wait()
	return 0
}

// connectLinger bounds how long the helper waits for the daemon to close
// after rsync has closed its end.
const connectLinger = 5 * time.Second

func connectFromEnv(ctx context.Context) (net.Conn, error) {
	proxy, err := config.ParseProxyField("rsync.proxy", os.Getenv(connectProxyEnv))
	if err != nil || proxy == nil {
		return nil, connectError(connectInvalid, "invalid_proxy", "invalid proxy address")
	}
	target := os.Getenv(connectTargetEnv)
	_, portText, err := net.SplitHostPort(target)
	if port, convErr := strconv.Atoi(portText); err != nil || convErr != nil || port < 1 || port > 65535 {
		return nil, connectError(connectInvalid, "invalid_target", "invalid upstream address")
	}
	timeout, err := strconv.Atoi(os.Getenv(connectTimeoutEnv))
	if err != nil || timeout < 1 {
		return nil, connectError(connectInvalid, "invalid_timeout", "invalid connect timeout")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	return dialProxy(ctx, proxy, target, nil)
}

// connectFailure is a classified helper diagnostic: kind decides retries,
// reason is the code clients map to text, message is English. Its text never
// contains the proxy URL or credentials, only the proxy host and port.
type connectFailure struct {
	kind, reason, message string
}

func (e *connectFailure) Error() string { return e.kind + ": " + e.reason + ": " + e.message }

func connectError(kind, reason, message string) error {
	return &connectFailure{kind: kind, reason: reason, message: message}
}

// dialProxy opens a tunnel to target through proxy within ctx. tlsConfig is
// used for an https:// proxy; nil verifies the proxy against system roots.
func dialProxy(ctx context.Context, proxy *url.URL, target string, tlsConfig *tls.Config) (net.Conn, error) {
	address := proxyAddress(proxy)
	conn, err := (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, connectError(connectProxyUnreachable, "proxy_unreachable", "cannot connect to proxy "+address+" ("+networkReason(ctx, err)+")")
	}
	// The deadline bounds every handshake step; the relay itself has none.
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close() // Failed handshake; no buffered output needs flushing.
			return nil, connectError(connectProxyUnreachable, "proxy_handshake_failed", "cannot set proxy handshake deadline")
		}
	}
	// Capture the TCP connection before conn becomes the TLS wrapper. Closing
	// it interrupts every handshake without racing with that reassignment.
	raw := conn
	stop := context.AfterFunc(ctx, func() { raw.Close() })
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
				return nil, connectError(connectProxyUnreachable, "proxy_tls_timeout", "TLS handshake with proxy "+address+" timed out")
			}
			return nil, connectError(connectProxyProtocol, "proxy_tls_failed", "TLS handshake with proxy "+address+" failed ("+err.Error()+")")
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
		if ctx.Err() != nil {
			return nil, connectError(connectProxyUnreachable, "proxy_handshake_timeout", "handshake with proxy "+address+" was interrupted ("+networkReason(ctx, ctx.Err())+")")
		}
		if _, classified := errors.AsType[*connectFailure](err); !classified {
			err = connectError(connectProxyUnreachable, "proxy_handshake_failed", "handshake with proxy "+address+" failed ("+networkReason(ctx, err)+")")
		}
		return nil, err
	}
	if !stop() {
		conn.Close()
		return nil, connectError(connectProxyUnreachable, "proxy_handshake_timeout", "handshake with proxy "+address+" timed out")
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close() // Discard a tunnel whose handshake deadline could not be cleared.
		return nil, connectError(connectProxyUnreachable, "proxy_handshake_failed", "cannot clear proxy handshake deadline")
	}
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
		return "timeout"
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return "proxy DNS lookup failed"
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
		return nil, connectError(connectProxyProtocol, "proxy_bad_response", "proxy "+proxyAddress(proxy)+" returned no valid HTTP response")
	}
	response.Body.Close()
	address := proxyAddress(proxy)
	switch code := response.StatusCode; {
	case code == http.StatusOK:
		return &bufferedConn{Conn: conn, reader: reader}, nil
	case code == http.StatusProxyAuthRequired:
		return nil, connectError(connectProxyAuth, "proxy_auth_required", "proxy "+address+" requires authentication (HTTP 407)")
	case code == http.StatusForbidden:
		return nil, connectError(connectTargetDenied, "target_denied", fmt.Sprintf("proxy %s refused to connect to %s (HTTP 403)", address, target))
	case code >= 500:
		return nil, connectError(connectTargetFailed, "target_failed", fmt.Sprintf("proxy %s cannot connect to %s (HTTP %d)", address, target, code))
	default:
		return nil, connectError(connectProxyProtocol, "connect_unsupported", fmt.Sprintf("proxy %s does not support CONNECT to %s (HTTP %d)", address, target, code))
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
		return nil, connectError(connectProxyProtocol, "not_socks5", "proxy "+address+" is not a SOCKS5 proxy")
	}
	switch reply[1] {
	case 0x00:
	case 0x02:
		if proxy.User == nil {
			return nil, connectError(connectProxyAuth, "socks_auth_required", "SOCKS5 proxy "+address+" requires a username and password")
		}
		if err := socksAuthenticate(conn, proxy, address); err != nil {
			return nil, err
		}
	default:
		return nil, connectError(connectProxyAuth, "socks_auth_unsupported", "SOCKS5 proxy "+address+" accepts no available authentication method")
	}
	host, portText, _ := net.SplitHostPort(target)
	port, _ := strconv.Atoi(portText)
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip == nil {
		if len(host) > 255 {
			return nil, connectError(connectInvalid, "target_host_too_long", "upstream host name too long")
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
		return nil, connectError(connectProxyProtocol, "socks_bad_response", "SOCKS5 proxy "+address+" returned an invalid response")
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
		return nil, connectError(connectProxyProtocol, "socks_bad_response", "SOCKS5 proxy "+address+" returned an invalid address type")
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
		return connectError(connectProxyAuth, "socks_auth_failed", "SOCKS5 proxy "+address+" rejected the credentials")
	}
	return nil
}

func socksReplyError(code byte, address, target string) error {
	reasons := map[byte]string{
		0x01: "general proxy failure",
		0x02: "connection not allowed by ruleset",
		0x03: "network unreachable",
		0x04: "host unreachable",
		0x05: "connection refused",
		0x06: "TTL expired",
		0x07: "command not supported",
		0x08: "address type not supported",
	}
	reason := reasons[code]
	if reason == "" {
		reason = fmt.Sprintf("code %d", code)
	}
	message := fmt.Sprintf("SOCKS5 proxy %s cannot connect to %s (%s)", address, target, reason)
	switch code {
	case 0x02:
		return connectError(connectTargetDenied, "target_denied", message)
	case 0x07, 0x08:
		return connectError(connectProxyProtocol, "connect_unsupported", message)
	default:
		return connectError(connectTargetFailed, "target_failed", message)
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
	kind, rest, _ := strings.Cut(detail, ": ")
	reason, message, _ := strings.Cut(rest, ": ")
	failure := &Failure{
		Code:     "rsync_proxy",
		Message:  "proxy connection failed: " + message,
		Details:  map[string]any{"reason": reason, "detail": message},
		ExitCode: exitCode,
	}
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
