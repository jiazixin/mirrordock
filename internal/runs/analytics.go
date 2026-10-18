package runs

import (
	"context"
	"database/sql"
	"fmt"
	"mirrordock/internal/database"
	"slices"
	"strings"
	"time"
)

// Analytics contains only aggregate facts, never configuration, URLs or logs.
type AnalyticsStats struct {
	Runs              int     `json:"runs"`
	Succeeded         int     `json:"succeeded"`
	Failed            int     `json:"failed"`
	Cancelled         int     `json:"cancelled"`
	Active            int     `json:"active"`
	DurationSamples   int     `json:"duration_samples"`
	ElapsedSeconds    float64 `json:"elapsed_seconds"`
	Bytes             int64   `json:"bytes"`
	Files             int64   `json:"files"`
	Completed         int64   `json:"completed"`
	Reused            int64   `json:"reused"`
	Attempts          int     `json:"attempts"`
	FailedAttempts    int     `json:"failed_attempts"`
	RetriedRuns       int     `json:"retried_runs"`
	FallbackSucceeded int     `json:"fallback_succeeded"`
}

type AnalyticsDay struct {
	Date string `json:"date"`
	AnalyticsStats
}

type AnalyticsSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	AnalyticsStats
}

type AnalyticsError struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

type Analytics struct {
	Type     string            `json:"type"`
	From     time.Time         `json:"from"`
	Until    time.Time         `json:"until"`
	Timezone string            `json:"timezone"`
	Summary  AnalyticsStats    `json:"summary"`
	Days     []AnalyticsDay    `json:"days"`
	Sources  []AnalyticsSource `json:"sources"`
	Errors   []AnalyticsError  `json:"errors"`
}

// analyticsProjection selects one row of aggregate facts per run without
// loading full run snapshots or raw attempt errors. Its six parameters are
// the lower bound, the coarse and exact upper bounds, the type and the
// optional source ID (twice).
const analyticsProjection = `WITH facts AS (
 SELECT
  source_id,
  created_at,
  state,
  COALESCE(json_extract(data, '$.name'), source_id) AS name,
  COALESCE(json_extract(data, '$.error_code'), 'UNKNOWN') AS error_code,
  CASE WHEN state IN ('succeeded', 'failed', 'timed_out')
        AND json_extract(data, '$.started_at') IS NOT NULL
        AND COALESCE(json_extract(data, '$.elapsed_seconds'), 0) > 0
   THEN 1 ELSE 0 END AS duration_samples,
  CASE WHEN state IN ('succeeded', 'failed', 'timed_out')
        AND json_extract(data, '$.started_at') IS NOT NULL
   THEN MAX(0, COALESCE(json_extract(data, '$.elapsed_seconds'), 0)) ELSE 0 END AS elapsed,
  CASE WHEN state = 'succeeded'
   THEN MAX(0, COALESCE(json_extract(data, '$.progress.bytes'), 0)) ELSE 0 END AS bytes,
  CASE WHEN state = 'succeeded'
   THEN MAX(0, COALESCE(json_extract(data, '$.progress.files'), 0)) ELSE 0 END AS files,
  CASE WHEN state = 'succeeded'
   THEN MAX(0, COALESCE(json_extract(data, '$.progress.success'), 0)) ELSE 0 END AS completed,
  CASE WHEN state = 'succeeded'
   THEN MAX(0, MIN(
    COALESCE(json_extract(data, '$.progress.skipped'), 0),
    COALESCE(json_extract(data, '$.progress.success'), 0)
   )) ELSE 0 END AS reused,
  COALESCE(json_array_length(data, '$.attempts'), 0) AS attempts,
  (SELECT COUNT(*) FROM json_each(data, '$.attempts')
    WHERE COALESCE(json_extract(value, '$.error_code'), '')
     NOT IN ('', 'PAUSED', 'PAUSING', 'CANCELLED', 'INTERRUPTED')) AS failed_attempts,
  CASE WHEN COALESCE(json_extract(data, '$.retry_count'), 0) > 0 THEN 1 ELSE 0 END AS retried,
  CASE WHEN state = 'succeeded' AND COALESCE(json_extract(data, '$.candidate_index'), 0) > 0
   THEN 1 ELSE 0 END AS fallback
 FROM runs
 WHERE created_at >= ? AND created_at < ? AND RTRIM(created_at, 'Z') <= ?
  AND json_extract(data, '$.type') = ?
  AND (? = '' OR source_id = ?)
) `

// analyticsSums aggregates the projection in the field order of statFields.
const analyticsSums = `COUNT(*),
 SUM(state = 'succeeded'),
 SUM(state IN ('failed', 'timed_out')),
 SUM(state = 'cancelled'),
 SUM(state NOT IN ('succeeded', 'failed', 'timed_out', 'cancelled')),
 SUM(duration_samples), SUM(elapsed), SUM(bytes), SUM(files), SUM(completed), SUM(reused),
 SUM(attempts), SUM(failed_attempts), SUM(retried), SUM(fallback)`

const dailyQuery = analyticsProjection + "SELECT created_at, " + analyticsSums + " FROM facts GROUP BY created_at"

// sourceQuery returns the 20 busiest sources under their latest historical
// name, even after a source is renamed or removed from YAML. The name lookup
// repeats the first four projection parameters.
const sourceQuery = analyticsProjection + `SELECT
 source_id,
 (SELECT COALESCE(json_extract(r.data, '$.name'), r.source_id) FROM runs r
   WHERE r.source_id = facts.source_id
    AND r.created_at >= ? AND r.created_at < ? AND RTRIM(r.created_at, 'Z') <= ?
    AND json_extract(r.data, '$.type') = ?
   ORDER BY r.id DESC LIMIT 1),
 ` + analyticsSums + `
 FROM facts GROUP BY source_id ORDER BY COUNT(*) DESC, source_id LIMIT 20`

const errorQuery = analyticsProjection + `SELECT COALESCE(NULLIF(error_code, ''), 'UNKNOWN'), COUNT(*)
 FROM facts WHERE state IN ('failed', 'timed_out')
 GROUP BY 1 ORDER BY COUNT(*) DESC, 1 LIMIT 8`

// statFields returns scan destinations in the column order of analyticsSums.
func statFields(s *AnalyticsStats) []any {
	return []any{
		&s.Runs, &s.Succeeded, &s.Failed, &s.Cancelled, &s.Active,
		&s.DurationSamples, &s.ElapsedSeconds, &s.Bytes, &s.Files, &s.Completed, &s.Reused,
		&s.Attempts, &s.FailedAttempts, &s.RetriedRuns, &s.FallbackSucceeded,
	}
}

func (s *AnalyticsStats) add(v AnalyticsStats) {
	s.Runs += v.Runs
	s.Succeeded += v.Succeeded
	s.Failed += v.Failed
	s.Cancelled += v.Cancelled
	s.Active += v.Active
	s.DurationSamples += v.DurationSamples
	s.ElapsedSeconds += v.ElapsedSeconds
	s.Bytes += v.Bytes
	s.Files += v.Files
	s.Completed += v.Completed
	s.Reused += v.Reused
	s.Attempts += v.Attempts
	s.FailedAttempts += v.FailedAttempts
	s.RetriedRuns += v.RetriedRuns
	s.FallbackSucceeded += v.FallbackSucceeded
}

// Analyze includes every run created in the window. Retained Go memory is bounded by days
// and the ranked SQL result limits; no run blobs or logs are retained in Go.
func Analyze(
	ctx context.Context,
	store *database.Store,
	kind, source string,
	days int,
	now time.Time,
	location *time.Location,
) (*Analytics, error) {
	validKind := kind == "rsync" || kind == "reposync" || kind == "github_release"
	if !validKind || (days != 7 && days != 30 && days != 90) {
		return nil, fmt.Errorf("无效分析范围")
	}
	local := now.In(location)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	from := midnight.AddDate(0, 0, -days+1)
	out := &Analytics{
		Type:     kind,
		From:     from,
		Until:    now,
		Timezone: location.String(),
		Days:     make([]AnalyticsDay, days),
		Sources:  []AnalyticsSource{},
		Errors:   []AnalyticsError{},
	}
	dayIndex := make(map[string]int, days)
	for i := range days {
		day := from.AddDate(0, 0, i).Format(time.DateOnly)
		out.Days[i].Date = day
		dayIndex[day] = i
	}
	// Lower/upper prefixes preserve the index while comparing fractional and
	// whole-second RFC3339 UTC strings correctly at midnight and at now.
	const wholeSecond = "2006-01-02T15:04:05"
	lower := from.UTC().Format(wholeSecond)
	coarseUpper := now.UTC().Add(time.Second).Format(wholeSecond)
	exactUpper := strings.TrimSuffix(now.UTC().Format(time.RFC3339Nano), "Z")
	args := []any{lower, coarseUpper, exactUpper, kind, source, source}

	tx, err := store.ReadDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := addDays(ctx, tx, args, out, dayIndex, location); err != nil {
		return nil, err
	}
	sourceArgs := append(slices.Clone(args), lower, coarseUpper, exactUpper, kind)
	if err := addSources(ctx, tx, sourceArgs, out); err != nil {
		return nil, err
	}
	return out, addErrors(ctx, tx, args, out)
}

// addDays groups identical instants in SQLite and assigns calendar days in the
// startup location, including daylight-saving boundaries, in constant memory.
func addDays(
	ctx context.Context,
	tx *sql.Tx,
	args []any,
	out *Analytics,
	dayIndex map[string]int,
	location *time.Location,
) error {
	rows, err := tx.QueryContext(ctx, dailyQuery, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var created string
		var stats AnalyticsStats
		if err := rows.Scan(append([]any{&created}, statFields(&stats)...)...); err != nil {
			return err
		}
		at, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return err
		}
		if i, ok := dayIndex[at.In(location).Format(time.DateOnly)]; ok {
			out.Days[i].AnalyticsStats.add(stats)
			out.Summary.add(stats)
		}
	}
	return rows.Err()
}

func addSources(ctx context.Context, tx *sql.Tx, args []any, out *Analytics) error {
	rows, err := tx.QueryContext(ctx, sourceQuery, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var source AnalyticsSource
		if err := rows.Scan(append([]any{&source.ID, &source.Name}, statFields(&source.AnalyticsStats)...)...); err != nil {
			return err
		}
		out.Sources = append(out.Sources, source)
	}
	return rows.Err()
}

func addErrors(ctx context.Context, tx *sql.Tx, args []any, out *Analytics) error {
	rows, err := tx.QueryContext(ctx, errorQuery, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var failure AnalyticsError
		if err := rows.Scan(&failure.Code, &failure.Count); err != nil {
			return err
		}
		out.Errors = append(out.Errors, failure)
	}
	return rows.Err()
}
