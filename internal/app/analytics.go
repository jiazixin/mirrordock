package app

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"mirrordock/internal/config"
	"mirrordock/internal/database"
)

func (a *App) analytics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("type")
	days := 30
	if q.Has("days") {
		var err error
		days, err = strconv.Atoi(q.Get("days"))
		if err != nil {
			writeError(w, r, &config.FieldError{Path: "days", Code: "invalid_days", Reason: "must be 7, 30 or 90"})
			return
		}
	}
	validKind := kind == "rsync" || kind == "reposync" || kind == "github_release"
	validDays := days == 7 || days == 30 || days == 90
	if !validKind || !validDays || len(q.Get("source")) > 128 {
		writeError(w, r, &config.FieldError{Path: "type", Code: "invalid_value", Reason: "invalid analytics type, days or source"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := a.Store.Analyze(ctx, database.AnalyzeOptions{Kind: kind, Source: q.Get("source"), Days: days, Now: time.Now(), Location: a.location})
	if err != nil {
		requestLogger(r).Error("analytics_query_failed", "error", err)
		writeError(w, r, newFailure(http.StatusServiceUnavailable, "unavailable", "analytics temporarily unavailable"))
		return
	}
	respond(w, http.StatusOK, result)
}
