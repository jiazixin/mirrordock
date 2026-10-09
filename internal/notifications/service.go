package notifications

import (
	"context"
	"crypto/cipher"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"mirrordock/internal/config"
	"mirrordock/internal/database"
	"mirrordock/internal/runs"
)

type activation struct {
	Enabled bool  `json:"enabled"`
	Cutoff  int64 `json:"cutoff"`
	Cursor  int64 `json:"cursor"`
}
type ChannelView struct {
	Kind        string `json:"kind"`
	Enabled     bool   `json:"enabled"`
	OnlyFailure bool   `json:"only_failure"`
	HasEndpoint bool   `json:"has_endpoint"`
	HasToken    bool   `json:"has_token"`
	HasSecret   bool   `json:"has_secret"`
	ChatID      string `json:"chat_id"`
	Ready       bool   `json:"ready"`
}
type SettingsView struct {
	Channels []ChannelView `json:"channels"`
	Revision string        `json:"revision"`
	Timezone string        `json:"timezone"`
}
type Update struct {
	Revision    string `json:"revision"`
	Enabled     bool   `json:"enabled"`
	OnlyFailure bool   `json:"only_failure"`
	Endpoint    string `json:"endpoint"`
	Token       string `json:"token"`
	Secret      string `json:"secret"`
	ChatID      string `json:"chat_id"`
	ClearSecret bool   `json:"clear_secret"`
}
type Record struct {
	ID        int64  `json:"id"`
	Channel   string `json:"channel"`
	RunID     int64  `json:"run_id"`
	State     string `json:"state"`
	Attempts  int    `json:"attempts"`
	UpdatedAt string `json:"updated_at"`
	Error     string `json:"error"`
}
type Service struct {
	mu      sync.Mutex
	scanMu  sync.Mutex
	testing map[string]bool
	cfg     *config.Manager
	store   *database.Store
	aead    cipher.AEAD
	client  *http.Client
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	start   sync.Once
}

func New(cfg *config.Manager, store *database.Store) (*Service, error) {
	existing := false
	for _, c := range cfg.Current().Settings.Notifications {
		existing = existing || c.Endpoint != "" || c.Token != "" || c.Secret != ""
	}
	key, err := openKey(config.Resolve(cfg.File(), cfg.Startup().Path), existing)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{cfg: cfg, store: store, aead: key, client: newClient(), testing: map[string]bool{}, ctx: ctx, cancel: cancel}
	if err = s.ValidateConfig(cfg.Current()); err != nil {
		cancel()
		return nil, err
	}
	if _, err = store.DB.Exec("UPDATE notifications SET state=CASE WHEN attempts<3 THEN 'waiting' ELSE 'failed' END,next_at=0,updated_at=?,error='发送状态未确认；服务重启恢复，可能重复送达' WHERE state='sending'", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		cancel()
		return nil, err
	}
	if err = s.Sync(); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}
func (s *Service) ValidateConfig(c config.Config) error {
	for kind, encrypted := range c.Settings.Notifications {
		p, err := s.plain(kind, encrypted)
		if err != nil {
			return err
		}
		if err = Validate(kind, p, p.Enabled); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Start() {
	s.start.Do(func() {
		s.wg.Go(func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				if err := s.Scan(); err != nil && s.ctx.Err() == nil {
					slog.Error("notification queue scan failed")
				}
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
				}
			}
		})
	})
}
func (s *Service) Close() { s.cancel(); s.wg.Wait(); s.client.CloseIdleConnections() }
func (s *Service) timezone() (string, error) {
	zone, err := s.store.State("notification_timezone")
	if zone == "" {
		zone = "UTC"
	}
	return zone, err
}
func (s *Service) SetTimezone(zone string) error {
	if len(zone) > 128 {
		return errors.New("IANA 时区无效")
	}
	if _, err := time.LoadLocation(zone); err != nil || zone == "" || zone == "Local" {
		return errors.New("IANA 时区无效")
	}
	return s.store.SetState("notification_timezone", zone)
}
func (s *Service) View() (SettingsView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.cfg.View()
	out := SettingsView{Channels: []ChannelView{}, Revision: cfg.Revision}
	var err error
	out.Timezone, err = s.timezone()
	if err != nil {
		return out, err
	}
	for _, kind := range config.NotificationChannels {
		c := cfg.Config.Settings.Notifications[kind]
		plain, err := s.plain(kind, c)
		if err != nil {
			return out, err
		}
		out.Channels = append(out.Channels, ChannelView{Kind: kind, Enabled: c.Enabled, OnlyFailure: c.OnlyFailure, ChatID: c.ChatID, HasEndpoint: c.Endpoint != "", HasToken: c.Token != "", HasSecret: c.Secret != "", Ready: Validate(kind, plain, true) == nil})
	}
	return out, nil
}
func (s *Service) Save(kind string, u Update) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(config.NotificationChannels, kind) {
		return errors.New("不支持的通知渠道")
	}
	if kind != "dingtalk" && (u.ClearSecret || u.Secret != "") || kind != "telegram" && (u.Token != "" || u.ChatID != "") || kind == "telegram" && u.Endpoint != "" {
		return errors.New("通知字段与渠道不匹配")
	}
	if u.ClearSecret && u.Secret != "" {
		return errors.New("清除密钥时不能同时填写新密钥")
	}
	channels := maps.Clone(s.cfg.Current().Settings.Notifications)
	if channels == nil {
		channels = map[string]config.Notification{}
	}
	c, err := s.plain(kind, channels[kind])
	if err != nil {
		return err
	}
	c.Enabled = u.Enabled
	c.OnlyFailure = u.OnlyFailure
	if u.Endpoint != "" {
		c.Endpoint = u.Endpoint
	}
	if u.Token != "" {
		c.Token = u.Token
	}
	if u.Secret != "" {
		c.Secret = u.Secret
	}
	if u.ClearSecret {
		c.Secret = ""
	}
	if kind == "telegram" {
		c.ChatID = u.ChatID
	}
	if err = Validate(kind, c, c.Enabled); err != nil {
		return err
	}
	c.Endpoint = s.encrypt(kind, "endpoint", c.Endpoint)
	c.Token = s.encrypt(kind, "token", c.Token)
	c.Secret = s.encrypt(kind, "secret", c.Secret)
	channels[kind] = c
	if err = s.cfg.SaveNotifications(channels, u.Revision); err != nil {
		return err
	}
	return s.syncLocked()
}
func (s *Service) Sync() error { s.mu.Lock(); defer s.mu.Unlock(); return s.syncLocked() }
func (s *Service) syncLocked() error {
	channels := s.cfg.Current().Settings.Notifications
	for _, kind := range config.NotificationChannels {
		c := channels[kind]
		b, err := s.store.State("notification_activation_" + kind)
		if err != nil {
			return err
		}
		var a activation
		if b != "" {
			if err = json.Unmarshal([]byte(b), &a); err != nil {
				return errors.New("通知激活边界无法读取")
			}
		}
		if c.Enabled && !a.Enabled {
			// Capture both boundaries in one SQLite snapshot: a newly created
			// run must not finish between separate reads and be skipped forever.
			if err = s.store.ReadDB.QueryRowContext(s.ctx, "SELECT COALESCE((SELECT MAX(id) FROM runs),0),COALESCE((SELECT MAX(sequence) FROM run_results),0)").Scan(&a.Cutoff, &a.Cursor); err != nil {
				return err
			}
		}
		if a.Enabled != c.Enabled || b == "" {
			a.Enabled = c.Enabled
			encoded, err := json.Marshal(a)
			if err != nil {
				return err
			}
			if err = s.store.SetState("notification_activation_"+kind, string(encoded)); err != nil {
				return err
			}
		}
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		if !c.Enabled {
			_, err = s.store.DB.Exec("UPDATE notifications SET state='stopped',updated_at=?,error='渠道已关闭' WHERE channel=? AND state='waiting'", stamp, kind)
		} else if c.OnlyFailure {
			_, err = s.store.DB.Exec("UPDATE notifications SET state='stopped',updated_at=?,error='不符合仅失败通知条件' WHERE channel=? AND state='waiting' AND failure=0", stamp, kind)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) enqueueLocked() error {
	zone, err := s.timezone()
	if err != nil {
		return err
	}
	for _, kind := range config.NotificationChannels {
		c := s.cfg.Current().Settings.Notifications[kind]
		if !c.Enabled {
			continue
		}
		state, err := s.store.State("notification_activation_" + kind)
		if err != nil {
			return err
		}
		var a activation
		if err = json.Unmarshal([]byte(state), &a); err != nil {
			return err
		}
		// Completion order differs from run ID order: an older paused task can
		// finish after a newer task. Seek a durable insertion cursor so each scan
		// remains bounded without missing those late results or rescanning history.
		rows, err := s.store.ReadDB.QueryContext(s.ctx, "SELECT sequence,data FROM run_results WHERE sequence>? ORDER BY sequence LIMIT 100", a.Cursor)
		if err != nil {
			return err
		}
		records := []runs.Run{}
		cursor := a.Cursor
		for rows.Next() {
			var b []byte
			if err = rows.Scan(&cursor, &b); err != nil {
				break
			}
			var r runs.Run
			if err = json.Unmarshal(b, &r); err != nil {
				break
			}
			records = append(records, r)
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return err
		}
		for _, r := range records {
			if r.ID <= a.Cutoff {
				continue
			}
			m, failed := resultMessage(&r, zone)
			status, reason := "waiting", ""
			if c.OnlyFailure && !failed {
				status = "stopped"
				reason = "不符合仅失败通知条件"
			}
			if _, err = s.store.DB.Exec("INSERT OR IGNORE INTO notifications(channel,run_id,event,title,body,failure,state,next_at,updated_at,error) VALUES(?,?,?,?,?,?,?,0,?,?)", kind, r.ID, m.Event, m.Title, m.Body, failed, status, time.Now().UTC().Format(time.RFC3339Nano), reason); err != nil {
				return err
			}
		}
		if cursor != a.Cursor {
			a.Cursor = cursor
			encoded, err := json.Marshal(a)
			if err != nil {
				return err
			}
			// Advance only after every queue write succeeds. A crash before this
			// write safely replays the bounded page via UNIQUE(channel,run_id).
			if err := s.store.SetState("notification_activation_"+kind, string(encoded)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Scan reads only persisted terminal results. HTTP work runs outside the configuration lock.
func (s *Service) Scan() error {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil
	}
	err := s.syncLocked()
	if err == nil {
		err = s.enqueueLocked()
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	// Bound each pass so shutdown/settings remain responsive even with a backlog.
	for range 10 {
		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			return nil
		}
		if err = s.syncLocked(); err != nil {
			s.mu.Unlock()
			return err
		}
		var rec Record
		var m Message
		err = s.store.DB.QueryRow("SELECT id,channel,run_id,attempts,event,title,body FROM notifications WHERE state='waiting' AND next_at<=? ORDER BY id LIMIT 1", time.Now().UnixMilli()).Scan(&rec.ID, &rec.Channel, &rec.RunID, &rec.Attempts, &m.Event, &m.Title, &m.Body)
		if errors.Is(err, sql.ErrNoRows) {
			s.mu.Unlock()
			return nil
		}
		if err != nil {
			s.mu.Unlock()
			return err
		}
		c, err := s.plain(rec.Channel, s.cfg.Current().Settings.Notifications[rec.Channel])
		if err == nil {
			err = Validate(rec.Channel, c, true)
		}
		if err != nil {
			_, dbErr := s.store.DB.Exec("UPDATE notifications SET state='failed',updated_at=?,error='通知配置无效，请重新保存' WHERE id=?", time.Now().UTC().Format(time.RFC3339Nano), rec.ID)
			s.mu.Unlock()
			if dbErr != nil {
				return dbErr
			}
			continue
		}
		rec.Attempts++
		_, err = s.store.DB.Exec("UPDATE notifications SET state='sending',attempts=?,updated_at=? WHERE id=? AND state='waiting'", rec.Attempts, time.Now().UTC().Format(time.RFC3339Nano), rec.ID)
		s.mu.Unlock()
		if err != nil {
			return err
		}
		err = send(s.ctx, s.client, rec.Channel, c, m)
		s.mu.Lock()
		status, reason, next := "sent", "", int64(0)
		if err != nil {
			reason = err.Error()
			status = "failed"
			if rec.Attempts < 3 {
				status = "waiting"
				next = time.Now().Add(time.Duration(rec.Attempts) * 30 * time.Second).UnixMilli()
			}
		}
		_, dbErr := s.store.DB.Exec("UPDATE notifications SET state=?,next_at=?,updated_at=?,error=? WHERE id=? AND state='sending'", status, next, time.Now().UTC().Format(time.RFC3339Nano), reason, rec.ID)
		if dbErr == nil {
			dbErr = s.syncLocked()
		}
		s.mu.Unlock()
		if dbErr != nil {
			return dbErr
		}
	}
	return nil
}
func (s *Service) Records() ([]Record, error) {
	records, _, err := s.RecordPage(s.ctx, 30, 0)
	return records, err
}

func (s *Service) RecordPage(ctx context.Context, limit, offset int) ([]Record, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.store.ReadDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM notifications").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,channel,run_id,state,attempts,updated_at,error FROM notifications ORDER BY id DESC LIMIT ? OFFSET ?", min(100, max(1, limit)), max(0, offset))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		if err = rows.Scan(&r.ID, &r.Channel, &r.RunID, &r.State, &r.Attempts, &r.UpdatedAt, &r.Error); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
func (s *Service) Test(ctx context.Context, kind string) error {
	s.mu.Lock()
	if !slices.Contains(config.NotificationChannels, kind) {
		s.mu.Unlock()
		return errors.New("不支持的通知渠道")
	}
	if s.testing[kind] {
		s.mu.Unlock()
		return errors.New("该渠道正在发送测试，请稍后")
	}
	s.testing[kind] = true
	defer func() { s.mu.Lock(); delete(s.testing, kind); s.mu.Unlock() }()
	c, err := s.plain(kind, s.cfg.Current().Settings.Notifications[kind])
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err = Validate(kind, c, true); err != nil {
		return err
	}
	return send(ctx, s.client, kind, c, Message{Event: "notification.test", Title: "[" + Project + "] · 测试通知", Body: "渠道配置测试成功。自动通知只处理启用后新创建的同步任务。"})
}
