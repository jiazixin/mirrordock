package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"mirrordock/internal/config"
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

// passwordHashCost is bcryptCost in production. TestMain lowers it because a
// cost-12 comparison takes seconds under the race detector, which made
// parallel login tests exceed their HTTP client timeouts on CI.
var passwordHashCost = bcryptCost

type session struct{ expires time.Time }

type loginWindow struct {
	attempts int
	start    time.Time
}

// auth protects session digests and login windows with mu; now is fixed at construction.
type auth struct {
	now          func() time.Time
	mu           sync.Mutex
	username     string
	passwordHash []byte
	sessions     map[[sha256.Size]byte]session
	attempts     map[string]loginWindow
}

// newAuth keeps only a bcrypt hash of the YAML password in memory.
func newAuth(credentials config.Auth) (*auth, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(credentials.Password), passwordHashCost)
	if err != nil {
		return nil, err
	}
	return &auth{
		now:          time.Now,
		username:     credentials.Username,
		passwordHash: hash,
		sessions:     map[[sha256.Size]byte]session{},
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
	digest := sha256.Sum256([]byte(cookie.Value))
	now := a.now()
	for stored, current := range a.sessions {
		if !now.Before(current.expires) {
			delete(a.sessions, stored)
			continue
		}
		if subtle.ConstantTimeCompare(digest[:], stored[:]) == 1 {
			return true
		}
	}
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
	now := a.now()
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
		return "", errBadLogin
	}
	passwordMatches := bcrypt.CompareHashAndPassword(a.passwordHash, []byte(password)) == nil
	usernameMatches := subtle.ConstantTimeCompare([]byte(a.username), []byte(username)) == 1
	if !passwordMatches || !usernameMatches {
		return "", errBadLogin
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	maps.DeleteFunc(a.sessions, func(_ [sha256.Size]byte, existing session) bool {
		return now.After(existing.expires)
	})
	if len(a.sessions) >= maxSessions {
		var oldest [sha256.Size]byte
		var expiry time.Time
		for token, existing := range a.sessions {
			if expiry.IsZero() || existing.expires.Before(expiry) {
				oldest = token
				expiry = existing.expires
			}
		}
		delete(a.sessions, oldest)
	}
	token := rand.Text()
	a.sessions[sha256.Sum256([]byte(token))] = session{expires: now.Add(sessionLifetime)}
	return token, nil
}

func (a *auth) logout(r *http.Request) {
	cookie, err := r.Cookie(sessionCookie)
	if err == nil {
		a.mu.Lock()
		delete(a.sessions, sha256.Sum256([]byte(cookie.Value)))
		a.mu.Unlock()
	}
}

// String prevents accidental credential disclosure in diagnostics.
func (a *auth) String() string { return "auth{redacted}" }

// GoString redacts authentication data in Go-syntax diagnostics.
func (a *auth) GoString() string { return a.String() }

// LogValue redacts authentication data in structured logging.
func (a *auth) LogValue() slog.Value { return slog.StringValue(a.String()) }
