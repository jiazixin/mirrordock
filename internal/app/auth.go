package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"mirrordock/internal/config"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type session struct{ expires time.Time }
type loginWindow struct {
	attempts int
	start    time.Time
}
type auth struct {
	mu           sync.Mutex
	username     string
	passwordHash []byte
	sessions     map[string]session
	attempts     map[string]loginWindow
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func newAuth(credentials config.Auth) (*auth, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(credentials.Password), 12)
	if err != nil {
		return nil, err
	}
	return &auth{username: credentials.Username, passwordHash: hash, sessions: map[string]session{}, attempts: map[string]loginWindow{}}, nil
}
func (a *auth) authorized(r *http.Request) bool {
	cookie, err := r.Cookie("mirrordock_session")
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[cookie.Value]
	if ok && time.Now().Before(s.expires) {
		return true
	}
	delete(a.sessions, cookie.Value)
	return false
}

// Use the transport peer, never an untrusted forwarding header. Keep buckets
// bounded and expire idle entries so arbitrary client addresses cannot grow RAM.
// IPv6 clients are bucketed by /64, the smallest prefix normally assigned to
// one subscriber, so rotating interface addresses cannot multiply attempts.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	address = address.Unmap()
	if address.Is6() {
		if prefix, err := address.Prefix(64); err == nil {
			return prefix.String()
		}
	}
	return address.String()
}
func (a *auth) limited(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for key, v := range a.attempts {
		if now.Sub(v.start) >= time.Minute {
			delete(a.attempts, key)
		}
	}
	v, exists := a.attempts[ip]
	if !exists {
		// A full table evicts the oldest window instead of refusing every new
		// client; otherwise address spraying could lock the administrator out.
		if len(a.attempts) >= 4096 {
			var oldest string
			for key, window := range a.attempts {
				if oldest == "" || window.start.Before(a.attempts[oldest].start) {
					oldest = key
				}
			}
			delete(a.attempts, oldest)
		}
		v.start = now
	}
	v.attempts++
	a.attempts[ip] = v
	return v.attempts > 10
}
func (a *auth) resetLimit(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.attempts, ip)
}
func (a *auth) login(username, password string) (string, error) {
	if len(password) > 72 || bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) != nil || subtle.ConstantTimeCompare([]byte(a.username), []byte(username)) != 1 {
		return "", errors.New("用户名或密码错误")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, s := range a.sessions {
		if now.After(s.expires) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 32 {
		var oldest string
		var expiry time.Time
		for k, s := range a.sessions {
			if oldest == "" || s.expires.Before(expiry) {
				oldest = k
				expiry = s.expires
			}
		}
		delete(a.sessions, oldest)
	}
	token := randomToken()
	a.sessions[token] = session{expires: now.Add(12 * time.Hour)}
	return token, nil
}
func (a *auth) logout(r *http.Request) {
	cookie, err := r.Cookie("mirrordock_session")
	if err == nil {
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
	}
}
func csrf(r *http.Request) bool {
	if r.Header.Get("X-MirrorDock-Request") != "1" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return strings.EqualFold(origin, scheme+"://"+r.Host)
}
