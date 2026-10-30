package app

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strings"

	"mirrordock/internal/config"
	"mirrordock/internal/notification"
	runengine "mirrordock/internal/run"
)

// apiError is the body of every failed API response. Code is the only value
// clients branch on; Message is English text for developers and is never shown
// to users. See docs/API.md.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitzero"`
}

// apiFailure is an error reported to the client with a fixed status and code.
type apiFailure struct {
	status  int
	code    string
	message string
	details map[string]any
}

func (e *apiFailure) Error() string { return e.code + ": " + e.message }

func newFailure(status int, code, message string) *apiFailure {
	return &apiFailure{status: status, code: code, message: message}
}

// with returns a copy of the failure with one more details entry.
func (e *apiFailure) with(key string, value any) *apiFailure {
	details := make(map[string]any, len(e.details)+1)
	for k, v := range e.details {
		details[k] = v
	}
	details[key] = value
	copied := *e
	copied.details = details
	return &copied
}

// Failures shared by the API middleware and handlers.
var (
	errNoRoute          = newFailure(http.StatusNotFound, "not_found", "no such API endpoint")
	errMethodNotAllowed = newFailure(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	errNotJSON          = newFailure(http.StatusUnsupportedMediaType, "unsupported_media_type", "request body must be application/json")
	errIfMatchRequired  = newFailure(http.StatusPreconditionFailed, "precondition_failed", "If-Match must name the edited revision")
	errKeyReused        = newFailure(http.StatusConflict, "idempotency_key_reused", "Idempotency-Key was used with a different request")
	errInternal         = newFailure(http.StatusInternalServerError, "internal", "internal error")
	errShuttingDown     = newFailure(http.StatusServiceUnavailable, "unavailable", "service is shutting down")
	errOriginRejected   = newFailure(http.StatusForbidden, "permission_denied", "cross-origin request rejected")
	errLoginRequired    = newFailure(http.StatusUnauthorized, "unauthenticated", "login required")
	errLoginLimited     = newFailure(http.StatusTooManyRequests, "rate_limited", "too many login attempts; retry in a minute")
	errBadLogin         = newFailure(http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
	errRunNotFound      = newFailure(http.StatusNotFound, "not_found", "run not found")
	errQueryFailed      = newFailure(http.StatusServiceUnavailable, "unavailable", "query temporarily unavailable")
	errStreamsFull      = newFailure(http.StatusServiceUnavailable, "event_streams_full", "too many event streams")
)

// domainFailures maps sentinel errors of the domain packages to their status
// and code. The first match in the error chain wins.
var domainFailures = []struct {
	err    error
	status int
	code   string
}{
	{config.ErrConflict, http.StatusPreconditionFailed, "precondition_failed"},
	{config.ErrMalformed, http.StatusBadRequest, "config_malformed"},
	{runengine.ErrStopped, http.StatusServiceUnavailable, "unavailable"},
	{runengine.ErrRestartRequired, http.StatusConflict, "restart_required"},
	{runengine.ErrReservedSourceChange, http.StatusConflict, "source_in_use"},
	{runengine.ErrSourceNotFound, http.StatusNotFound, "source_not_found"},
	{runengine.ErrSourceDisabled, http.StatusConflict, "source_disabled"},
	{runengine.ErrSourceBusy, http.StatusConflict, "source_busy"},
	{runengine.ErrSourceRemoved, http.StatusConflict, "source_removed"},
	{runengine.ErrSourceChanged, http.StatusConflict, "source_changed"},
	{runengine.ErrRunNotActive, http.StatusConflict, "run_not_active"},
	{runengine.ErrNotPausable, http.StatusConflict, "run_not_pausable"},
	{runengine.ErrExecutorStopping, http.StatusConflict, "executor_stopping"},
	{runengine.ErrNotResumable, http.StatusConflict, "run_not_resumable"},
	{runengine.ErrBudgetExhausted, http.StatusConflict, "budget_exhausted"},
	{runengine.ErrUnknownAction, http.StatusBadRequest, "unknown_action"},
	{runengine.ErrAttemptNotFound, http.StatusNotFound, "attempt_not_found"},
	{runengine.ErrLogCleanupRunning, http.StatusConflict, "log_cleanup_running"},
	{runengine.ErrInvalidHistory, http.StatusInternalServerError, "invalid_history"},
	{notification.ErrUnsupportedChannel, http.StatusBadRequest, "unsupported_channel"},
	{notification.ErrTestRunning, http.StatusConflict, "notification_test_running"},
	{notification.ErrCredentialsUnreadable, http.StatusInternalServerError, "credentials_unreadable"},
	{sql.ErrNoRows, http.StatusNotFound, "not_found"},
}

// writeError writes err as the JSON error response. Field errors become 400
// invalid_argument; errors without a known mapping become 500 internal, are
// logged and do not expose their text.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	failure := errorResponse(r, err)
	respond(w, failure.status, apiError{Error: apiErrorBody{Code: failure.code, Message: failure.message, Details: failure.details}})
}

func errorResponse(r *http.Request, err error) *apiFailure {
	if known, ok := errors.AsType[*apiFailure](err); ok {
		return known
	}
	if field, ok := errors.AsType[*config.FieldError](err); ok {
		return newFailure(http.StatusBadRequest, "invalid_argument", field.Error()).
			with("fields", []map[string]string{{"path": field.Path, "reason": field.Code}})
	}
	if delivery, ok := errors.AsType[*notification.DeliveryError](err); ok {
		failure := newFailure(http.StatusServiceUnavailable, "notification_failed", delivery.Error()).with("reason", delivery.Reason)
		if delivery.Status != 0 {
			failure = failure.with("status", delivery.Status)
		}
		return failure
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return newFailure(http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
	}
	for _, known := range domainFailures {
		if errors.Is(err, known.err) {
			return newFailure(known.status, known.code, known.err.Error())
		}
	}
	requestLogger(r).Error("request_failed", "error", err)
	return errInternal
}

// decodeError converts an error from decoding a JSON request body into a
// field error or a payload_too_large failure.
func decodeError(err error) error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return err
	}
	if semantic, ok := errors.AsType[*json.SemanticError](err); ok {
		path := strings.ReplaceAll(strings.TrimPrefix(string(semantic.JSONPointer), "/"), "/", ".")
		if errors.Is(err, json.ErrUnknownName) {
			return &config.FieldError{Path: path, Code: "unknown_field", Reason: "is not an allowed field"}
		}
		return &config.FieldError{Path: path, Code: "invalid_type", Reason: "has the wrong JSON type"}
	}
	return &config.FieldError{Code: "invalid_json", Reason: "request body must be one valid JSON object"}
}
