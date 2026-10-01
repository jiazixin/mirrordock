package app

import (
	"context"
	"log/slog"
	"mirrordock/internal/runs"
	"net/http"
	"strconv"
	"time"
)

func (a *App) analytics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("type")
	days := 30
	if q.Has("days") {
		var err error
		days, err = strconv.Atoi(q.Get("days"))
		if err != nil {
			respond(w, 400, map[string]string{"error": "时间范围须为 7、30 或 90 天"})
			return
		}
	}
	if (kind != "rsync" && kind != "github_release") || (days != 7 && days != 30 && days != 90) || len(q.Get("source")) > 128 {
		respond(w, 400, map[string]string{"error": "分析类型或范围无效"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := runs.Analyze(ctx, a.Store, kind, q.Get("source"), days, time.Now(), a.location)
	if err != nil {
		slog.Error("query analytics", "error", err)
		respond(w, 503, map[string]string{"error": "分析暂时不可用，请稍后重试"})
		return
	}
	respond(w, 200, result)
}
