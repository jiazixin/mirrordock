package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mirrordock/internal/config"
)

// maxRequestBytes bounds every JSON request body.
const maxRequestBytes = 1 << 20

// capabilities lists the optional features GET /api/info announces.
var capabilities = []string{"analytics", "github_release_sources", "notifications", "reposync_sources"}

// Handler returns the HTTP handler that routes the health check, the admin
// API, the console and the public mirrors.
func (a *App) Handler() http.Handler {
	api := a.apiRoutes()
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", a.middleware(http.HandlerFunc(a.serveHealth)))
	mux.Handle("/api/", a.middleware(a.guardAPI(api)))
	mux.HandleFunc("/admin/", a.frontend)
	mux.HandleFunc("/favicon.ico", a.frontend)
	mux.Handle("/", a.Mirrors)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = a.identifyRequest(w, r)
		mux.ServeHTTP(w, r)
	})
}

// apiRoutes is the admin API route table. GET patterns also match HEAD.
func (a *App) apiRoutes() *http.ServeMux {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/info", a.serveInfo)
	api.HandleFunc("GET /api/auth/session", a.serveSession)
	api.HandleFunc("POST /api/auth/login", a.login)
	api.HandleFunc("POST /api/auth/logout", emptyBody(a.logout))
	api.HandleFunc("GET /api/config", a.serveConfig)
	api.HandleFunc("PUT /api/config", a.saveConfig)
	api.HandleFunc("POST /api/config/reload", emptyBody(a.reload))
	api.HandleFunc("GET /api/analytics", a.analytics)
	api.HandleFunc("GET /api/runs", a.listRuns)
	api.HandleFunc("GET /api/runs/{id}", a.getRun)
	api.HandleFunc("POST /api/sources/{id}/runs", a.idempotent(emptyBody(a.enqueue)))
	api.HandleFunc("POST /api/runs/{id}/{action}", emptyBody(a.action))
	api.HandleFunc("GET /api/runs/{id}/logs", a.logs)
	api.HandleFunc("POST /api/logs/clear", a.clearLogs)
	api.HandleFunc("GET /api/events", a.events)
	api.HandleFunc("GET /api/system/info", a.systemInfo)
	api.HandleFunc("GET /api/system/checks", a.checks)
	a.notificationRoutes(api)
	return api
}

// publicAPIPaths answer without a session.
var publicAPIPaths = map[string]bool{"/api/info": true, "/api/auth/session": true, "/api/auth/login": true}

// guardAPI rejects cross-site writes, requests during shutdown and requests
// without a session before they reach the route table.
func (a *App) guardAPI(api *http.ServeMux) http.Handler {
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, errOriginRejected)
	}))
	return protection.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-a.closing:
			writeError(w, r, errShuttingDown)
			return
		default:
		}
		public := publicAPIPaths[r.URL.Path]
		if !public && !a.auth.authorized(r) {
			writeError(w, r, errLoginRequired)
			return
		}
		recorder, ok := w.(*responseRecorder)
		if !ok {
			recorder = &responseRecorder{ResponseWriter: w}
		}
		api.ServeHTTP(&routerErrorWriter{responseRecorder: recorder, request: r}, r)
		if !public && r.Method != http.MethodGet && r.Method != http.MethodHead {
			// Authenticated writes may create or change runs; the next SSE frame
			// must not replay a cached snapshot from before the write. Anonymous
			// login attempts never trigger a fresh query.
			a.progressFrames.invalidate()
		}
	}))
}

// middleware wraps next in the shared request handling, outermost first:
// request ID, access log, panic recovery and security headers.
func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = a.identifyRequest(w, r)
		logger := requestLogger(r)
		recorder := &responseRecorder{ResponseWriter: w, logger: logger}
		started := time.Now()
		defer logAccess(logAccessOptions{logger: logger, r: r, w: recorder, started: started})
		defer recoverPanic(recorder, r, logger)
		header := recorder.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'")
		header.Set("Cache-Control", "no-store")
		next.ServeHTTP(recorder, r)
	})
}

// identifyRequest also protects public responses without wrapping their file
// writer, so the HTTP/1.1 sendfile path remains available.
func (a *App) identifyRequest(w http.ResponseWriter, r *http.Request) *http.Request {
	if _, ok := r.Context().Value(loggerKey{}).(*slog.Logger); ok {
		return r
	}
	requestID := r.Header.Get("X-Request-ID")
	if !requestIDPattern.MatchString(requestID) {
		requestID = rand.Text()
	}
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	return r.WithContext(context.WithValue(r.Context(), loggerKey{}, a.logger.With("request_id", requestID)))
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

type loggerKey struct{}

// requestLogger returns the logger carrying the request ID.
func requestLogger(r *http.Request) *slog.Logger {
	if logger, ok := r.Context().Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.New(slog.DiscardHandler)
}

// recoverPanic logs a handler panic and answers 500 internal unless the
// response has started. http.ErrAbortHandler keeps aborting the response.
func recoverPanic(w *responseRecorder, r *http.Request, logger *slog.Logger) {
	recovered := recover()
	if recovered == nil {
		return
	}
	if err, ok := recovered.(error); ok && errors.Is(err, http.ErrAbortHandler) {
		panic(recovered)
	}
	logger.Error("http_handler_panic", "method", r.Method, "path", r.URL.Path, "error", fmt.Sprint(recovered))
	if !w.wroteHeader {
		writeError(w, r, errInternal)
	}
}

// logAccess records one API request; health checks log at debug level.
func logAccess(params logAccessOptions) {
	logger := params.logger
	r := params.r
	w := params.w
	started := params.started

	level := slog.LevelInfo
	if r.URL.Path == "/healthz" {
		level = slog.LevelDebug
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	logger.Log(r.Context(), level, "http_request",
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"duration_ms", time.Since(started).Milliseconds(),
		"bytes", w.bytes,
	)
}

// routerErrorWriter replaces the router's plain-text 404 and 405 responses
// with the JSON error body. Handlers write JSON errors themselves.
type routerErrorWriter struct {
	*responseRecorder
	request  *http.Request
	replaced bool
}

func (w *routerErrorWriter) WriteHeader(status int) {
	plainText := strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain")
	if !w.wroteHeader && plainText && (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) {
		w.replaced = true
		w.Header().Del("Content-Length")
		if status == http.StatusNotFound {
			writeError(w.responseRecorder, w.request, errNoRoute)
		} else {
			writeError(w.responseRecorder, w.request, errMethodNotAllowed)
		}
		return
	}
	w.responseRecorder.WriteHeader(status)
}

func (w *routerErrorWriter) Write(payload []byte) (int, error) {
	if w.replaced {
		return len(payload), nil
	}
	return w.responseRecorder.Write(payload)
}

// responseRecorder preserves the optional interfaces used by SSE and file
// downloads while collecting the final status and response size.
type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
	logger      *slog.Logger
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(payload []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	count, err := r.ResponseWriter.Write(payload)
	r.bytes += int64(count)
	if err != nil && r.logger != nil {
		r.logger.Debug("http_write_failed", "error", err)
	}
	return count, err
}

func (r *responseRecorder) ReadFrom(reader io.Reader) (int64, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	count, err := io.Copy(r.ResponseWriter, reader)
	r.bytes += count
	return count, err
}

func (r *responseRecorder) Flush() {
	// http.Flusher cannot return an error; streaming handlers use FlushError.
	if err := r.FlushError(); err != nil && r.logger != nil {
		r.logger.Debug("http_flush_failed", "error", err)
	}
}

func (r *responseRecorder) FlushError() error {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(r.ResponseWriter).Flush()
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func respond(w http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Errorf("encode response: %w", err))
	} // only programmer-controlled DTOs reach this boundary
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// The response recorder records transport errors; no second response is possible.
	_, _ = w.Write(payload)
}

// emptyBody accepts no body or an empty JSON object, rejecting unknown input
// even for command endpoints. Idempotency checks run outside this validator.
func emptyBody(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
		if err != nil {
			writeError(w, r, decodeError(err))
			return
		}
		if len(bytes.TrimSpace(body)) != 0 {
			r.Body = io.NopCloser(bytes.NewReader(body))
			if err := decode(w, r, &struct{}{}); err != nil {
				writeError(w, r, err)
				return
			}
		}
		next(w, r)
	}
}

// decode reads one JSON object from a request body of at most
// maxRequestBytes, rejecting other media types and unknown members.
func decode(w http.ResponseWriter, r *http.Request, out any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errNotJSON
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	defer r.Body.Close() // Request body is read-only; decoding errors are returned.
	var raw jsontext.Value
	if err := json.UnmarshalRead(r.Body, &raw); err != nil {
		return decodeError(err)
	}
	if len(raw) == 0 || raw[0] != '{' {
		return &config.FieldError{Path: "body", Code: "invalid_value", Reason: "must be a JSON object"}
	}
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		return decodeError(err)
	}
	return nil
}

// setETag sends revision as the strong entity tag of the response.
func setETag(w http.ResponseWriter, revision string) {
	w.Header().Set("ETag", strconv.Quote(revision))
}

// ifMatch returns the revision a write is conditioned on. A missing or weak
// If-Match fails the precondition: every configuration write must name the
// revision it was edited from.
func ifMatch(r *http.Request) (string, error) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	revision, err := strconv.Unquote(value)
	if err != nil || revision == "" {
		return "", errIfMatchRequired
	}
	return revision, nil
}

// queryInt parses an optional integer query parameter; a malformed value is
// a 400 invalid_argument instead of a silent default.
func queryInt(r *http.Request, name string, fallback int64) (int64, error) {
	if !r.URL.Query().Has(name) {
		return fallback, nil
	}
	value, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	if err != nil {
		return 0, &config.FieldError{Path: name, Code: "invalid_integer", Reason: "must be an integer"}
	}
	return value, nil
}

// pathID parses the numeric {id} path parameter.
func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, &config.FieldError{Path: "id", Code: "invalid_integer", Reason: "must be a positive integer"}
	}
	return id, nil
}

func (a *App) serveHealth(w http.ResponseWriter, r *http.Request) {
	select {
	case <-a.closing:
		writeError(w, r, errShuttingDown)
	default:
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (a *App) serveInfo(w http.ResponseWriter, _ *http.Request) {
	respond(w, http.StatusOK, map[string]any{"version": a.Version, "capabilities": capabilities})
}

// logAccessOptions groups the inputs to logAccess.
type logAccessOptions struct {
	logger  *slog.Logger
	r       *http.Request
	w       *responseRecorder
	started time.Time
}
