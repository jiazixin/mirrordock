// Package app owns the MirrorDock service lifecycle: it wires configuration,
// storage, mirrors, runs and notifications, and serves the admin console and
// the public mirrors over HTTP.
package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"mirrordock/internal/config"
	"mirrordock/internal/database"
	mirrorfs "mirrordock/internal/mirror"
	"mirrordock/internal/notification"
	runengine "mirrordock/internal/run"
	"mirrordock/internal/syncer"
	"mirrordock/internal/timefmt"
	"mirrordock/web"
)

// maxEventStreams bounds concurrent SSE connections of the admin console.
const maxEventStreams = 8

// App is the running MirrorDock service and owns every component it starts.
type App struct {
	connections *connections
	workers     sync.WaitGroup
	serveMu     sync.Mutex
	// idempotency serializes keyed requests with cancellable acquisition.
	idempotency chan struct{}

	Version       string
	Config        *config.Manager
	Store         *database.Store
	Mirrors       *mirrorfs.Manager
	Runs          *runengine.Engine
	Notifications *notification.Service
	auth          *auth
	logger        *slog.Logger
	location      *time.Location
	// lock holds the exclusive flock on the data directory.
	lock        *os.File
	Server      *http.Server
	closeOnce   sync.Once
	cleanupOnce sync.Once
	cleanupErr  error
	// progressFrames caches the admin SSE progress payload; listingFrames
	// caches the per-source state shown in the public directory.
	progressFrames progressFrames
	listingFrames  progressFrames
	// eventStreams is a semaphore bounding concurrent SSE connections.
	eventStreams chan struct{}
	closing      chan struct{}
}

// OpenConfig uses the configuration already validated during process startup.
func OpenConfig(ctx context.Context, cfg *config.Manager, logger *slog.Logger, version string) (*App, error) {
	c := cfg.Startup()
	data := config.Resolve(cfg.File(), c.Path)
	if err := os.MkdirAll(data, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(data, ".mirrordock.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Join(errors.New("the data directory is locked by another running instance"), lock.Close())
	}
	// Resources opened so far are released in reverse order on failure.
	cleanups := []func() error{lock.Close}
	fail := func(err error) (*App, error) {
		for _, cleanup := range slices.Backward(cleanups) {
			err = errors.Join(err, cleanup())
		}
		return nil, err
	}
	store, err := database.Open(ctx, data)
	if err != nil {
		return fail(err)
	}
	cleanups = append(cleanups, store.Close)
	m, err := mirrorfs.New(ctx, mirrorfs.NewOptions{Root: config.Resolve(cfg.File(), c.MirrorRoot), Store: store, Sources: c.Sources, Logger: logger})
	if err != nil {
		return fail(err)
	}
	cleanups = append(cleanups, m.Close)
	// A previous instance killed with SIGKILL may leave executor descendants
	// writing into task work directories. Stop them before any run recovers.
	workRoot := filepath.Join(m.Root, ".mirrordock", "work")
	if count, err := syncer.TerminateStrays(ctx, workRoot); err != nil {
		logger.Error("stray_executors_terminate_failed", "count", count, "error", err)
	} else if count > 0 {
		logger.Warn("stray_executors_terminated", "count", count)
	}
	location := startupLocation()
	engine, err := runengine.New(ctx, runengine.NewOptions{Config: cfg, Store: store, Mirror: m, Runner: syncer.SystemRunner{}, Location: location, Logger: logger})
	if err != nil {
		return fail(err)
	}
	cleanups = append(cleanups, func() error {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), database.OperationTimeout)
		defer cancel()
		return engine.Shutdown(shutdownCtx)
	})

	auth, err := newAuth(c.Auth)
	if err != nil {
		return fail(err)
	}
	notifier, err := notification.New(ctx, cfg, store, logger)
	if err != nil {
		return fail(err)
	}
	engine.ConfigValidator = notifier.ValidateConfig
	engine.ConfigApplied = notifier.Sync
	a := &App{
		Version:       version,
		logger:        logger,
		Config:        cfg,
		Store:         store,
		Mirrors:       m,
		Runs:          engine,
		Notifications: notifier,
		auth:          auth,
		location:      location,
		lock:          lock,
		eventStreams:  make(chan struct{}, maxEventStreams),
		idempotency:   make(chan struct{}, 1),
		closing:       make(chan struct{}),
		connections:   &connections{entries: map[net.Conn]*connection{}, limit: connectionLimit()},
	}
	m.ListingInfo = a.listingInfo
	a.Server = &http.Server{
		Addr:              net.JoinHostPort(c.Listen, strconv.Itoa(c.Port)),
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		WriteTimeout:      time.Minute,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		ConnState:         a.connections.state,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	return a, nil
}

// listingInfo reports the latest state, last success and next schedule of
// each source for the public directory, shared through a one-second frame.
func (a *App) listingInfo(ctx context.Context) map[string]mirrorfs.SourceListingInfo {
	encoded, err := a.listingFrames.get(ctx, a.readListingInfo)
	if err != nil {
		return nil
	}
	var infos map[string]mirrorfs.SourceListingInfo
	if err := json.Unmarshal(encoded, &infos); err != nil {
		a.logger.Error("listing_frame_decode_failed", "error", err)
		return nil
	}
	return infos
}

func (a *App) readListingInfo(ctx context.Context) ([]byte, error) {
	latest, err := a.Runs.Latest(ctx)
	if err != nil {
		return nil, err
	}
	schedules := a.Runs.ScheduleInfo()
	infos := map[string]mirrorfs.SourceListingInfo{}
	for _, run := range latest {
		infos[run.SourceID] = mirrorfs.SourceListingInfo{
			State: run.State,
			Last:  run.EndedAt.Time(),
			Next:  schedules[run.SourceID],
		}
	}
	// A source whose latest run is still active shows its last success.
	unfinished := []string{}
	for id, info := range infos {
		if info.Last.IsZero() {
			unfinished = append(unfinished, id)
		}
	}
	if len(unfinished) > 0 {
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		completed, err := a.Store.LatestSuccessfulRuns(queryCtx, unfinished)
		cancel()
		if err != nil {
			return nil, err
		}
		for _, document := range completed {
			var run runengine.Run
			if err := json.Unmarshal(document, &run); err != nil {
				return nil, err
			}
			info := infos[run.SourceID]
			info.Last = run.EndedAt.Time()
			infos[run.SourceID] = info
		}
	}
	for id, next := range schedules {
		info := infos[id]
		info.Next = next
		infos[id] = info
	}
	return json.Marshal(infos)
}

// Serve listens on the configured addresses and serves until Close is called;
// it returns http.ErrServerClosed once the service is closing.
func (a *App) Serve(ctx context.Context) error {
	a.serveMu.Lock()
	select {
	case <-a.closing:
		a.serveMu.Unlock()
		return http.ErrServerClosed
	default:
	}
	unlock := true
	defer func() {
		if unlock {
			a.serveMu.Unlock()
		}
	}()
	c := a.Config.Startup()
	// Load certificates and bind before any queued task can leave the queue.
	if c.TLS.Cert != "" {
		certFile := config.Resolve(a.Config.File(), c.TLS.Cert)
		keyFile := config.Resolve(a.Config.File(), c.TLS.Key)
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return err
		}
		a.Server.TLSConfig.Certificates = []tls.Certificate{certificate}
	}
	listener, err := net.Listen("tcp", a.Server.Addr)
	if err != nil {
		return err
	}
	defer listener.Close() // Serve owns the listener and closes it on return.
	a.logger.Info("service_listening", "listen", listener.Addr().String(), "tls", c.TLS.Cert != "")
	if err := a.Notifications.Start(ctx); err != nil {
		return err
	}
	if err := a.Runs.Start(ctx); err != nil {
		return err
	}
	a.workers.Go(a.watchConnections)
	a.serveMu.Unlock()
	unlock = false
	if c.TLS.Cert != "" {
		return a.Server.ServeTLS(listener, "", "")
	}
	return a.Server.Serve(listener)
}

// Close stops accepting requests, waits for background workers, shuts the
// server down within ctx and closes every component. Only the first call has an
// effect; a timed-out call can be retried to finish resource cleanup.
func (a *App) Close(ctx context.Context) error {
	a.closeOnce.Do(func() {
		a.serveMu.Lock()
		close(a.closing)
		a.serveMu.Unlock()
	})
	// The connection watcher exits immediately on closing and owns no I/O.
	a.workers.Wait()
	err := a.Server.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, a.Server.Close())
	}
	runErr := a.Runs.Shutdown(ctx)
	notificationErr := a.Notifications.Shutdown(ctx)
	if runErr != nil || notificationErr != nil {
		// A timed-out caller may retry Close. Never release storage underneath
		// a worker still saving its final result.
		return errors.Join(err, runErr, notificationErr)
	}
	a.cleanupOnce.Do(func() { a.cleanupErr = errors.Join(a.Mirrors.Close(), a.Store.Close(), a.lock.Close()) })
	return errors.Join(err, a.cleanupErr)
}

func (a *App) serveSession(w http.ResponseWriter, r *http.Request) {
	a.writeSession(w, a.auth.authorized(r))
}

// writeSession reports whether the caller is signed in and, if so, as whom.
func (a *App) writeSession(w http.ResponseWriter, authenticated bool) {
	session := struct {
		Authenticated bool   `json:"authenticated"`
		Username      string `json:"username,omitzero"`
	}{Authenticated: authenticated}
	if authenticated {
		session.Username = a.auth.username
	}
	respond(w, http.StatusOK, session)
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if a.auth.limited(clientIP(r)) {
		w.Header().Set("Retry-After", strconv.Itoa(int(loginWindowLength.Seconds())))
		writeError(w, r, errLoginLimited)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	token, err := a.auth.login(body.Username, body.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	a.auth.resetLimit(clientIP(r))
	http.SetCookie(w, sessionCookieFor(r, token, int(sessionLifetime/time.Second)))
	a.writeSession(w, true)
}

// sessionCookieFor scopes the session to the API, hides it from scripts and
// never sends it cross-site. A negative maxAge deletes the cookie.
func sessionCookieFor(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/api/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	}
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	a.auth.logout(r)
	http.SetCookie(w, sessionCookieFor(r, "", -1))
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) serveConfig(w http.ResponseWriter, _ *http.Request) {
	view := a.Config.View()
	setETag(w, view.Revision)
	respond(w, http.StatusOK, view)
}

func (a *App) saveConfig(w http.ResponseWriter, r *http.Request) {
	revision, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body struct {
		Config      config.Config `json:"config"`
		TokenUpdate *string       `json:"token_update,omitzero"`
		ProxyUpdate *string       `json:"proxy_update,omitzero"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	err = a.Runs.ApplyConfig(r.Context(), runengine.ApplyConfigOptions{Config: body.Config, Revision: revision, Token: body.TokenUpdate, Proxy: body.ProxyUpdate})
	if _, partial := errors.AsType[*runengine.PartialApplyError](err); err != nil && !partial {
		writeError(w, r, err)
		return
	}
	if err := a.Store.SetState(r.Context(), "last_config_change", timefmt.From(time.Now()).String()); err != nil {
		requestLogger(r).Error("config_audit_failed", "error", err)
	}
	a.configApplied(w, r, err)
}

func (a *App) reload(w http.ResponseWriter, r *http.Request) {
	err := a.Runs.Reload(r.Context())
	if _, partial := errors.AsType[*runengine.PartialApplyError](err); err != nil && !partial {
		writeError(w, r, err)
		return
	}
	a.configApplied(w, r, err)
}

// configApplied returns the adopted revision even when a follow-up step failed,
// so the editor does not keep a stale revision for an already written YAML.
func (a *App) configApplied(w http.ResponseWriter, r *http.Request, warning error) {
	result := struct {
		config.View
		Warning *apiErrorBody `json:"warning,omitzero"`
	}{View: a.Config.View()}
	if warning != nil {
		requestLogger(r).Error("config_apply_incomplete", "error", warning)
		result.Warning = &apiErrorBody{
			Code:    "config_apply_incomplete",
			Message: "configuration adopted, but a follow-up step failed",
			Details: map[string]any{"detail": syncer.CleanLog(warning.Error())},
		}
	}
	setETag(w, result.Revision)
	respond(w, http.StatusOK, result)
}

// runQuery is the parsed query of GET /api/runengine.
type runQuery struct {
	latest        bool
	state, source string
	limit, offset int
	filter        database.RunFilter
}

func parseRunQuery(r *http.Request, location *time.Location) (runQuery, error) {
	q := r.URL.Query()
	query := runQuery{state: q.Get("state"), source: q.Get("source")}
	switch q.Get("latest") {
	case "":
	case "true":
		query.latest = true
	default:
		return query, &config.FieldError{Path: "latest", Code: "invalid_boolean", Reason: "must be true"}
	}
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		return query, err
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil {
		return query, err
	}
	if limit < 1 || limit > 100 {
		return query, &config.FieldError{Path: "limit", Code: "out_of_range", Reason: "must be between 1 and 100"}
	}
	if offset < 0 {
		return query, &config.FieldError{Path: "offset", Code: "out_of_range", Reason: "must not be negative"}
	}
	query.limit, query.offset = int(limit), int(offset)
	query.filter.Trigger = q.Get("trigger")
	if from := q.Get("from"); from != "" {
		parsed, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return query, &config.FieldError{Path: "from", Code: "invalid_time", Reason: "must be an RFC 3339 time"}
		}
		query.filter.From = parsed.UTC().Format(time.RFC3339Nano)
	}
	if key := q.Get("range"); key != "" {
		start, end, err := runTimeRange(key, time.Now(), location)
		if err != nil {
			return query, err
		}
		query.filter.From = start.UTC().Format(time.RFC3339Nano)
		if !end.IsZero() {
			query.filter.Until = end.UTC().Format(time.RFC3339Nano)
		}
	}
	return query, nil
}

func (a *App) listRuns(w http.ResponseWriter, r *http.Request) {
	query, err := parseRunQuery(r, a.location)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if query.latest {
		rows, err := a.Runs.Latest(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		respond(w, http.StatusOK, map[string]any{"items": rows, "total": len(rows), "limit": len(rows), "offset": 0})
		return
	}
	rows, total, err := a.Runs.List(r.Context(), runengine.ListOptions{State: query.state, Source: query.source, Limit: query.limit, Offset: query.offset, Filters: []database.RunFilter{query.filter}})
	if err != nil {
		writeError(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"items": rows, "total": total, "limit": query.limit, "offset": query.offset})
}

func (a *App) getRun(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	run, err := a.Runs.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, r, errRunNotFound)
		} else {
			requestLogger(r).Error("run_query_failed", "run_id", id, "error", err)
			writeError(w, r, errQueryFailed)
		}
		return
	}
	respond(w, http.StatusOK, run)
}

func (a *App) enqueue(w http.ResponseWriter, r *http.Request) {
	run, err := a.Runs.Enqueue(r.Context(), r.PathValue("id"), "manual", requestIdentity(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	respond(w, http.StatusCreated, run)
}

func (a *App) action(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.Runs.Action(r.Context(), id, r.PathValue("action")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	attempt, err := queryInt(r, "attempt", 0)
	if err != nil {
		writeError(w, r, err)
		return
	}
	offset, err := queryInt(r, "offset_bytes", 0)
	if err != nil {
		writeError(w, r, err)
		return
	}
	download := r.URL.Query().Get("download")
	if r.URL.Query().Has("download") && download != "true" && download != "false" {
		writeError(w, r, &config.FieldError{Path: "download", Code: "invalid_value", Reason: "must be true or false"})
		return
	}
	if download == "true" {
		file, size, err := a.Runs.LogSnapshot(r.Context(), id, int(attempt))
		if err != nil {
			writeError(w, r, err)
			return
		}
		defer file.Close() // Read-only descriptor; closing cannot discard buffered writes.
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			writeError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="run-%d-attempt-%d.log"`, id, attempt))
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		if _, err := io.CopyN(w, file, size); err != nil {
			requestLogger(r).Debug("log_download_stopped", "error", err)
		}
		return
	}
	text, next, err := a.Runs.Log(r.Context(), id, int(attempt), offset)
	if errors.Is(err, runengine.ErrLogCleared) {
		respond(w, http.StatusOK, map[string]any{"text": "", "next_offset_bytes": next, "cleared": true})
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"text": text, "next_offset_bytes": next})
}

func (a *App) clearLogs(w http.ResponseWriter, r *http.Request) {
	count, err := a.Runs.ClearLogs(r.Context())
	if err != nil {
		// Report removals that already happened even when cleanup stopped.
		requestLogger(r).Error("log_cleanup_failed", "error", err, "cleared", count)
		failure := errorResponse(r, err)
		if failure.status == http.StatusInternalServerError {
			failure = newFailure(http.StatusServiceUnavailable, "log_cleanup_failed", "log cleanup stopped")
		}
		writeError(w, r, failure.with("cleared", count))
		return
	}
	respond(w, http.StatusOK, map[string]int{"cleared": count})
}

func (a *App) systemInfo(w http.ResponseWriter, r *http.Request) {
	_, offset := time.Now().In(a.location).Zone()
	c := a.Config.Startup()
	file := a.Config.File()
	data := config.Resolve(file, c.Path)
	resolveOptional := func(value string) string {
		if value == "" {
			return ""
		}
		return config.Resolve(file, value)
	}
	deployment := map[string]any{
		"listen":      net.JoinHostPort(c.Listen, strconv.Itoa(c.Port)),
		"data":        data,
		"database":    filepath.Join(data, "data.sqlite"),
		"mirror_root": a.Mirrors.Root,
		"log": map[string]string{
			"level":  c.Log.Level,
			"format": c.Log.Format,
			"path":   resolveOptional(c.Log.Path),
		},
		"tls": map[string]string{
			"cert": resolveOptional(c.TLS.Cert),
			"key":  resolveOptional(c.TLS.Key),
		},
	}
	respond(w, http.StatusOK, map[string]any{
		"timezone":           a.location.String(),
		"utc_offset_seconds": offset,
		"version":            a.Version,
		"go":                 strings.TrimPrefix(runtime.Version(), "go"),
		"os":                 runtime.GOOS,
		"arch":               runtime.GOARCH,
		"cpus":               runtime.NumCPU(),
		"tls":                c.TLS.Cert != "",
		"publications":       a.Mirrors.Publications(),
		"downloads":          a.Mirrors.Stats(),
		"schedules":          a.scheduleTimes(),
		"deployment":         deployment,
	})
}

func (a *App) checks(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	for _, name := range []string{"rsync", "dnf", "rpmkeys"} {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		command := exec.CommandContext(ctx, name, "--version")
		command.WaitDelay = time.Second
		var output versionOutput
		command.Stdout, command.Stderr = &output, &output
		err := command.Run()
		cancel()
		text := output.String()
		out[name] = map[string]any{"available": err == nil, "version": text}
	}
	out["config_writable"] = checkWritable(filepath.Dir(a.Config.File()))
	out["mirror_writable"] = checkWritable(a.Mirrors.Root)
	out["reflink"] = a.Mirrors.ProbeReflink(r.Context())
	out["storage"] = a.Mirrors.StorageInfo(r.Context())
	out["publication_issues"] = a.Mirrors.Issues()
	out["github_token_configured"] = a.Config.View().TokenConfigured
	out["github_proxy_configured"] = a.Config.View().ProxyConfigured
	respond(w, http.StatusOK, out)
}

func checkWritable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".mirrordock-check-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	return errors.Join(probe.Close(), os.Remove(name)) == nil
}

func (a *App) events(w http.ResponseWriter, r *http.Request) {
	select {
	case a.eventStreams <- struct{}{}:
		defer func() { <-a.eventStreams }()
	default:
		writeError(w, r, errStreamsFull)
		return
	}
	controller := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if !a.auth.authorized(r) {
			return
		}
		changed := a.progressFrames.changes()
		frame, err := a.progressFrames.get(r.Context(), a.readProgress)
		if err != nil {
			return
		}
		// Each frame gets the server's write timeout; the stream itself is open-ended.
		if err := controller.SetWriteDeadline(time.Now().Add(time.Minute)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			requestLogger(r).Debug("event_deadline_failed", "error", err)
			return
		}
		if _, err = fmt.Fprintf(w, "event: progress\ndata: %s\n\n", frame); err != nil {
			return
		}
		if err := controller.Flush(); err != nil {
			requestLogger(r).Debug("event_flush_failed", "error", err)
			return
		}
		select {
		case <-a.closing:
			return
		case <-r.Context().Done():
			return
		case <-changed:
		case <-ticker.C:
		}
	}
}

// readProgress builds one SSE frame shared by every connected client.
func (a *App) readProgress(ctx context.Context) ([]byte, error) {
	items, err := a.Runs.Latest(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"runs":         items,
		"downloads":    a.Mirrors.Stats(),
		"publications": a.Mirrors.Publications(),
		"schedules":    a.scheduleTimes(),
	})
}

// adminContentSecurityPolicy allows only the embedded bundle. Inline styles
// stay allowed for Vue style bindings; scripts must come from the bundle.
var adminContentSecurityPolicy = strings.Join([]string{
	"default-src 'self'",
	"script-src 'self'",
	"style-src 'self' 'unsafe-inline'",
	"img-src 'self' data:",
	"font-src 'self'",
	"connect-src 'self'",
	"frame-ancestors 'none'",
	"base-uri 'self'",
	"form-action 'self'",
}, "; ")

func (a *App) frontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", adminContentSecurityPolicy)
	name := strings.TrimPrefix(r.URL.Path, "/admin/")
	if r.URL.Path == "/favicon.ico" {
		name = "favicon.ico"
	}
	if name == "" {
		name = "index.html"
	}
	if path.Clean(name) != name || strings.HasPrefix(name, "../") {
		http.NotFound(w, r)
		return
	}
	contents, err := web.Files()
	if err != nil {
		http.Error(w, "embedded files unavailable", http.StatusInternalServerError)
		return
	}
	if _, err := fs.Stat(contents, name); err != nil {
		if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	// FileServer redirects /index.html to ./, which loops when a SPA fallback
	// rewrites the URL. Serve the embedded file directly with the original URL.
	file, err := contents.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close() // Read-only descriptor; closing cannot discard buffered writes.
	st, err := file.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	reader, ok := file.(io.ReadSeeker)
	if !ok {
		http.Error(w, "embedded file unavailable", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, st.ModTime(), reader)
}

func (a *App) scheduleTimes() map[string]timefmt.Time {
	result := map[string]timefmt.Time{}
	for id, at := range a.Runs.ScheduleInfo() {
		result[id] = timefmt.From(at)
	}
	return result
}

// versionOutput drains command pipes while retaining a bounded diagnostic prefix.
type versionOutput struct{ bytes.Buffer }

func (b *versionOutput) Write(p []byte) (int, error) {
	const maximum = 2048
	_, _ = b.Buffer.Write(p[:min(len(p), maximum-b.Len())]) // bytes.Buffer.Write cannot fail
	return len(p), nil
}
