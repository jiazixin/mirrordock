package app

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"io/fs"
	"log/slog"
	"mirrordock/internal/config"
	"mirrordock/internal/database"
	"mirrordock/internal/mirrors"
	"mirrordock/internal/notifications"
	"mirrordock/internal/runs"
	syncer "mirrordock/internal/sync"
	webui "mirrordock/web"
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
)

// maxEventStreams bounds concurrent SSE connections of the admin console.
const maxEventStreams = 8

type App struct {
	connections *connections
	workers     sync.WaitGroup
	serveMu     sync.Mutex

	Version       string
	Config        *config.Manager
	Store         *database.Store
	Mirrors       *mirrors.Manager
	Runs          *runs.Engine
	Notifications *notifications.Service
	auth          *auth
	location      *time.Location
	// lock holds the exclusive flock on the data directory.
	lock      *os.File
	Server    *http.Server
	closeOnce sync.Once
	// progressFrames caches the admin SSE progress payload; listingFrames
	// caches the per-source state shown in the public directory.
	progressFrames progressFrames
	listingFrames  progressFrames
	// eventStreams is a semaphore bounding concurrent SSE connections.
	eventStreams chan struct{}
	closing      chan struct{}
}

// OpenConfig uses the configuration already validated during process startup.
func OpenConfig(cfg *config.Manager) (*App, error) {
	c := cfg.Startup()
	data := config.Resolve(cfg.File(), c.Path)
	if err := os.MkdirAll(data, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(data, ".mirrordock.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("该数据目录已被另一程序实例占用")
	}
	// Resources opened so far are released in reverse order on failure.
	cleanups := []func(){func() { lock.Close() }}
	fail := func(err error) (*App, error) {
		for _, cleanup := range slices.Backward(cleanups) {
			cleanup()
		}
		return nil, err
	}
	store, err := database.Open(data)
	if err != nil {
		return fail(err)
	}
	cleanups = append(cleanups, func() { store.Close() })
	m, err := mirrors.New(config.Resolve(cfg.File(), c.MirrorRoot), store, c.Sources)
	if err != nil {
		return fail(err)
	}
	cleanups = append(cleanups, func() { m.Close() })
	// A previous instance killed with SIGKILL may leave executor descendants
	// writing into task work directories. Stop them before any run recovers.
	workRoot := filepath.Join(m.Root, ".mirrordock", "work")
	if count, err := syncer.TerminateStrays(workRoot); err != nil {
		slog.Error("terminate leftover executor processes", "count", count, "error", err)
	} else if count > 0 {
		slog.Warn("terminated leftover executor processes", "count", count)
	}
	location := startupLocation()
	engine, err := runs.New(cfg, store, m, syncer.SystemRunner{}, location)
	if err != nil {
		return fail(err)
	}
	cleanups = append(cleanups, engine.Close)
	auth, err := newAuth(c.Auth)
	if err != nil {
		return fail(err)
	}
	notifier, err := notifications.New(cfg, store)
	if err != nil {
		return fail(err)
	}
	engine.ConfigValidator = notifier.ValidateConfig
	engine.ConfigApplied = notifier.Sync
	a := &App{
		Version:       "dev",
		Config:        cfg,
		Store:         store,
		Mirrors:       m,
		Runs:          engine,
		Notifications: notifier,
		auth:          auth,
		location:      location,
		lock:          lock,
		eventStreams:  make(chan struct{}, maxEventStreams),
		closing:       make(chan struct{}),
		connections:   &connections{entries: map[net.Conn]*connection{}, limit: connectionLimit()},
	}
	m.ListingInfo = a.listingInfo
	a.Server = &http.Server{
		Addr:              net.JoinHostPort(c.Listen, strconv.Itoa(c.Port)),
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		ConnState:         a.connections.state,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}
	a.workers.Go(a.watchConnections)
	return a, nil
}

// listingInfo reports the latest state, last success and next schedule of
// each source for the public directory, shared through a one-second frame.
func (a *App) listingInfo(ctx context.Context) map[string]mirrors.SourceListingInfo {
	encoded, err := a.listingFrames.get(ctx, a.readListingInfo)
	if err != nil {
		return nil
	}
	var infos map[string]mirrors.SourceListingInfo
	_ = json.Unmarshal(encoded, &infos)
	return infos
}

func (a *App) readListingInfo(ctx context.Context) ([]byte, error) {
	latest, err := a.Runs.Latest(ctx)
	if err != nil {
		return nil, err
	}
	schedules := a.Runs.ScheduleInfo()
	infos := map[string]mirrors.SourceListingInfo{}
	for _, run := range latest {
		infos[run.SourceID] = mirrors.SourceListingInfo{
			State: run.State,
			Last:  run.EndedAt,
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
		completed, err := a.Store.LatestSuccessfulRunsContext(queryCtx, unfinished)
		cancel()
		if err != nil {
			return nil, err
		}
		for _, document := range completed {
			var run runs.Run
			if err := json.Unmarshal(document, &run); err != nil {
				return nil, err
			}
			info := infos[run.SourceID]
			info.Last = run.EndedAt
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

func (a *App) Serve() error {
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
	defer listener.Close()
	a.Notifications.Start()
	a.Runs.Start()
	a.serveMu.Unlock()
	unlock = false
	if c.TLS.Cert != "" {
		return a.Server.ServeTLS(listener, "", "")
	}
	return a.Server.Serve(listener)
}

func (a *App) Close(ctx context.Context) error {
	var err error
	a.closeOnce.Do(func() {
		a.serveMu.Lock()
		close(a.closing)
		a.serveMu.Unlock()
		a.workers.Wait()
		err = a.Server.Shutdown(ctx)
		if err != nil {
			_ = a.Server.Close()
		}
		a.Runs.Close()
		a.Notifications.Close()
		err = errors.Join(err, a.Mirrors.Close(), a.Store.Close(), a.lock.Close())
	})
	return err
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	api := http.NewServeMux()
	api.HandleFunc("GET /api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		authorized := a.auth.authorized(r)
		status := map[string]any{"authenticated": authorized}
		if authorized {
			status["username"] = a.auth.username
		}
		respond(w, 200, status)
	})
	api.HandleFunc("POST /api/auth/login", a.login)
	api.HandleFunc("POST /api/auth/logout", a.logout)
	api.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, a.Config.View()) })
	api.HandleFunc("PUT /api/config", a.saveConfig)
	api.HandleFunc("POST /api/config/reload", a.reload)
	api.HandleFunc("GET /api/analytics", a.analytics)
	api.HandleFunc("GET /api/runs", a.listRuns)
	api.HandleFunc("GET /api/runs/{id}", a.getRun)
	api.HandleFunc("POST /api/sources/{id}/run", a.enqueue)
	api.HandleFunc("POST /api/runs/{id}/{action}", a.action)
	api.HandleFunc("GET /api/runs/{id}/logs", a.logs)
	api.HandleFunc("POST /api/logs/clear", a.clearLogs)
	api.HandleFunc("GET /api/events", a.events)
	api.HandleFunc("GET /api/system/info", a.info)
	api.HandleFunc("GET /api/system/checks", a.checks)
	a.notificationRoutes(api)
	api.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 404, map[string]string{"error": "接口不存在"})
	})
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		select {
		case <-a.closing:
			respond(w, 503, map[string]string{"error": "服务正在关闭"})
			return
		default:
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != "GET" && r.Method != "HEAD" && !csrf(r) {
			respond(w, 403, map[string]string{"error": "请求未通过同源校验"})
			return
		}
		public := r.URL.Path == "/api/auth/status" || r.URL.Path == "/api/auth/login"
		if !public && !a.auth.authorized(r) {
			respond(w, 401, map[string]string{"error": "请先登录"})
			return
		}
		api.ServeHTTP(w, r)
		if !public && r.Method != "GET" && r.Method != "HEAD" {
			// Authenticated writes may create or change runs; the next SSE frame
			// must not replay a cached snapshot from before the write. Anonymous
			// login attempts never trigger a fresh query.
			a.progressFrames.invalidate()
		}
	}))
	mux.HandleFunc("/admin/", a.frontend)
	mux.HandleFunc("/favicon.ico", a.frontend)
	mux.Handle("/", a.Mirrors)
	return mux
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.MarshalWrite(w, value); err != nil {
		slog.Error("write JSON", "error", err)
	}
}

func decode(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	return json.UnmarshalRead(r.Body, out, json.RejectUnknownMembers(true))
}

func failure(w http.ResponseWriter, err error) {
	status := 400
	if errors.Is(err, config.ErrConflict) {
		status = 409
	}
	respond(w, status, map[string]string{"error": err.Error()})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if a.auth.limited(clientIP(r)) {
		respond(w, 429, map[string]string{"error": "尝试过多，一分钟后重试"})
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		failure(w, err)
		return
	}
	token, err := a.auth.login(body.Username, body.Password)
	if err != nil {
		respond(w, 401, map[string]string{"error": err.Error()})
		return
	}
	a.auth.resetLimit(clientIP(r))
	http.SetCookie(w, sessionCookieFor(r, token, int(sessionLifetime/time.Second)))
	respond(w, 200, map[string]any{"ok": true, "username": a.auth.username})
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
	respond(w, 200, map[string]bool{"ok": true})
}

func (a *App) saveConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Config      config.Config `json:"config"`
		Revision    string        `json:"revision"`
		TokenUpdate *string       `json:"token_update"`
		ProxyUpdate *string       `json:"proxy_update"`
	}
	if err := decode(w, r, &body); err != nil {
		failure(w, err)
		return
	}
	err := a.Runs.ApplyConfig(body.Config, body.Revision, body.TokenUpdate, body.ProxyUpdate)
	if _, partial := errors.AsType[*runs.PartialApplyError](err); err != nil && !partial {
		failure(w, err)
		return
	}
	_ = a.Store.SetState("last_config_change", time.Now().UTC().Format(time.RFC3339))
	a.configApplied(w, err)
}

func (a *App) reload(w http.ResponseWriter, r *http.Request) {
	err := a.Runs.Reload()
	if _, partial := errors.AsType[*runs.PartialApplyError](err); err != nil && !partial {
		failure(w, err)
		return
	}
	a.configApplied(w, err)
}

// configApplied returns the adopted revision even when a follow-up step failed,
// so the editor does not keep a stale revision for an already written YAML.
func (a *App) configApplied(w http.ResponseWriter, warning error) {
	result := struct {
		config.View
		Warning string `json:"warning,omitempty"`
	}{View: a.Config.View()}
	if warning != nil {
		slog.Error("apply configuration", "error", warning)
		result.Warning = syncer.CleanLog(warning.Error())
	}
	respond(w, 200, result)
}

func (a *App) listRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("latest") == "1" {
		rows, err := a.Runs.Latest(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"items": rows, "total": len(rows)})
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	limit = min(100, max(1, limit))
	if q.Get("limit") == "" {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	from := q.Get("from")
	if from != "" {
		parsed, err := time.Parse(time.RFC3339, from)
		if err != nil {
			failure(w, err)
			return
		}
		from = parsed.UTC().Format(time.RFC3339Nano)
	}
	filter := database.RunFilter{From: from, Trigger: q.Get("trigger")}
	if key := q.Get("range"); key != "" {
		start, end, err := runTimeRange(key, time.Now(), a.location)
		if err != nil {
			failure(w, err)
			return
		}
		filter.From = start.UTC().Format(time.RFC3339Nano)
		if !end.IsZero() {
			filter.Until = end.UTC().Format(time.RFC3339Nano)
		}
	}
	rows, total, err := a.Runs.ListContext(r.Context(), q.Get("state"), q.Get("source"), limit, max(0, offset), filter)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": rows, "total": total})
}

func (a *App) getRun(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	run, err := a.Runs.GetContext(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond(w, 404, map[string]string{"error": "记录不存在"})
		} else {
			respond(w, 503, map[string]string{"error": "记录查询暂时不可用，请重试"})
		}
		return
	}
	respond(w, 200, run)
}

func (a *App) enqueue(w http.ResponseWriter, r *http.Request) {
	run, err := a.Runs.Enqueue(r.PathValue("id"), "manual")
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 201, run)
}

func (a *App) action(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.Runs.Action(id, r.PathValue("action")); err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	attempt, _ := strconv.Atoi(r.URL.Query().Get("attempt"))
	if r.URL.Query().Get("download") == "1" {
		file, size, err := a.Runs.LogSnapshot(id, attempt)
		if err != nil {
			failure(w, err)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="run-%d-attempt-%d.log"`, id, attempt))
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		if _, err := io.CopyN(w, file, size); err != nil {
			slog.Debug("log download stopped", "error", err)
		}
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	text, next, err := a.Runs.Log(id, attempt, offset)
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"text": text, "next_offset": next})
}

func (a *App) clearLogs(w http.ResponseWriter, r *http.Request) {
	count, err := a.Runs.ClearLogsContext(r.Context())
	if err != nil {
		// Report removals that already happened even when cleanup stopped.
		respond(w, http.StatusServiceUnavailable, map[string]any{
			"error":   syncer.CleanLog(err.Error()),
			"cleared": count,
		})
		return
	}
	respond(w, 200, map[string]int{"cleared": count})
}

func (a *App) info(w http.ResponseWriter, r *http.Request) {
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
	respond(w, 200, map[string]any{
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
		"schedules":          a.Runs.ScheduleInfo(),
		"deployment":         deployment,
	})
}

func (a *App) checks(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	for _, name := range []string{"rsync", "dnf", "rpmkeys"} {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		output, err := exec.CommandContext(ctx, name, "--version").CombinedOutput()
		cancel()
		text := string(output)
		if len(text) > 2048 {
			text = text[:2048]
		}
		out[name] = map[string]any{"available": err == nil, "version": text}
	}
	out["config_writable"] = checkWritable(filepath.Dir(a.Config.File()))
	out["mirror_writable"] = checkWritable(a.Mirrors.Root)
	out["reflink"] = a.Mirrors.ProbeReflink(r.Context())
	out["storage"] = a.Mirrors.StorageInfo()
	out["publication_issues"] = a.Mirrors.Issues()
	out["github_token_configured"] = a.Config.View().TokenConfigured
	out["github_proxy_configured"] = a.Config.View().ProxyConfigured
	respond(w, 200, out)
}

func checkWritable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".mirrordock-check-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return true
}

func (a *App) events(w http.ResponseWriter, r *http.Request) {
	select {
	case a.eventStreams <- struct{}{}:
		defer func() { <-a.eventStreams }()
	default:
		respond(w, 503, map[string]string{"error": "事件连接数量已满"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		respond(w, 500, map[string]string{"error": "SSE unavailable"})
		return
	}
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
		if _, err = fmt.Fprintf(w, "event: progress\ndata: %s\n\n", frame); err != nil {
			return
		}
		flusher.Flush()
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
		"schedules":    a.Runs.ScheduleInfo(),
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
	contents := webui.Files()
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
	defer file.Close()
	st, err := file.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	reader, ok := file.(io.ReadSeeker)
	if !ok {
		http.Error(w, "embedded file unavailable", 500)
		return
	}
	http.ServeContent(w, r, name, st.ModTime(), reader)
}
