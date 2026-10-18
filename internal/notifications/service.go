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

// activation is the durable boundary of one channel, stored in app_state.
// Cutoff is the largest run ID that existed when the channel was enabled;
// Cursor is the last consumed run_results sequence.
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

// Delivery limits of the background queue.
const (
	scanInterval      = 3 * time.Second
	maxAttempts       = 3
	retryStep         = 30 * time.Second
	sendsPerScan      = 10
	resultsPerScan    = 100
	activationPrefix  = "notification_activation_"
	timezoneStateKey  = "notification_timezone"
	defaultTimezone   = "UTC"
	maxTimezoneLength = 128
)

const (
	recoverSendingQuery = `UPDATE notifications
 SET state=CASE WHEN attempts<3 THEN 'waiting' ELSE 'failed' END, next_at=0, updated_at=?,
     error='发送状态未确认；服务重启恢复，可能重复送达'
 WHERE state='sending'`
	activationBoundaryQuery = `SELECT
 COALESCE((SELECT MAX(id) FROM runs), 0),
 COALESCE((SELECT MAX(sequence) FROM run_results), 0)`
	stopDisabledQuery = `UPDATE notifications SET state='stopped', updated_at=?, error='渠道已关闭'
 WHERE channel=? AND state='waiting'`
	stopSuccessQuery = `UPDATE notifications SET state='stopped', updated_at=?, error='不符合仅失败通知条件'
 WHERE channel=? AND state='waiting' AND failure=0`
	pendingResultsQuery = `SELECT sequence, data FROM run_results
 WHERE sequence>? ORDER BY sequence LIMIT ?`
	enqueueQuery = `INSERT OR IGNORE INTO notifications
 (channel, run_id, event, title, body, failure, state, next_at, updated_at, error)
 VALUES(?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`
	nextDueQuery = `SELECT id, channel, run_id, attempts, event, title, body FROM notifications
 WHERE state='waiting' AND next_at<=? ORDER BY id LIMIT 1`
	invalidChannelQuery = `UPDATE notifications SET state='failed', updated_at=?, error='通知配置无效，请重新保存'
 WHERE id=?`
	markSendingQuery = `UPDATE notifications SET state='sending', attempts=?, updated_at=?
 WHERE id=? AND state='waiting'`
	finishSendingQuery = `UPDATE notifications SET state=?, next_at=?, updated_at=?, error=?
 WHERE id=? AND state='sending'`
	recordCountQuery = "SELECT count(*) FROM notifications"
	recordPageQuery  = `SELECT id, channel, run_id, state, attempts, updated_at, error FROM notifications
 ORDER BY id DESC LIMIT ? OFFSET ?`
)

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

func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func New(cfg *config.Manager, store *database.Store) (*Service, error) {
	hasCredentials := false
	for _, channel := range cfg.Current().Settings.Notifications {
		if channel.Endpoint != "" || channel.Token != "" || channel.Secret != "" {
			hasCredentials = true
		}
	}
	key, err := openKey(config.Resolve(cfg.File(), cfg.Startup().Path), hasCredentials)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		cfg:     cfg,
		store:   store,
		aead:    key,
		client:  newClient(),
		testing: map[string]bool{},
		ctx:     ctx,
		cancel:  cancel,
	}
	if err = s.ValidateConfig(cfg.Current()); err != nil {
		cancel()
		return nil, err
	}
	// A send interrupted by a restart may or may not have been delivered.
	if _, err = store.DB.Exec(recoverSendingQuery, timestamp()); err != nil {
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
		channel, err := s.plain(kind, encrypted)
		if err != nil {
			return err
		}
		if err = Validate(kind, channel, channel.Enabled); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Start() {
	s.start.Do(func() {
		s.wg.Go(func() {
			ticker := time.NewTicker(scanInterval)
			defer ticker.Stop()
			for {
				if err := s.Scan(); err != nil && s.ctx.Err() == nil {
					slog.Error("notification queue scan failed", "error", err)
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

func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
	s.client.CloseIdleConnections()
}

func (s *Service) timezone() (string, error) {
	zone, err := s.store.State(timezoneStateKey)
	if zone == "" {
		zone = defaultTimezone
	}
	return zone, err
}

func (s *Service) SetTimezone(zone string) error {
	if zone == "" || zone == "Local" || len(zone) > maxTimezoneLength {
		return errors.New("IANA 时区无效")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return errors.New("IANA 时区无效")
	}
	return s.store.SetState(timezoneStateKey, zone)
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
		stored := cfg.Config.Settings.Notifications[kind]
		plain, err := s.plain(kind, stored)
		if err != nil {
			return out, err
		}
		out.Channels = append(out.Channels, ChannelView{
			Kind:        kind,
			Enabled:     stored.Enabled,
			OnlyFailure: stored.OnlyFailure,
			ChatID:      stored.ChatID,
			HasEndpoint: stored.Endpoint != "",
			HasToken:    stored.Token != "",
			HasSecret:   stored.Secret != "",
			Ready:       Validate(kind, plain, true) == nil,
		})
	}
	return out, nil
}

// Save applies one channel update. Empty credential fields keep the stored
// value; ClearSecret is the only way to remove a secret.
func (s *Service) Save(kind string, u Update) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(config.NotificationChannels, kind) {
		return errors.New("不支持的通知渠道")
	}
	fields := config.ChannelFields{
		Endpoint: u.Endpoint != "",
		Token:    u.Token != "",
		ChatID:   u.ChatID != "",
		Secret:   u.Secret != "" || u.ClearSecret,
	}
	if !fields.AllowedFor(kind) {
		return errors.New("通知字段与渠道不匹配")
	}
	if u.ClearSecret && u.Secret != "" {
		return errors.New("清除密钥时不能同时填写新密钥")
	}
	channels := maps.Clone(s.cfg.Current().Settings.Notifications)
	if channels == nil {
		channels = map[string]config.Notification{}
	}
	channel, err := s.plain(kind, channels[kind])
	if err != nil {
		return err
	}
	channel.Enabled = u.Enabled
	channel.OnlyFailure = u.OnlyFailure
	if u.Endpoint != "" {
		channel.Endpoint = u.Endpoint
	}
	if u.Token != "" {
		channel.Token = u.Token
	}
	if u.Secret != "" {
		channel.Secret = u.Secret
	}
	if u.ClearSecret {
		channel.Secret = ""
	}
	if kind == "telegram" {
		channel.ChatID = u.ChatID
	}
	if err = Validate(kind, channel, channel.Enabled); err != nil {
		return err
	}
	channel.Endpoint = s.encrypt(kind, "endpoint", channel.Endpoint)
	channel.Token = s.encrypt(kind, "token", channel.Token)
	channel.Secret = s.encrypt(kind, "secret", channel.Secret)
	channels[kind] = channel
	if err = s.cfg.SaveNotifications(channels, u.Revision); err != nil {
		return err
	}
	return s.syncLocked()
}

func (s *Service) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncLocked()
}

func (s *Service) loadActivation(kind string) (activation, bool, error) {
	stored, err := s.store.State(activationPrefix + kind)
	if err != nil || stored == "" {
		return activation{}, false, err
	}
	var boundary activation
	if err := json.Unmarshal([]byte(stored), &boundary); err != nil {
		return activation{}, true, errors.New("通知激活边界无法读取")
	}
	return boundary, true, nil
}

func (s *Service) saveActivation(kind string, boundary activation) error {
	encoded, err := json.Marshal(boundary)
	if err != nil {
		return err
	}
	return s.store.SetState(activationPrefix+kind, string(encoded))
}

// syncLocked records activation boundaries and stops queued messages that
// no longer match the channel settings. Callers hold s.mu.
func (s *Service) syncLocked() error {
	channels := s.cfg.Current().Settings.Notifications
	for _, kind := range config.NotificationChannels {
		channel := channels[kind]
		boundary, stored, err := s.loadActivation(kind)
		if err != nil {
			return err
		}
		if channel.Enabled && !boundary.Enabled {
			// Capture both boundaries in one SQLite snapshot: a newly created
			// run must not finish between separate reads and be skipped forever.
			row := s.store.ReadDB.QueryRowContext(s.ctx, activationBoundaryQuery)
			if err = row.Scan(&boundary.Cutoff, &boundary.Cursor); err != nil {
				return err
			}
		}
		if boundary.Enabled != channel.Enabled || !stored {
			boundary.Enabled = channel.Enabled
			if err = s.saveActivation(kind, boundary); err != nil {
				return err
			}
		}
		switch {
		case !channel.Enabled:
			_, err = s.store.DB.Exec(stopDisabledQuery, timestamp(), kind)
		case channel.OnlyFailure:
			_, err = s.store.DB.Exec(stopSuccessQuery, timestamp(), kind)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// pendingResults reads the next bounded page of terminal results after cursor.
func (s *Service) pendingResults(cursor int64) ([]runs.Run, int64, error) {
	rows, err := s.store.ReadDB.QueryContext(s.ctx, pendingResultsQuery, cursor, resultsPerScan)
	if err != nil {
		return nil, cursor, err
	}
	defer rows.Close()
	results := []runs.Run{}
	for rows.Next() {
		var document []byte
		if err = rows.Scan(&cursor, &document); err != nil {
			return nil, cursor, err
		}
		var run runs.Run
		if err = json.Unmarshal(document, &run); err != nil {
			return nil, cursor, err
		}
		results = append(results, run)
	}
	return results, cursor, rows.Err()
}

// enqueueLocked turns new terminal results into per-channel messages.
// Callers hold s.mu.
func (s *Service) enqueueLocked() error {
	zone, err := s.timezone()
	if err != nil {
		return err
	}
	for _, kind := range config.NotificationChannels {
		channel := s.cfg.Current().Settings.Notifications[kind]
		if !channel.Enabled {
			continue
		}
		boundary, _, err := s.loadActivation(kind)
		if err != nil {
			return err
		}
		// Completion order differs from run ID order: an older paused task can
		// finish after a newer task. Seek a durable insertion cursor so each scan
		// remains bounded without missing those late results or rescanning history.
		results, cursor, err := s.pendingResults(boundary.Cursor)
		if err != nil {
			return err
		}
		for _, run := range results {
			if run.ID <= boundary.Cutoff {
				continue
			}
			message, failed := resultMessage(&run, zone)
			state, reason := "waiting", ""
			if channel.OnlyFailure && !failed {
				state, reason = "stopped", "不符合仅失败通知条件"
			}
			_, err = s.store.DB.Exec(
				enqueueQuery,
				kind, run.ID, message.Event, message.Title, message.Body, failed, state, timestamp(), reason,
			)
			if err != nil {
				return err
			}
		}
		if cursor != boundary.Cursor {
			// Advance only after every queue write succeeds. A crash before this
			// write safely replays the bounded page via UNIQUE(channel, run_id).
			boundary.Cursor = cursor
			if err := s.saveActivation(kind, boundary); err != nil {
				return err
			}
		}
	}
	return nil
}

// queued is one message selected for delivery.
type queued struct {
	record  Record
	message Message
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
	for range sendsPerScan {
		delivered, err := s.deliverNext()
		if err != nil || !delivered {
			return err
		}
	}
	return nil
}

// deliverNext sends the oldest due message and records the outcome. It
// reports false when the queue has no due message or the service is closing.
func (s *Service) deliverNext() (bool, error) {
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return false, nil
	}
	if err := s.syncLocked(); err != nil {
		s.mu.Unlock()
		return false, err
	}
	var next queued
	err := s.store.DB.QueryRow(nextDueQuery, time.Now().UnixMilli()).Scan(
		&next.record.ID, &next.record.Channel, &next.record.RunID, &next.record.Attempts,
		&next.message.Event, &next.message.Title, &next.message.Body,
	)
	if errors.Is(err, sql.ErrNoRows) {
		s.mu.Unlock()
		return false, nil
	}
	if err != nil {
		s.mu.Unlock()
		return false, err
	}
	stored := s.cfg.Current().Settings.Notifications[next.record.Channel]
	channel, err := s.plain(next.record.Channel, stored)
	if err == nil {
		err = Validate(next.record.Channel, channel, true)
	}
	if err != nil {
		_, dbErr := s.store.DB.Exec(invalidChannelQuery, timestamp(), next.record.ID)
		s.mu.Unlock()
		return dbErr == nil, dbErr
	}
	next.record.Attempts++
	_, err = s.store.DB.Exec(markSendingQuery, next.record.Attempts, timestamp(), next.record.ID)
	s.mu.Unlock()
	if err != nil {
		return false, err
	}

	sendErr := send(s.ctx, s.client, next.record.Channel, channel, next.message)

	state, reason, retryAt := "sent", "", int64(0)
	if sendErr != nil {
		reason = sendErr.Error()
		state = "failed"
		if next.record.Attempts < maxAttempts {
			state = "waiting"
			delay := time.Duration(next.record.Attempts) * retryStep
			retryAt = time.Now().Add(delay).UnixMilli()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.store.DB.Exec(finishSendingQuery, state, retryAt, timestamp(), reason, next.record.ID)
	if err == nil {
		err = s.syncLocked()
	}
	return err == nil, err
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
	if err := tx.QueryRowContext(ctx, recordCountQuery).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, recordPageQuery, min(100, max(1, limit)), max(0, offset))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	records := []Record{}
	for rows.Next() {
		var r Record
		if err = rows.Scan(&r.ID, &r.Channel, &r.RunID, &r.State, &r.Attempts, &r.UpdatedAt, &r.Error); err != nil {
			return nil, 0, err
		}
		records = append(records, r)
	}
	return records, total, rows.Err()
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
	defer func() {
		s.mu.Lock()
		delete(s.testing, kind)
		s.mu.Unlock()
	}()
	channel, err := s.plain(kind, s.cfg.Current().Settings.Notifications[kind])
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err = Validate(kind, channel, true); err != nil {
		return err
	}
	message := Message{
		Event: "notification.test",
		Title: "[" + Project + "] · 测试通知",
		Body:  "渠道配置测试成功。自动通知只处理启用后新创建的同步任务。",
	}
	return send(ctx, s.client, kind, channel, message)
}
