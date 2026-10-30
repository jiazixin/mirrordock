package app

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mirrordock/internal/config"
	"mirrordock/internal/notification"
	runengine "mirrordock/internal/run"
)

func TestWriteErrorReportsCodes(t *testing.T) {
	t.Parallel()
	field := &config.FieldError{Path: "sources.0.dest", Code: "reserved_dest", Reason: "must not start with a reserved path"}
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		code    string
		details string
	}{
		{"unknown", errors.New("secret detail"), http.StatusInternalServerError, "internal", "map[]"},
		{"conflict", fmt.Errorf("save: %w", config.ErrConflict), http.StatusPreconditionFailed, "precondition_failed", "map[]"},
		{"field", fmt.Errorf("validate: %w", field), http.StatusBadRequest, "invalid_argument",
			"map[fields:[map[path:sources.0.dest reason:reserved_dest]]]"},
		{"malformed", fmt.Errorf("%w: bad yaml", config.ErrMalformed), http.StatusBadRequest, "config_malformed", "map[]"},
		{"domain", fmt.Errorf("enqueue: %w", runengine.ErrSourceBusy), http.StatusConflict, "source_busy", "map[]"},
		{"delivery", &notification.DeliveryError{Reason: "http_status", Status: 502}, http.StatusServiceUnavailable,
			"notification_failed", "map[reason:http_status status:502]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeError(w, httptest.NewRequest("GET", "/api/config", nil), tc.err)
			var got apiError
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			details := fmt.Sprint(got.Error.Details)
			if w.Code != tc.status || got.Error.Code != tc.code || details != tc.details || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("got %d %s %s, want %d %s %s", w.Code, got.Error.Code, details, tc.status, tc.code, tc.details)
			}
		})
	}
}
