package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"mirrordock/internal/database"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type session struct{ expires time.Time }
type auth struct {
	mu           sync.Mutex
	store        *database.Store
	setupCode    string
	passwordHash string
	sessions     map[string]session
	attempts     int
	window       time.Time
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func newAuth(store *database.Store) (*auth, error) {
	hash, err := store.State("admin_password")
	if err != nil {
		return nil, err
	}
	a := &auth{store: store, passwordHash: hash, sessions: map[string]session{}}
	if hash == "" {
		a.setupCode = randomToken()
	}
	return a, nil
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
func (a *auth) initialized() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.passwordHash != "" }
func (a *auth) limited() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if now.Sub(a.window) > time.Minute {
		a.window = now
		a.attempts = 0
	}
	a.attempts++
	return a.attempts > 10
}
func (a *auth) setup(code, password string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.passwordHash != "" || subtle.ConstantTimeCompare([]byte(a.setupCode), []byte(code)) != 1 {
		return errors.New("初始化码无效或已完成初始化")
	}
	if utf8.RuneCountInString(password) < 12 || len(password) > 72 {
		return errors.New("密码至少 12 个字符，最多 72 字节")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	if err = a.store.SetState("admin_password", string(hash)); err != nil {
		return err
	}
	a.passwordHash = string(hash)
	a.setupCode = ""
	return nil
}
func (a *auth) login(password string) (string, error) {
	a.mu.Lock()
	hash := a.passwordHash
	a.mu.Unlock()
	if hash == "" || len(password) > 72 || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", errors.New("密码错误")
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
