package notification

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"mirrordock/internal/config"
)

// Errors returned by Service methods. The HTTP layer maps them to error codes.
var (
	ErrUnsupportedChannel    = errors.New("unsupported notification channel")
	ErrTestRunning           = errors.New("a test notification is already being sent on this channel")
	ErrCredentialsUnreadable = errors.New("notification credentials cannot be decrypted; check the key backup")
)

// DeliveryError is a failed delivery. Reason is the code clients map to text;
// Status is the receiver's HTTP status when Reason is http_status.
type DeliveryError struct {
	Reason string
	Status int
}

func (e *DeliveryError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("notification delivery failed: %s %d", e.Reason, e.Status)
	}
	return "notification delivery failed: " + e.Reason
}

func deliveryError(reason string) error { return &DeliveryError{Reason: reason} }

// fieldError reports an invalid channel field of an update.
func fieldError(path, code, reason string) error {
	return &config.FieldError{Path: path, Code: code, Reason: reason}
}

// recordFailureOnly is the stored error code of a success notification skipped
// by a failure-only channel. The other codes stored in the error column are
// written by the SQL statements in service.go and by recordCode; a delivery
// failure with an HTTP status is stored as "http_status:<status>".
const recordFailureOnly = "failure_only"

// recordCode is the stored error code of a delivery failure.
func recordCode(err error) string {
	if delivery, ok := errors.AsType[*DeliveryError](err); ok {
		if delivery.Status != 0 {
			return delivery.Reason + ":" + strconv.Itoa(delivery.Status)
		}
		return delivery.Reason
	}
	if errors.Is(err, ErrCredentialsUnreadable) {
		return "credentials_unreadable"
	}
	return "delivery_failed"
}

// setStoredError fills the error fields of r from its stored error column.
func (r *Record) setStoredError(stored string) {
	if stored == "" {
		return
	}
	code, status, found := strings.Cut(stored, ":")
	r.ErrorCode = code
	if found {
		number, _ := strconv.Atoi(status)
		r.ErrorDetails = map[string]any{"status": number}
	}
}
