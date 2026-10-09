package app

import (
	"mirrordock/internal/notifications"
	"net/http"
	"strconv"
)

func (a *App) notificationRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/notifications", func(w http.ResponseWriter, r *http.Request) {
		view, err := a.Notifications.View()
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, view)
	})
	api.HandleFunc("PUT /api/notifications/{channel}", func(w http.ResponseWriter, r *http.Request) {
		var body notifications.Update
		if err := decode(w, r, &body); err != nil {
			respond(w, 400, map[string]string{"error": "通知请求格式无效"})
			return
		}
		if err := a.Runs.NotificationChange(func() error { return a.Notifications.Save(r.PathValue("channel"), body) }); err != nil {
			failure(w, err)
			return
		}
		_ = a.Store.SetState("last_notification_change", "settings updated")
		view, err := a.Notifications.View()
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, view)
	})
	api.HandleFunc("POST /api/notifications/{channel}/test", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Notifications.Test(r.Context(), r.PathValue("channel")); err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
	api.HandleFunc("GET /api/notifications/records", func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if r.URL.Query().Has("limit") {
			limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		records, total, err := a.Notifications.RecordPage(r.Context(), limit, offset)
		if err != nil {
			respond(w, 500, map[string]string{"error": "发送记录读取失败"})
			return
		}
		respond(w, 200, map[string]any{"items": records, "total": total})
	})
	api.HandleFunc("POST /api/notifications/timezone", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Timezone string `json:"timezone"`
		}
		if err := decode(w, r, &body); err != nil {
			respond(w, 400, map[string]string{"error": "时区请求格式无效"})
			return
		}
		if err := a.Notifications.SetTimezone(body.Timezone); err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
}
