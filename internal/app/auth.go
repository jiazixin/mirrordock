package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"maps"
	"mirrordock/internal/config"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Session and login limits.
const (
	sessionCookie      = "mirrordock_session"
	sessionLifetime    = 12 * time.Hour
	maxSessions        = 32
	loginWindowLength  = time.Minute
	loginsPerWindow    = 10
	maxLoginWindows    = 4096
	bcryptCost         = 12
	maxPasswordBytes   = 72
	ipv6SubscriberBits = 64
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

// randomToken returns 256 bits from crypto/rand, URL-safe encoded.
func randomToken() string {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(token)
}

// newAuth keeps only a bcrypt hash of the YAML password in memory.
func newAuth(credentials config.Auth) (*auth, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(credentials.Password), bcryptCost)
	if err != nil {
		return nil, err
	}
	return &auth{
		username:     credentials.Username,
		passwordHash: hash,
		sessions:     map[string]session{},
		attempts:     map[string]loginWindow{},
	}, nil
}

func (a *auth) authorized(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.sessions[cookie.Value]
	if ok && time.Now().Before(current.expires) {
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
		if prefix, err := address.Prefix(ipv6SubscriberBits); err == nil {
			return prefix.String()
		}
	}
	return address.String()
}

// limited counts one login attempt for ip and reports whether the address
// exceeded its allowance in the current window.
func (a *auth) limited(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	maps.DeleteFunc(a.attempts, func(_ string, window loginWindow) bool {
		return now.Sub(window.start) >= loginWindowLength
	})
	window, exists := a.attempts[ip]
	if !exists {
		// A full table evicts the oldest window instead of refusing every new
		// client; otherwise address spraying could lock the administrator out.
		if len(a.attempts) >= maxLoginWindows {
			var oldest string
			for key, candidate := range a.attempts {
				if oldest == "" || candidate.start.Before(a.attempts[oldest].start) {
					oldest = key
				}
			}
			delete(a.attempts, oldest)
		}
		window.start = now
	}
	window.attempts++
	a.attempts[ip] = window
	return window.attempts > loginsPerWindow
}

func (a *auth) resetLimit(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.attempts, ip)
}

// login verifies the credentials and opens a session. The password hash is
// always compared first, so the response time does not reveal the username.
func (a *auth) login(username, password string) (string, error) {
	if len(password) > maxPasswordBytes {
		return "", errors.New("用户名或密码错误")
	}
	passwordMatches := bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) == nil
	usernameMatches := subtle.ConstantTimeCompare([]byte(a.username), []byte(username)) == 1
	if !passwordMatches || !usernameMatches {
		return "", errors.New("用户名或密码错误")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	maps.DeleteFunc(a.sessions, func(_ string, existing session) bool {
		return now.After(existing.expires)
	})
	if len(a.sessions) >= maxSessions {
		var oldest string
		var expiry time.Time
		for token, existing := range a.sessions {
			if oldest == "" || existing.expires.Before(expiry) {
				oldest = token
				expiry = existing.expires
			}
		}
		delete(a.sessions, oldest)
	}
	token := randomToken()
	a.sessions[token] = session{expires: now.Add(sessionLifetime)}
	return token, nil
}

func (a *auth) logout(r *http.Request) {
	cookie, err := r.Cookie(sessionCookie)
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
