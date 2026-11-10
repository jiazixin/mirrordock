package run

import "errors"

// Errors returned by Engine methods. The HTTP layer maps them to error codes.
var (
	// ErrStopped rejects new work after shutdown begins.
	ErrStopped              = errors.New("run engine is stopped")
	ErrRestartRequired      = errors.New("data directory or mirror root changed; restart required")
	ErrReservedSourceChange = errors.New("cannot remove, retype or move the dest of a source with an active, paused or interrupted run")
	ErrSourceNotFound       = errors.New("source not found")
	ErrSourceDisabled       = errors.New("source disabled")
	ErrSourceBusy           = errors.New("source already has an active, paused or interrupted run")
	ErrSourceRemoved        = errors.New("source removed from the configuration; cancel the run")
	ErrSourceChanged        = errors.New("source type or dest changed; cancel the run")
	ErrRunNotActive         = errors.New("only active or interrupted runs accept actions")
	ErrNotPausable          = errors.New("run cannot be paused")
	ErrExecutorStopping     = errors.New("executor is stopping; resume after it exits")
	ErrNotResumable         = errors.New("run cannot be resumed")
	ErrBudgetExhausted      = errors.New("run budget exhausted")
	ErrUnknownAction        = errors.New("unknown run action")
	ErrNegativeOffset       = errors.New("log offset must not be negative")
	ErrAttemptNotFound      = errors.New("attempt not found")
	ErrLogCleanupRunning    = errors.New("a log cleanup is already running")
	ErrInvalidHistory       = errors.New("invalid run ID in history; log cleanup stopped")
)

// Run error codes. Codes of executor failures come from syncer.Failure.
const (
	codeServiceInterrupted = "service_interrupted"
	codeTaskTimeout        = "task_timeout"
	codePersistenceFailed  = "persistence_failed"
	codeDiskSpace          = "disk_space"
	codeSourceExhausted    = "source_exhausted"
)

// Event codes select the client's text for an entry in the run history.
const (
	eventQueued      = "queued"
	eventRequeued    = "requeued_after_restart"
	eventRunError    = "run_error"
	eventAction      = "action"
	eventWaitingLoad = "waiting_downloads"
	eventWaitingDisk = "waiting_space"
	eventStarted     = "started"
	eventState       = "state"
	eventReusedWork  = "reused_work"
	eventCandidate   = "candidate"
	eventRetry       = "retry_scheduled"
)
