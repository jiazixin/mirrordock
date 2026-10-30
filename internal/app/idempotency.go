package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"regexp"
	"time"

	"mirrordock/internal/config"
	"mirrordock/internal/database"
)

// idempotencyWindow is how long the first response to an Idempotency-Key is
// replayed for the API contract.
const idempotencyWindow = database.IdempotencyWindow

var idempotencyKeyPattern = regexp.MustCompile(`^[\x21-\x7e]{1,255}$`)

// idempotent replays the stored first response when a creating request repeats
// its Idempotency-Key, and rejects a key reused for a different request with
// 409 idempotency_key_reused. Keyed requests run one at a time, so a retry
// that races the original waits for the stored result.
func (a *App) idempotent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next(w, r)
			return
		}
		if !idempotencyKeyPattern.MatchString(key) {
			writeError(w, r, &config.FieldError{Path: "Idempotency-Key", Code: "invalid_value", Reason: "must be 1 to 255 visible ASCII characters"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
		if err != nil {
			writeError(w, r, err)
			return
		}
		if err := r.Body.Close(); err != nil {
			writeError(w, r, err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		digest := sha256.New()
		io.WriteString(digest, r.Method+" "+r.URL.Path+"\n")
		digest.Write(body)
		fingerprint := hex.EncodeToString(digest.Sum(nil))

		select {
		case a.idempotency <- struct{}{}:
		case <-r.Context().Done():
			return
		}
		defer func() { <-a.idempotency }()
		now := time.Now()
		stored, found, err := a.Store.IdempotentResponse(r.Context(), key, now.Add(-idempotencyWindow))
		if err != nil {
			writeError(w, r, err)
			return
		}
		if found {
			if stored.Fingerprint != fingerprint {
				writeError(w, r, errKeyReused)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			status := stored.Status
			if status == 0 {
				status = http.StatusCreated
			}
			w.WriteHeader(status)
			_, _ = w.Write(stored.Body)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), requestIdentityKey{}, database.RequestIdentity{Key: key, Fingerprint: fingerprint, CreatedAt: now}))
		capture := &capturingWriter{ResponseWriter: w, status: http.StatusOK}
		next(capture, r)
		// Server failures and rate limits are not final; a retry may succeed.
		if capture.status == http.StatusCreated || capture.status >= http.StatusInternalServerError || capture.status == http.StatusTooManyRequests {
			return
		}
		response := database.IdempotentResponse{Fingerprint: fingerprint, Status: capture.status, Body: capture.body.Bytes()}
		// The resource exists even if the client has gone; keep its result for the retry.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		if err := a.Store.SaveIdempotentResponse(ctx, database.SaveIdempotentResponseOptions{Key: key, Response: response, Now: now, NotBefore: now.Add(-idempotencyWindow)}); err != nil {
			requestLogger(r).Error("idempotency_save_failed", "error", err)
		}
	}
}

// capturingWriter copies the status and body of a response it passes on.
type capturingWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *capturingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *capturingWriter) Write(payload []byte) (int, error) {
	w.body.Write(payload)
	return w.ResponseWriter.Write(payload)
}

// requestIdentity is only set by the keyed creation handler after validation.
type requestIdentityKey struct{}

func requestIdentity(r *http.Request) database.RequestIdentity {
	identity, _ := r.Context().Value(requestIdentityKey{}).(database.RequestIdentity)
	return identity
}
