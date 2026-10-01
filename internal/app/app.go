package app

import (
	"context"
	"crypto/tls"
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
	"strconv"
	"strings"
	"sync"
	"time"
)

type App struct {
	connections *connections
	workers     sync.WaitGroup

	Config        *config.Manager
	Store         *database.Store
	Mirrors       *mirrors.Manager
	Runs          *runs.Engine
	Notifications *notifications.Service
	auth          *auth
	location      *time.Location
	lock          *os.File
	Server        *http.Server
	once          sync.Once
	sse           chan struct{}
	closing       chan struct{}
}

func Open(file string) (*App, error) {
	cfg, err := config.Open(file)
	if err != nil {
		return nil, err
	}
	return OpenConfig(cfg)
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
	fail := func() { lock.Close() }
	store, err := database.Open(data)
	if err != nil {
		fail()
		return nil, err
	}
	m, err := mirrors.New(config.Resolve(cfg.File(), c.MirrorRoot), store, c.Sources)
	if err != nil {
		store.Close()
		fail()
		return nil, err
	}
	location := startupLocation()
	engine, err := runs.New(cfg, store, m, syncer.SystemRunner{}, location)
	if err != nil {
		m.Close()
		store.Close()
		fail()
		return nil, err
	}
	auth, err := newAuth(c.Auth)
	if err != nil {
		engine.Close()
		m.Close()
		store.Close()
		fail()
		return nil, err
	}
	notifier, err := notifications.New(cfg, store)
	if err != nil {
		engine.Close()
		m.Close()
		store.Close()
		fail()
		return nil, err
	}
	engine.ConfigValidator = notifier.ValidateConfig
	engine.ConfigApplied = notifier.Sync
	a := &App{Config: cfg, Store: store, Mirrors: m, Runs: engine, Notifications: notifier, auth: auth, location: location, lock: lock, sse: make(chan struct{}, 8), closing: make(chan struct{}), connections: &connections{entries: map[net.Conn]*connection{}, limit: connectionLimit()}}
	m.ListingInfo = func(id string) mirrors.SourceListingInfo {
		info := mirrors.SourceListingInfo{Next: engine.ScheduleInfo()[id]}
		latest, _, err := engine.List("", id, 1, 0)
		if err == nil && len(latest) > 0 {
			info.State = latest[0].State
			info.Last = latest[0].EndedAt
			if info.Last.IsZero() {
				completed, _, err := engine.List("succeeded", id, 1, 0)
				if err == nil && len(completed) > 0 {
					info.Last = completed[0].EndedAt
				}
			}
		}
		return info
	}
	a.Server = &http.Server{Addr: net.JoinHostPort(c.Listen, strconv.Itoa(c.Port)), Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ConnState: a.connections.state, ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError)}
	a.workers.Go(a.watchConnections)
	return a, nil
}
func (a *App) Serve() error {
	a.Notifications.Start()
	a.Runs.Start()
	c := a.Config.Startup()
	if c.TLS.Cert != "" {
		return a.Server.ListenAndServeTLS(config.Resolve(a.Config.File(), c.TLS.Cert), config.Resolve(a.Config.File(), c.TLS.Key))
	}
	return a.Server.ListenAndServe()
}
func (a *App) Close(ctx context.Context) error {
	var err error
	a.once.Do(func() {
		close(a.closing)
		a.workers.Wait()
		a.Runs.Close()
		a.Notifications.Close()
		err = a.Server.Shutdown(ctx)
		if err != nil {
			_ = a.Server.Close()
		}
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
	if a.auth.limited() {
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
	http.SetCookie(w, &http.Cookie{Name: "mirrordock_session", Value: token, Path: "/api/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	respond(w, 200, map[string]any{"ok": true, "username": a.auth.username})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	a.auth.logout(r)
	http.SetCookie(w, &http.Cookie{Name: "mirrordock_session", Value: "", Path: "/api/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
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
	if err := a.Runs.ApplyConfig(body.Config, body.Revision, body.TokenUpdate, body.ProxyUpdate); err != nil {
		failure(w, err)
		return
	}
	_ = a.Store.SetState("last_config_change", time.Now().UTC().Format(time.RFC3339))
	respond(w, 200, a.Config.View())
}
func (a *App) reload(w http.ResponseWriter, r *http.Request) {
	if err := a.Runs.Reload(); err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, a.Config.View())
}
func (a *App) listRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
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
	rows, total, err := a.Runs.List(q.Get("state"), q.Get("source"), limit, max(0, offset), database.RunFilter{From: from, Trigger: q.Get("trigger")})
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": rows, "total": total})
}
func (a *App) getRun(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	run, err := a.Runs.Get(id)
	if err != nil {
		respond(w, 404, map[string]string{"error": "记录不存在"})
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
	count, err := a.Runs.ClearLogs()
	if err != nil {
		failure(w, err)
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
	respond(w, 200, map[string]any{
		"timezone": a.location.String(), "utc_offset_seconds": offset, "version": "0.1.0",
		"go": strings.TrimPrefix(runtime.Version(), "go"), "os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(),
		"tls": c.TLS.Cert != "", "publications": a.Mirrors.Publications(), "downloads": a.Mirrors.Stats(), "schedules": a.Runs.ScheduleInfo(),
		"deployment": map[string]any{
			"listen": net.JoinHostPort(c.Listen, strconv.Itoa(c.Port)), "data": data,
			"database": filepath.Join(data, "data.sqlite"), "mirror_root": a.Mirrors.Root,
			"log": map[string]string{"level": c.Log.Level, "format": c.Log.Format, "path": resolveOptional(c.Log.Path)},
			"tls": map[string]string{"cert": resolveOptional(c.TLS.Cert), "key": resolveOptional(c.TLS.Key)},
		},
	})
}
func (a *App) checks(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	for _, name := range []string{"rsync", "dnf"} {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		b, err := exec.CommandContext(ctx, name, "--version").CombinedOutput()
		cancel()
		text := string(b)
		if len(text) > 2048 {
			text = text[:2048]
		}
		out[name] = map[string]any{"available": err == nil, "version": text}
	}
	out["config_writable"] = checkWritable(filepath.Dir(a.Config.File()))
	out["mirror_writable"] = checkWritable(a.Mirrors.Root)
	out["github_token_configured"] = a.Config.View().TokenConfigured
	out["github_proxy_configured"] = a.Config.View().ProxyConfigured
	respond(w, 200, out)
}
func checkWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".mirrordock-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	select {
	case a.sse <- struct{}{}:
		defer func() { <-a.sse }()
	default:
		respond(w, 503, map[string]string{"error": "事件连接数量已满"})
		return
	}
	f, ok := w.(http.Flusher)
	if !ok {
		respond(w, 500, map[string]string{"error": "SSE unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		items, _, err := a.Runs.List("", "", 50, 0)
		if err != nil {
			return
		}
		b, err := json.Marshal(map[string]any{"runs": items, "downloads": a.Mirrors.Stats(), "publications": a.Mirrors.Publications(), "schedules": a.Runs.ScheduleInfo()})
		if err != nil {
			return
		}
		if _, err = fmt.Fprintf(w, "event: progress\ndata: %s\n\n", b); err != nil {
			return
		}
		f.Flush()
		select {
		case <-a.closing:
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
func (a *App) frontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "method not allowed", 405)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
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
