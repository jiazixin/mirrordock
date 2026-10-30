package notification

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mirrordock/internal/config"
	"mirrordock/internal/database"
	runengine "mirrordock/internal/run"
	"mirrordock/internal/syncer"
	"mirrordock/internal/timefmt"
)

func fixture(t *testing.T) (*Service, *config.Manager, *database.Store) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("# notification fixture\nlisten: 127.0.0.1\npath: ./data\nmirror_root: ./mirror\nauth:\n  username: admin\n  password: fixture-password\nsources: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Open(config.LoadSource{Path: file, Explicit: true})
	if err != nil {
		t.Fatal(err)
	}
	store, err := database.Open(t.Context(), filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(t.Context(), cfg, store, slog.New(slog.DiscardHandler))
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { shutdownService(t, service); store.Close() })
	return service, cfg, store
}

func TestRecordPage(t *testing.T) {
	t.Parallel()
	s, _, store := fixture(t)
	for i := range 125 {
		_, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "INSERT INTO notifications(channel,run_id,event,title,body,failure,state,next_at,updated_at) VALUES('webhook',?,'batch.finished','fixture','fixture',0,'sent',0,'2026-10-01T00:00:00.000Z')", args: []any{i + 1}})
		if err != nil {
			t.Fatal(err)
		}
	}
	records, total, err := s.RecordPage(t.Context(), 50, 100)
	if err != nil || total != 125 || len(records) != 25 || records[0].RunID != 25 || records[24].RunID != 1 {
		t.Fatalf("last page: records=%v total=%d err=%v", records, total, err)
	}
	records, total, err = s.RecordPage(t.Context(), 1000, -1)
	if err != nil || total != 125 || len(records) != 100 || records[0].RunID != 125 {
		t.Fatalf("bounded page: records=%d total=%d err=%v", len(records), total, err)
	}
	records, total, err = s.RecordPage(t.Context(), 50, 200)
	if err != nil || total != 125 || len(records) != 0 {
		t.Fatalf("empty page: records=%d total=%d err=%v", len(records), total, err)
	}
}

func saveChannel(params saveChannelOptions) {
	params.t.Helper()
	t := params.t
	s := params.s
	kind := params.kind
	u := params.u

	if err := s.Save(t.Context(), kind, u, s.cfg.View().Revision); err != nil {
		t.Fatal(err)
	}
}

func createRun(params createRunOptions) *runengine.Run {
	params.t.Helper()
	t := params.t
	store := params.store
	state := params.state
	failed := params.failed

	now := time.Now().UTC()
	r := &runengine.Run{State: state, CreatedAt: timefmt.From(now), StartedAt: timefmt.From(now.Add(time.Second)), EndedAt: timefmt.From(now.Add(5 * time.Second)), Name: "fixture", SourceID: "local", Type: "github_release", Dest: "local", Progress: syncer.Progress{Files: 8, Success: 4, Failed: failed, Skipped: 1}}
	if _, err := store.CreateRun(t.Context(), database.CreateRunOptions{Source: "local", State: state, Created: now, Value: r}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCredentialPersistenceAndValidation(t *testing.T) {
	t.Parallel()
	s, cfg, _ := fixture(t)
	endpoint := "https://oapi.dingtalk.com/robot/send?access_token=fixture-sensitive-token"
	saveChannel(saveChannelOptions{t: t, s: s, kind: "dingtalk", u: Update{Endpoint: new(endpoint), Secret: new("SEC-fixture-secret"), Enabled: new(false), OnlyFailure: new(false)}})
	encrypted := cfg.Current().Settings.Notifications["dingtalk"]
	if !strings.HasPrefix(encrypted.Endpoint, "enc:v1:") {
		t.Fatal("plaintext persisted")
	}
	saveChannel(saveChannelOptions{t: t, s: s, kind: "dingtalk", u: Update{Enabled: new(false), OnlyFailure: new(false)}})
	plain, err := s.plain("dingtalk", cfg.Current().Settings.Notifications["dingtalk"])
	if err != nil || plain.Endpoint != endpoint || plain.Secret != "SEC-fixture-secret" {
		t.Fatal("blank did not preserve", err)
	}
	for _, value := range []any{cfg.View(), mustView(t, s)} {
		b, _ := json.Marshal(value)
		if strings.Contains(string(b), "fixture-sensitive") || strings.Contains(string(b), "SEC-fixture") || strings.Contains(string(b), "enc:v1:") {
			t.Fatal("read response exposed credential")
		}
	}
	written, err := os.ReadFile(cfg.File())
	if err != nil || strings.Contains(string(written), "fixture-sensitive") || strings.Contains(string(written), "SEC-fixture") || !strings.Contains(string(written), "# notification fixture") {
		t.Fatal("unsafe YAML", err)
	}
	// General settings saves cannot replace notification settings or decrypt them.
	c := cfg.View().Config
	c.Settings.Notifications = nil
	c.Settings.TaskTimeout = "4h"
	if err := cfg.Save(config.SaveOptions{Config: c, Revision: cfg.View().Revision, TokenUpdate: nil, ProxyUpdate: nil}); err != nil {
		t.Fatal(err)
	}
	if cfg.Current().Settings.Notifications["dingtalk"].Secret == "" {
		t.Fatal("general save lost credentials")
	}
	saveChannel(saveChannelOptions{t: t, s: s, kind: "dingtalk", u: Update{ClearSecret: true, Enabled: new(false), OnlyFailure: new(false)}})
	plain, err = s.plain("dingtalk", cfg.Current().Settings.Notifications["dingtalk"])
	if err != nil || plain.Secret != "" || plain.Endpoint != endpoint {
		t.Fatal("explicit clear failed")
	}
	before := cfg.View().Revision
	if err = s.Save(t.Context(), "dingtalk", Update{Enabled: new(false), OnlyFailure: new(false)}, "stale"); !errors.Is(err, config.ErrConflict) {
		t.Fatal("revision check absent", err)
	}
	if cfg.View().Revision != before {
		t.Fatal("conflict mutated YAML")
	}
	for _, kind := range config.NotificationChannels {
		if err = s.Save(t.Context(), kind, Update{Enabled: new(true), OnlyFailure: new(false)}, cfg.View().Revision); kind != "dingtalk" && err == nil {
			t.Fatal("enabled without credentials", kind)
		}
	}
	for _, test := range []struct {
		kind string
		c    config.Notification
	}{
		{"other", config.Notification{}}, {"webhook", config.Notification{Endpoint: "ftp://host/token"}}, {"webhook", config.Notification{Endpoint: "https://user:secret@host/"}}, {"webhook", config.Notification{Endpoint: "https://host/#secret"}},
		{"wecom", config.Notification{Endpoint: "https://evil/q?key=secret"}}, {"wecom", config.Notification{Endpoint: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send"}},
		{"dingtalk", config.Notification{Endpoint: "https://oapi.dingtalk.com/robot/send?access_token="}}, {"telegram", config.Notification{Token: "secret", ChatID: "-100123"}}, {"telegram", config.Notification{Token: "123:" + strings.Repeat("a", 30)}},
		{"bark", config.Notification{Endpoint: "https://host/"}},
	} {
		err := Validate(test.kind, test.c, true)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid config accepted or leaked", test.kind, err)
		}
	}
	if err := Validate("telegram", config.Notification{Token: "123:" + strings.Repeat("a", 30), ChatID: "-100123"}, true); err != nil {
		t.Fatal(err)
	}
}

func mustView(t *testing.T, s *Service) SettingsView {
	t.Helper()
	v, err := s.View(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestActivationFilteringAndSnapshots(t *testing.T) {
	t.Parallel()
	s, _, store := fixture(t)
	var hits atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(204) }))
	defer receiver.Close()
	old := createRun(createRunOptions{t: t, store: store, state: "queued", failed: 0})
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Endpoint: new(receiver.URL), Enabled: new(true), OnlyFailure: new(false)}})
	old.State = "failed"
	if err := store.SaveRun(t.Context(), old.ID, old.State, old); err != nil {
		t.Fatal(err)
	}
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Enabled: new(true), OnlyFailure: new(true)}})
	success := createRun(createRunOptions{t: t, store: store, state: "succeeded", failed: 0})
	cancel := createRun(createRunOptions{t: t, store: store, state: "cancelled", failed: 0})
	partial := createRun(createRunOptions{t: t, store: store, state: "succeeded", failed: 1})
	if err := s.SetTimezone(t.Context(), "Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	records, err := readRecords(t, s)
	if err != nil || len(records) != 3 || hits.Load() != 1 {
		t.Fatal("history/failure filtering", records, hits.Load(), err)
	}
	for _, r := range records {
		if r.RunID == old.ID {
			t.Fatal("old task notified")
		}
		if r.RunID == success.ID || r.RunID == cancel.ID {
			if r.State != "stopped" {
				t.Fatal("success/cancel not filtered")
			}
		}
	}
	var body string
	if err = fixtureScan(fixtureScanOptions{t: t, store: store, query: "SELECT body FROM notifications WHERE run_id=?", args: []any{partial.ID}, dest: []any{&body}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "UTC+08:00 (Asia/Shanghai)") {
		t.Fatal("missing zone", body)
	}
	if err = s.SetTimezone(t.Context(), "America/New_York"); err != nil {
		t.Fatal(err)
	}
	if err = s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	var after string
	_ = fixtureScan(fixtureScanOptions{t: t, store: store, query: "SELECT body FROM notifications WHERE run_id=?", args: []any{partial.ID}, dest: []any{&after}})
	if hits.Load() != 1 || body != after {
		t.Fatal("dedup or immutable snapshot failed")
	}
	// Disable/re-enable excludes tasks created during the disabled period.
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Enabled: new(false), OnlyFailure: new(false)}})
	createRun(createRunOptions{t: t, store: store, state: "failed", failed: 1})
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Enabled: new(true), OnlyFailure: new(false)}})
	createRun(createRunOptions{t: t, store: store, state: "failed", failed: 0})
	if err = s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatal("reenable replay", hits.Load())
	}
}

func TestRetriesStopAndRestartRecovery(t *testing.T) {
	t.Parallel()
	s, cfg, store := fixture(t)
	var hits atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "sensitive-secret", http.StatusServiceUnavailable)
	}))
	defer receiver.Close()
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Endpoint: new(receiver.URL), Enabled: new(true), OnlyFailure: new(false)}})
	task := createRun(createRunOptions{t: t, store: store, state: "failed", failed: 1})
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	var next int64
	_ = fixtureScan(fixtureScanOptions{t: t, store: store, query: "SELECT next_at FROM notifications WHERE run_id=?", args: []any{task.ID}, dest: []any{&next}})
	if delay := time.Until(time.UnixMilli(next)); delay < 29*time.Second || delay > 31*time.Second {
		t.Fatal("first retry delay", delay)
	}
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatal("retry ran early")
	}
	_, _ = fixtureExec(fixtureExecOptions{t: t, store: store, query: "UPDATE notifications SET next_at=0"})
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = fixtureScan(fixtureScanOptions{t: t, store: store, query: "SELECT next_at FROM notifications WHERE run_id=?", args: []any{task.ID}, dest: []any{&next}})
	if delay := time.Until(time.UnixMilli(next)); delay < 59*time.Second || delay > 61*time.Second {
		t.Fatal("second retry delay", delay)
	}
	_, _ = fixtureExec(fixtureExecOptions{t: t, store: store, query: "UPDATE notifications SET next_at=0"})
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	recs, _ := readRecords(t, s)
	if hits.Load() != 3 || recs[0].State != "failed" || recs[0].Attempts != 3 {
		t.Fatal("retry cap/redaction", recs)
	}
	var unchanged runengine.Run
	if err := store.Run(t.Context(), task.ID, &unchanged); err != nil || unchanged.State != "failed" || unchanged.Progress.Failed != 1 {
		t.Fatal("notification changed task result")
	}
	// Pending retries are stopped immediately by disable and by failure filtering.
	createRun(createRunOptions{t: t, store: store, state: "succeeded", failed: 0})
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Enabled: new(true), OnlyFailure: new(true)}})
	recs, _ = readRecords(t, s)
	if recs[0].State != "stopped" {
		t.Fatal("only failure did not stop pending", recs)
	}
	createRun(createRunOptions{t: t, store: store, state: "failed", failed: 1})
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Enabled: new(false), OnlyFailure: new(false)}})
	recs, _ = readRecords(t, s)
	if recs[0].State != "stopped" {
		t.Fatal("disable did not stop pending")
	}
	_, _ = fixtureExec(fixtureExecOptions{t: t, store: store, query: "UPDATE notifications SET state='sending',attempts=2 WHERE id=?", args: []any{recs[0].ID}})
	recovered, err := New(t.Context(), cfg, store, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownService(t, recovered)
	recs, _ = readRecords(t, recovered)
	if recs[0].State != "stopped" {
		t.Fatal("disabled recovery not stopped")
	}
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Enabled: new(true), OnlyFailure: new(false)}})
	_, _ = fixtureExec(fixtureExecOptions{t: t, store: store, query: "UPDATE notifications SET state='sending',attempts=2 WHERE id=?", args: []any{recs[0].ID}})
	recovered2, err := New(t.Context(), cfg, store, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownService(t, recovered2)
	recs, _ = readRecords(t, recovered2)
	if recs[0].State != "waiting" || recs[0].Attempts != 2 {
		t.Fatal("unconfirmed send not recovered")
	}
	_, _ = fixtureExec(fixtureExecOptions{t: t, store: store, query: "UPDATE notifications SET state='sending',attempts=3 WHERE id=?", args: []any{recs[0].ID}})
	recovered3, err := New(t.Context(), cfg, store, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownService(t, recovered3)
	recs, _ = readRecords(t, recovered3)
	if recs[0].State != "failed" {
		t.Fatal("exhausted send recovered")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestChannelAdaptersLocalOnly(t *testing.T) {
	t.Parallel()
	for _, kind := range config.NotificationChannels {
		t.Run(kind, func(t *testing.T) {
			var input map[string]any
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("request contract")
				}
				if err := json.UnmarshalRead(r.Body, &input); err != nil {
					t.Error(err)
				}
				if kind == "bark" && r.URL.Path != "/custom/prefix/push" {
					t.Error("Bark prefix lost", r.URL.Path)
				}
				if kind == "dingtalk" {
					q := r.URL.Query()
					stamp := q.Get("timestamp")
					mac := hmac.New(sha256.New, []byte("SEC-fixture"))
					_, _ = mac.Write([]byte(stamp + "\nSEC-fixture"))
					if stamp == "" || q.Get("sign") != base64.StdEncoding.EncodeToString(mac.Sum(nil)) {
						t.Error("bad signature")
					}
				}
				switch kind {
				case "wecom", "dingtalk":
					io.WriteString(w, `{"errcode":0}`)
				case "telegram":
					io.WriteString(w, `{"ok":true}`)
				case "bark":
					io.WriteString(w, `{"code":200}`)
				default:
					w.WriteHeader(204)
				}
			}))
			defer receiver.Close()
			// Rewrite only inside the test transport; production always validates official endpoints.
			client := newClient()
			defer client.CloseIdleConnections()
			base := client.Transport
			client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				u, _ := endpointURL(receiver.URL)
				r.URL.Scheme = u.Scheme
				r.URL.Host = u.Host
				return base.RoundTrip(r)
			})
			c := config.Notification{Endpoint: receiver.URL + "/custom/prefix/device-key///", Token: "123:" + strings.Repeat("a", 30), ChatID: "-100123", Secret: "SEC-fixture"}
			if kind == "dingtalk" {
				c.Endpoint = "https://oapi.dingtalk.com/robot/send?access_token=test"
			}
			m := Message{Event: "batch.finished", Title: "fixture title", Body: "task #42"}
			if err := send(t.Context(), sendOptions{client: client, kind: kind, c: c, m: m}); err != nil {
				t.Fatal(err)
			}
			if kind == "telegram" && input["chat_id"] != "-100123" {
				t.Fatal("negative ID lost")
			}
			if kind == "bark" && (input["device_key"] != "device-key" || input["group"] != Project || input["isArchive"] != "1") {
				t.Fatal("Bark JSON", input)
			}
			if kind == "webhook" && (input["event"] != "batch.finished" || input["title"] != m.Title || input["body"] != m.Body) {
				t.Fatal("webhook JSON")
			}
		})
	}
}

func TestBusinessErrorsLimitsRedirectAndRedaction(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		kind, body string
		status     int
	}{
		{"wecom", `{"errcode":1,"errmsg":"sensitive-token"}`, 200}, {"dingtalk", `{"errcode":3,"errmsg":"sensitive-token"}`, 200}, {"telegram", `{"ok":false,"description":"sensitive-token"}`, 200}, {"bark", `{"code":400,"message":"sensitive-token"}`, 200},
		{"wecom", `{}`, 200}, {"telegram", `not-json sensitive-token`, 200}, {"webhook", strings.Repeat("s", (64<<10)+1), 200}, {"webhook", "sensitive-token", 503},
	} {
		t.Run(test.kind+test.body[:min(8, len(test.body))], func(t *testing.T) {
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.status); io.WriteString(w, test.body) }))
			defer receiver.Close()
			c := config.Notification{Endpoint: receiver.URL + "/key"}
			client := newClient()
			defer client.CloseIdleConnections()
			if test.kind == "telegram" {
				base := client.Transport
				client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					u, _ := endpointURL(receiver.URL)
					r.URL.Scheme = u.Scheme
					r.URL.Host = u.Host
					return base.RoundTrip(r)
				})
			}
			err := send(t.Context(), sendOptions{client: client, kind: test.kind, c: c, m: Message{}})
			if err == nil || strings.Contains(err.Error(), "sensitive-token") || strings.Contains(err.Error(), receiver.URL) {
				t.Fatal("error was not sanitized", err)
			}
		})
	}
	var followed atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1) }))
	defer dest.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dest.URL, http.StatusFound) }))
	defer redirect.Close()
	client := newClient()
	defer client.CloseIdleConnections()
	if client.Timeout != 10*time.Second {
		t.Fatal("timeout")
	}
	if err := send(t.Context(), sendOptions{client: client, kind: "webhook", c: config.Notification{Endpoint: redirect.URL}, m: Message{}}); err == nil || followed.Load() != 0 {
		t.Fatal("redirect followed", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := send(ctx, sendOptions{client: client, kind: "webhook", c: config.Notification{Endpoint: dest.URL + "/sensitive-token"}, m: Message{}}); err == nil || strings.Contains(err.Error(), "sensitive-token") {
		t.Fatal("cancellation leaked request")
	}
}

func TestTimezoneContentAndKeyBackup(t *testing.T) {
	t.Parallel()
	s, cfg, store := fixture(t)
	if mustView(t, s).Timezone != "UTC" {
		t.Fatal("default zone")
	}
	if err := s.SetTimezone(t.Context(), "Invalid/zone"); err == nil {
		t.Fatal("invalid zone")
	}
	if err := s.SetTimezone(t.Context(), "America/New_York"); err != nil {
		t.Fatal(err)
	}
	r := createRun(createRunOptions{t: t, store: store, state: "failed", failed: 2})
	r.Name = strings.Repeat("界", 200)
	r.CreatedAt = timefmt.From(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	r.StartedAt = timefmt.From(r.CreatedAt.Time().Add(time.Hour))
	r.EndedAt = timefmt.From(r.StartedAt.Time().Add(10 * time.Second))
	r.ElapsedMillis = 10000
	r.Progress.Success = 5
	m, failed := resultMessage(r, "America/New_York")
	if !failed || !strings.Contains(m.Title, "部分失败") || !strings.Contains(m.Body, "UTC-04:00 (America/New_York)") || !strings.Contains(m.Body, "处理耗时：10s") || !strings.Contains(m.Body, "未完成：1") || strings.Contains(m.Body, strings.Repeat("界", 101)) {
		t.Fatal(m)
	}
	r.StartedAt = timefmt.Time{}
	r.EndedAt = timefmt.Time{}
	m, _ = resultMessage(r, "UTC")
	if !strings.Contains(m.Body, "未开始") || !strings.Contains(m.Body, "未记录") {
		t.Fatal("missing times")
	}
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Endpoint: new("https://host/credential"), Enabled: new(false), OnlyFailure: new(false)}})
	restored, err := New(t.Context(), cfg, store, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	shutdownService(t, restored)
	if mustView(t, restored).Timezone != "America/New_York" {
		t.Fatal("timezone not persisted")
	}
	key := filepath.Join(config.Resolve(cfg.File(), cfg.Startup().Path), "notification.key")
	if err = os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if replacement, err := New(t.Context(), cfg, store, slog.New(slog.DiscardHandler)); err == nil {
		shutdownService(t, replacement)
		t.Fatal("missing key silently replaced")
	}
	if _, err = os.Stat(key); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing key regenerated")
	}
}

func TestInterruptedDoesNotConsumeFinalNotification(t *testing.T) {
	t.Parallel()
	s, _, store := fixture(t)
	var got Message
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.UnmarshalRead(r.Body, &got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Endpoint: new(receiver.URL), Enabled: new(true), OnlyFailure: new(false)}})
	r := createRun(createRunOptions{t: t, store: store, state: "interrupted", failed: 0})
	r.State = "queued"
	r.EndedAt = timefmt.Time{}
	if err := store.SaveRun(t.Context(), r.ID, r.State, r); err != nil {
		t.Fatal(err)
	}
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got.Title != "" {
		t.Fatal("interrupted sent a final notification", got)
	}
	r.State = "succeeded"
	r.EndedAt = timefmt.From(time.Now().UTC())
	if err := store.SaveRun(t.Context(), r.ID, r.State, r); err != nil {
		t.Fatal(err)
	}
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Title, "任务成功") {
		t.Fatal("final result was lost", got)
	}
}

func TestQueueAndTimezonePersistAcrossDatabaseReopen(t *testing.T) {
	t.Parallel()
	s, cfg, store := fixture(t)
	var failing atomic.Bool
	failing.Store(true)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer receiver.Close()
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Endpoint: new(receiver.URL), Enabled: new(true), OnlyFailure: new(false)}})
	if err := s.SetTimezone(t.Context(), "Asia/Shanghai"); err != nil {
		t.Fatal(err)
	}
	r := createRun(createRunOptions{t: t, store: store, state: "failed", failed: 1})
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "UPDATE notifications SET state='sending',attempts=2 WHERE run_id=?", args: []any{r.ID}}); err != nil {
		t.Fatal(err)
	}
	shutdownService(t, s)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Open(config.LoadSource{Path: cfg.File(), Explicit: true})
	if err != nil {
		t.Fatal(err)
	}
	store2, err := database.Open(t.Context(), config.Resolve(cfg.File(), cfg.Startup().Path))
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	restarted, err := New(t.Context(), reloaded, store2, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownService(t, restarted)
	if mustView(t, restarted).Timezone != "Asia/Shanghai" {
		t.Fatal("timezone lost across restart")
	}
	records, err := readRecords(t, restarted)
	if err != nil || len(records) != 1 || records[0].State != "waiting" {
		t.Fatal("queue lost", records, err)
	}
	failing.Store(false)
	if err := restarted.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	records, err = readRecords(t, restarted)
	if err != nil || len(records) != 1 || records[0].State != "sent" || records[0].Attempts != 3 {
		t.Fatal("recovery attempt/dedup failed", records, err)
	}
}

func TestLatestThirtyAndIndependentChannelFailure(t *testing.T) {
	t.Parallel()
	s, _, store := fixture(t)
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Endpoint: new("http://127.0.0.1:1/fixture"), Enabled: new(true), OnlyFailure: new(true)}})
	for range 40 {
		createRun(createRunOptions{t: t, store: store, state: "succeeded", failed: 0})
	}
	if err := s.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	records, err := readRecords(t, s)
	if err != nil || len(records) != 30 || records[0].RunID != 40 || records[29].RunID != 11 {
		t.Fatal("recent record bound/order", records, err)
	}
}

func TestCiphertextTamperAndKeyPermissions(t *testing.T) {
	t.Parallel()
	s, cfg, store := fixture(t)
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Endpoint: new("https://host/secret"), Enabled: new(false), OnlyFailure: new(false)}})
	encrypted := cfg.Current().Settings.Notifications["webhook"].Endpoint
	if _, err := s.decrypt("bark", "endpoint", encrypted); err == nil {
		t.Fatal("ciphertext moved between channels")
	}
	if _, err := s.decrypt("webhook", "token", encrypted); err == nil {
		t.Fatal("ciphertext moved between fields")
	}
	bytes, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encrypted, "enc:v1:"))
	if err != nil {
		t.Fatal(err)
	}
	bytes[len(bytes)-1] ^= 1
	if _, err := s.decrypt("webhook", "endpoint", "enc:v1:"+base64.RawStdEncoding.EncodeToString(bytes)); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	key := filepath.Join(config.Resolve(cfg.File(), cfg.Startup().Path), "notification.key")
	if err := os.Chmod(key, 0o644); err != nil {
		t.Fatal(err)
	}
	if unsafe, err := New(t.Context(), cfg, store, slog.New(slog.DiscardHandler)); err == nil {
		shutdownService(t, unsafe)
		t.Fatal("readable key accepted")
	}
	if err := os.Chmod(key, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if mismatched, err := New(t.Context(), cfg, store, slog.New(slog.DiscardHandler)); err == nil {
		shutdownService(t, mismatched)
		t.Fatal("wrong key accepted")
	}
}

func TestConcurrentTestsAreRejectedAndTimeoutIsSanitized(t *testing.T) {
	t.Parallel()
	s, _, _ := fixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) }))
	defer receiver.Close()
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Endpoint: new(receiver.URL + "/sensitive-token"), Enabled: new(false), OnlyFailure: new(false)}})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.Test(ctx, "webhook") }()
	<-entered
	err := s.Test(ctx, "webhook")
	close(release)
	if err == nil {
		t.Fatal("concurrent test not rejected")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer slow.Close()
	client := newClient()
	defer client.CloseIdleConnections()
	client.Timeout = 20 * time.Millisecond
	if err := send(t.Context(), sendOptions{client: client, kind: "webhook", c: config.Notification{Endpoint: slow.URL + "/sensitive-token"}, m: Message{}}); err == nil || strings.Contains(err.Error(), "sensitive-token") {
		t.Fatal("timeout leaked", err)
	}
}

func TestRecordPageUsesCancelableReadPool(t *testing.T) {
	t.Parallel()
	s, _, store := fixture(t)
	locked := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- store.Write(t.Context(), func(tx *sql.Tx) error { close(locked); <-release; return nil })
	}()
	<-locked
	defer func() {
		close(release)
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	records, total, err := s.RecordPage(ctx, 50, 0)
	if err != nil || total != 0 || len(records) != 0 {
		t.Fatalf("writer pool blocked records: %v", err)
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if _, _, err := s.RecordPage(cancelled, 50, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
}

func TestNotificationCursorHandlesLateCompletionAndRestart(t *testing.T) {
	t.Parallel()
	s, cfg, store := fixture(t)
	old := createRun(createRunOptions{t: t, store: store, state: "running", failed: 0})
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Enabled: new(true), Endpoint: new("http://127.0.0.1:1/fixture"), OnlyFailure: new(false)}})
	early := createRun(createRunOptions{t: t, store: store, state: "running", failed: 0})
	for range 105 {
		createRun(createRunOptions{t: t, store: store, state: "succeeded", failed: 0})
	}
	if err := s.enqueueLocked(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := fixtureScan(fixtureScanOptions{t: t, store: store, query: "SELECT count(*) FROM notifications", args: []any{}, dest: []any{&count}}); err != nil || count != 100 {
		t.Fatal(count, err)
	}
	early.State = "succeeded"
	if err := store.SaveRun(t.Context(), early.ID, early.State, early); err != nil {
		t.Fatal(err)
	}
	old.State = "succeeded"
	if err := store.SaveRun(t.Context(), old.ID, old.State, old); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(t.Context(), cfg, store, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownService(t, restarted)
	if err := restarted.enqueueLocked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.enqueueLocked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := fixtureScan(fixtureScanOptions{t: t, store: store, query: "SELECT count(*) FROM notifications", args: []any{}, dest: []any{&count}}); err != nil || count != 106 {
		t.Fatal(count, err)
	}
	if err := fixtureScan(fixtureScanOptions{t: t, store: store, query: "SELECT count(*) FROM notifications WHERE run_id=?", args: []any{early.ID}, dest: []any{&count}}); err != nil || count != 1 {
		t.Fatal("late completion missing", count, err)
	}
	if err := fixtureScan(fixtureScanOptions{t: t, store: store, query: "SELECT count(*) FROM notifications WHERE run_id=?", args: []any{old.ID}, dest: []any{&count}}); err != nil || count != 0 {
		t.Fatal("pre-activation run notified", count, err)
	}
	state, err := store.State(t.Context(), "notification_activation_webhook")
	if err != nil {
		t.Fatal(err)
	}
	var a activation
	if err := json.Unmarshal([]byte(state), &a); err != nil || a.Cursor != 107 {
		t.Fatal(a, err)
	}
}

func TestNotificationCursorReplaysFailedQueuePage(t *testing.T) {
	t.Parallel()
	s, _, store := fixture(t)
	saveChannel(saveChannelOptions{t: t, s: s, kind: "webhook", u: Update{Enabled: new(true), Endpoint: new("http://127.0.0.1:1/fixture"), OnlyFailure: new(false)}})
	for range 3 {
		createRun(createRunOptions{t: t, store: store, state: "succeeded", failed: 0})
	}
	if _, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "CREATE TRIGGER reject_queue BEFORE INSERT ON notifications WHEN NEW.run_id=2 BEGIN SELECT RAISE(ABORT,'fixture'); END"}); err != nil {
		t.Fatal(err)
	}
	if err := s.enqueueLocked(t.Context()); err == nil {
		t.Fatal("queue error lost")
	}
	if _, err := fixtureExec(fixtureExecOptions{t: t, store: store, query: "DROP TRIGGER reject_queue"}); err != nil {
		t.Fatal(err)
	}
	if err := s.enqueueLocked(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := fixtureScan(fixtureScanOptions{t: t, store: store, query: "SELECT count(*) FROM notifications", args: []any{}, dest: []any{&count}}); err != nil || count != 3 {
		t.Fatal(count, err)
	}
}

// saveChannelOptions groups the inputs to saveChannel.
type saveChannelOptions struct {
	t    *testing.T
	s    *Service
	kind string
	u    Update
}

// createRunOptions groups the inputs to createRun.
type createRunOptions struct {
	t      *testing.T
	store  *database.Store
	state  string
	failed int
}
