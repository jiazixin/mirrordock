package notification

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
	runengine "mirrordock/internal/run"
	"mirrordock/internal/timefmt"
)

// activation is the durable boundary of one channel, stored in app_state.
// Cutoff is the largest run ID that existed when the channel was enabled;
// Cursor is the last consumed run_results sequence.
type activation struct {
	Enabled bool  `json:"enabled"`
	Cutoff  int64 `json:"cutoff"`
	Cursor  int64 `json:"cursor"`
}

// ChannelView is a notification channel as shown to the admin console, without
// credentials.
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

// SettingsView lists the notification channels with the configuration revision.
type SettingsView struct {
	Channels []ChannelView `json:"channels"`
	Revision string        `json:"-"`
	Timezone string        `json:"timezone"`
}

// Update is a JSON Merge Patch of one notification channel: nil fields keep
// their stored value.
type Update struct {
	Enabled     *bool
	OnlyFailure *bool
	Endpoint    *string
	Token       *string
	Secret      *string
	ChatID      *string
	// ClearSecret removes the stored secret; it excludes a new Secret.
	ClearSecret bool
}

// Record is one delivered or pending notification.
type Record struct {
	ID        int64        `json:"id"`
	Channel   string       `json:"channel"`
	RunID     int64        `json:"run_id"`
	State     string       `json:"state"`
	Attempts  int          `json:"attempts"`
	UpdatedAt timefmt.Time `json:"updated_at"`
	// ErrorCode and ErrorDetails describe the failure for clients.
	ErrorCode    string         `json:"error_code,omitzero"`
	ErrorDetails map[string]any `json:"error_details,omitzero"`
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

// Service stores channel settings and delivers queued notifications in the
// background.
type Service struct {
	logger *slog.Logger
	now    func() time.Time
	mu     sync.Mutex
	scanMu sync.Mutex
	// testsInFlight marks channels with a test message being sent; guarded
	// by mu.
	testsInFlight map[string]bool
	cfg           *config.Manager
	store         *database.Store
	aead          cipher.AEAD
	client        *http.Client
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	lifecycleMu   sync.Mutex
	started       bool
	stopped       bool
	done          chan struct{}
}

func (s *Service) timestamp() timefmt.Time { return timefmt.From(s.now()) }

// New creates the notification service for cfg and store.
func New(ctx context.Context, cfg *config.Manager, store *database.Store, logger *slog.Logger) (*Service, error) {
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
	s := &Service{
		logger:        logger,
		now:           time.Now,
		cfg:           cfg,
		store:         store,
		aead:          key,
		client:        newClient(),
		testsInFlight: map[string]bool{},
	}
	if err = s.ValidateConfig(cfg.Current()); err != nil {
		return nil, err
	}
	// A send interrupted by a restart may or may not have been delivered.
	if err = store.RecoverNotifications(ctx, s.timestamp()); err != nil {
		return nil, err
	}
	if err = s.Sync(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// ValidateConfig checks that the notification channels in c can be decrypted
// and are valid.
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

// Start begins delivery after the HTTP listener has been bound.
// Shutdown must finish before the shared store is closed.
func (s *Service) Start(ctx context.Context) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopped {
		return errors.New("notification service is stopped")
	}
	if s.started {
		return nil
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.started = true
	s.done = make(chan struct{})
	s.wg.Go(func() {
		defer close(s.done)
		ticker := time.NewTicker(scanInterval)
		defer ticker.Stop()
		for {
			if err := s.Scan(ctx); err != nil && ctx.Err() == nil {
				s.logger.Error("notification_scan_failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	return nil
}

// Shutdown cancels deliveries and waits within ctx.
func (s *Service) Shutdown(ctx context.Context) error {
	s.lifecycleMu.Lock()
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	s.lifecycleMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.wg.Wait()
	s.client.CloseIdleConnections()
	return nil
}

func (s *Service) timezone(ctx context.Context) (string, error) {
	zone, err := s.store.State(ctx, timezoneStateKey)
	if zone == "" {
		zone = defaultTimezone
	}
	return zone, err
}

// SetTimezone sets the IANA time zone used in notification text.
func (s *Service) SetTimezone(ctx context.Context, zone string) error {
	if zone == "" || zone == "Local" || len(zone) > maxTimezoneLength {
		return fieldError("timezone", "invalid_timezone", "must be an IANA time zone")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return fieldError("timezone", "invalid_timezone", "must be an IANA time zone")
	}
	return s.store.SetState(ctx, timezoneStateKey, zone)
}

// View returns the notification channels for the admin console.
func (s *Service) View(ctx context.Context) (SettingsView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.cfg.View()
	out := SettingsView{Channels: []ChannelView{}, Revision: cfg.Revision}
	var err error
	out.Timezone, err = s.timezone(ctx)
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
func (s *Service) Save(ctx context.Context, kind string, u Update, revision string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(config.NotificationChannels, kind) {
		return ErrUnsupportedChannel
	}
	fields := config.ChannelFields{
		Endpoint: u.Endpoint != nil,
		Token:    u.Token != nil,
		ChatID:   u.ChatID != nil,
		Secret:   u.Secret != nil || u.ClearSecret,
	}
	if !fields.AllowedFor(kind) {
		return fieldError("", "fields_not_for_channel", "fields do not match the channel")
	}
	if u.ClearSecret && u.Secret != nil {
		return fieldError("secret", "clear_with_new_secret", "cannot clear and set the secret together")
	}
	channels := maps.Clone(s.cfg.Current().Settings.Notifications)
	if channels == nil {
		channels = map[string]config.Notification{}
	}
	channel, err := s.plain(kind, channels[kind])
	if err != nil {
		return err
	}
	if u.Enabled != nil {
		channel.Enabled = *u.Enabled
	}
	if u.OnlyFailure != nil {
		channel.OnlyFailure = *u.OnlyFailure
	}
	if u.Endpoint != nil {
		channel.Endpoint = *u.Endpoint
	}
	if u.Token != nil {
		channel.Token = *u.Token
	}
	if u.Secret != nil {
		channel.Secret = *u.Secret
	}
	if u.ClearSecret {
		channel.Secret = ""
	}
	if u.ChatID != nil {
		channel.ChatID = *u.ChatID
	}
	if err = Validate(kind, channel, channel.Enabled); err != nil {
		return err
	}
	channel.Endpoint = s.encrypt(kind, "endpoint", channel.Endpoint)
	channel.Token = s.encrypt(kind, "token", channel.Token)
	channel.Secret = s.encrypt(kind, "secret", channel.Secret)
	channels[kind] = channel
	if err = s.cfg.SaveNotifications(channels, revision); err != nil {
		return err
	}
	return s.syncLocked(ctx)
}

// Sync queues notifications for run results that are not queued yet.
func (s *Service) Sync(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncLocked(ctx)
}

func (s *Service) loadActivation(ctx context.Context, kind string) (activation, bool, error) {
	stored, err := s.store.State(ctx, activationPrefix+kind)
	if err != nil || stored == "" {
		return activation{}, false, err
	}
	var boundary activation
	if err := json.Unmarshal([]byte(stored), &boundary); err != nil {
		return activation{}, true, errors.New("cannot read the notification activation boundary")
	}
	return boundary, true, nil
}

func (s *Service) saveActivation(ctx context.Context, kind string, boundary activation) error {
	encoded, err := json.Marshal(boundary)
	if err != nil {
		return err
	}
	return s.store.SetState(ctx, activationPrefix+kind, string(encoded))
}

// syncLocked records activation boundaries and stops queued messages that
// no longer match the channel settings. Callers hold s.mu.
func (s *Service) syncLocked(ctx context.Context) error {
	channels := s.cfg.Current().Settings.Notifications
	for _, kind := range config.NotificationChannels {
		channel := channels[kind]
		boundary, stored, err := s.loadActivation(ctx, kind)
		if err != nil {
			return err
		}
		if channel.Enabled && !boundary.Enabled {
			// Capture both boundaries in one SQLite snapshot: a newly created
			// run must not finish between separate reads and be skipped forever.
			boundary.Cutoff, boundary.Cursor, err = s.store.NotificationBoundary(ctx)
			if err != nil {
				return err
			}
		}
		if boundary.Enabled != channel.Enabled || !stored {
			boundary.Enabled = channel.Enabled
			if err = s.saveActivation(ctx, kind, boundary); err != nil {
				return err
			}
		}
		switch {
		case !channel.Enabled:
			err = s.store.StopNotifications(ctx, database.NotificationDraft{Channel: kind, UpdatedAt: s.timestamp()})
		case channel.OnlyFailure:
			err = s.store.StopNotifications(ctx, database.NotificationDraft{Channel: kind, Failure: true, UpdatedAt: s.timestamp()})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// pendingResults reads the next bounded page of terminal results after cursor.
func (s *Service) pendingResults(ctx context.Context, cursor int64) ([]runengine.Run, int64, error) {
	records, err := s.store.PendingResults(ctx, cursor, resultsPerScan)
	if err != nil {
		return nil, cursor, err
	}
	results := make([]runengine.Run, 0, len(records))
	for _, record := range records {
		var run runengine.Run
		if err := json.Unmarshal(record.Document, &run); err != nil {
			return nil, cursor, err
		}
		results = append(results, run)
		cursor = record.Sequence
	}
	return results, cursor, nil
}

// enqueueLocked turns new terminal results into per-channel messages.
// Callers hold s.mu.
func (s *Service) enqueueLocked(ctx context.Context) error {
	zone, err := s.timezone(ctx)
	if err != nil {
		return err
	}
	for _, kind := range config.NotificationChannels {
		channel := s.cfg.Current().Settings.Notifications[kind]
		if !channel.Enabled {
			continue
		}
		boundary, _, err := s.loadActivation(ctx, kind)
		if err != nil {
			return err
		}
		// Completion order differs from run ID order: an older paused task can
		// finish after a newer task. Seek a durable insertion cursor so each scan
		// remains bounded without missing those late results or rescanning history.
		results, cursor, err := s.pendingResults(ctx, boundary.Cursor)
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
				state, reason = "stopped", recordFailureOnly
			}
			err = s.store.EnqueueNotification(ctx, database.NotificationDraft{
				Channel: kind, RunID: run.ID, Event: message.Event, Title: message.Title, Body: message.Body,
				Failure: failed, State: state, UpdatedAt: s.timestamp(), Error: reason,
			})
			if err != nil {
				return err
			}
		}
		if cursor != boundary.Cursor {
			// Advance only after every queue write succeeds. A crash before this
			// write safely replays the bounded page via UNIQUE(channel, run_id).
			boundary.Cursor = cursor
			if err := s.saveActivation(ctx, kind, boundary); err != nil {
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
func (s *Service) Scan(ctx context.Context) error {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return nil
	}
	err := s.syncLocked(ctx)
	if err == nil {
		err = s.enqueueLocked(ctx)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	// Bound each pass so shutdown/settings remain responsive even with a backlog.
	for range sendsPerScan {
		delivered, err := s.deliverNext(ctx)
		if err != nil || !delivered {
			return err
		}
	}
	return nil
}

// deliverNext sends the oldest due message and records the outcome. It
// reports false when the queue has no due message or the service is closing.
func (s *Service) deliverNext(ctx context.Context) (bool, error) {
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return false, nil
	}
	if err := s.syncLocked(ctx); err != nil {
		s.mu.Unlock()
		return false, err
	}
	pending, err := s.store.NextNotification(ctx, s.now())
	next := queued{record: Record{ID: pending.ID, Channel: pending.Channel, RunID: pending.RunID, Attempts: pending.Attempts},
		message: Message{Event: pending.Event, Title: pending.Title, Body: pending.Body}}
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
		dbErr := s.store.InvalidNotification(ctx, database.NotificationOutcome{ID: next.record.ID, UpdatedAt: s.timestamp()})
		s.mu.Unlock()
		return dbErr == nil, dbErr
	}
	next.record.Attempts++
	err = s.store.MarkNotificationSending(ctx, database.NotificationOutcome{ID: next.record.ID, Attempts: next.record.Attempts, UpdatedAt: s.timestamp()})
	s.mu.Unlock()
	if err != nil {
		return false, err
	}

	sendErr := send(ctx, sendOptions{client: s.client, kind: next.record.Channel, c: channel, m: next.message})

	state, reason := "sent", ""
	var retryAt time.Time
	if sendErr != nil {
		reason = recordCode(sendErr)
		state = "failed"
		if next.record.Attempts < maxAttempts {
			state = "waiting"
			delay := time.Duration(next.record.Attempts) * retryStep
			retryAt = s.now().Add(delay)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Persist the outcome even when shutdown canceled delivery; the next process
	// must see a retryable result rather than an indefinitely sending entry.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), database.OperationTimeout)
	defer cancel()
	err = s.store.FinishNotification(persistCtx, database.NotificationOutcome{ID: next.record.ID, State: state, RetryAt: retryAt, UpdatedAt: s.timestamp(), Error: reason})
	if err == nil {
		err = s.syncLocked(ctx)
	}
	return err == nil, err
}

// RecordPage returns a page of notification records and the total count.
func (s *Service) RecordPage(ctx context.Context, limit, offset int) ([]Record, int, error) {
	stored, total, err := s.store.NotificationRecords(ctx, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	records := make([]Record, 0, len(stored))
	for _, row := range stored {
		record := Record{ID: row.ID, Channel: row.Channel, RunID: row.RunID, State: row.State, Attempts: row.Attempts, UpdatedAt: row.UpdatedAt}
		record.setStoredError(row.Error)
		records = append(records, record)
	}
	return records, total, nil
}

// Test sends a test notification through the channel of kind.
func (s *Service) Test(ctx context.Context, kind string) error {
	s.mu.Lock()
	if !slices.Contains(config.NotificationChannels, kind) {
		s.mu.Unlock()
		return ErrUnsupportedChannel
	}
	if s.testsInFlight[kind] {
		s.mu.Unlock()
		return ErrTestRunning
	}
	s.testsInFlight[kind] = true
	defer func() {
		s.mu.Lock()
		delete(s.testsInFlight, kind)
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
	return send(ctx, sendOptions{client: s.client, kind: kind, c: channel, m: message})
}
