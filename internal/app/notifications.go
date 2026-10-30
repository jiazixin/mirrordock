package app

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"

	"mirrordock/internal/config"
	"mirrordock/internal/notification"
)

func (a *App) notificationRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/notifications", a.serveNotifications)
	api.HandleFunc("PATCH /api/notifications/{channel}", a.patchNotification)
	api.HandleFunc("POST /api/notifications/{channel}/test", emptyBody(a.testNotification))
	api.HandleFunc("GET /api/notifications/records", a.notificationRecords)
	api.HandleFunc("PUT /api/notifications/timezone", a.setNotificationTimezone)
}

func (a *App) serveNotifications(w http.ResponseWriter, r *http.Request) {
	view, err := a.Notifications.View(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	setETag(w, view.Revision)
	respond(w, http.StatusOK, view)
}

func (a *App) patchNotification(w http.ResponseWriter, r *http.Request) {
	revision, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	update, err := decodeChannelPatch(w, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	save := func() error { return a.Notifications.Save(r.Context(), r.PathValue("channel"), update, revision) }
	if err := a.Runs.NotificationChange(save); err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.Store.SetState(r.Context(), "last_notification_change", "settings updated"); err != nil {
		requestLogger(r).Error("notification_audit_failed", "error", err)
	}
	a.serveNotifications(w, r)
}

// decodeChannelPatch reads a JSON Merge Patch of one channel. Only the secret
// can be cleared with null; unknown members are rejected.
func decodeChannelPatch(w http.ResponseWriter, r *http.Request) (notification.Update, error) {
	var members map[string]jsontext.Value
	var update notification.Update
	if err := decode(w, r, &members); err != nil {
		return update, err
	}
	for name, value := range members {
		null := value.Kind() == 'n'
		var target any
		switch name {
		case "enabled":
			target = &update.Enabled
		case "only_failure":
			target = &update.OnlyFailure
		case "endpoint":
			target = &update.Endpoint
		case "token":
			target = &update.Token
		case "chat_id":
			target = &update.ChatID
		case "secret":
			if null {
				update.ClearSecret = true
				continue
			}
			target = &update.Secret
		default:
			return update, &config.FieldError{Path: name, Code: "unknown_field", Reason: "is not an allowed field"}
		}
		if null {
			return update, &config.FieldError{Path: name, Code: "not_nullable", Reason: "cannot be cleared"}
		}
		if err := json.Unmarshal(value, target); err != nil {
			return update, &config.FieldError{Path: name, Code: "invalid_type", Reason: "has the wrong JSON type"}
		}
	}
	return update, nil
}

func (a *App) testNotification(w http.ResponseWriter, r *http.Request) {
	if err := a.Notifications.Test(r.Context(), r.PathValue("channel")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) notificationRecords(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		writeError(w, r, err)
		return
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil {
		writeError(w, r, err)
		return
	}
	records, total, err := a.Notifications.RecordPage(r.Context(), int(limit), int(offset))
	if err != nil {
		requestLogger(r).Error("notification_records_failed", "error", err)
		writeError(w, r, newFailure(http.StatusServiceUnavailable, "unavailable", "reading delivery records failed"))
		return
	}
	respond(w, http.StatusOK, map[string]any{"items": records, "total": total, "limit": limit, "offset": offset})
}

func (a *App) setNotificationTimezone(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Timezone string `json:"timezone"`
	}
	if err := decode(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := a.Notifications.SetTimezone(r.Context(), body.Timezone); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
