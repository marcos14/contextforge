package api

import (
	"net/http"
	"strings"
	"time"
)

// statsTopTools bounds the size of the by_tool block.
const statsTopTools = 10

// statsSummary holds the aggregated consumption figures over the window.
type statsSummary struct {
	Total        int     `json:"total"`
	Errors       int     `json:"errors"`
	CacheHits    int     `json:"cache_hits"`
	ErrorRate    float64 `json:"error_rate"`
	CacheHitRate float64 `json:"cache_hit_rate"`
}

// statsBucket is one point of the time series.
type statsBucket struct {
	Bucket    time.Time `json:"bucket"`
	Total     int       `json:"total"`
	Errors    int       `json:"errors"`
	CacheHits int       `json:"cache_hits"`
}

// statsByTool is one row of the top-tools breakdown.
type statsByTool struct {
	ToolSlug string `json:"tool_slug"`
	Total    int    `json:"total"`
	Errors   int    `json:"errors"`
}

// resolvePeriod maps a period selector to the window start and the
// date_trunc bucket used for the time series. An empty period defaults to
// "24h"; unknown values are rejected (ok=false). It is pure so it can be
// unit-tested without a database.
func resolvePeriod(period string, now time.Time) (since time.Time, bucket string, ok bool) {
	switch period {
	case "", "24h":
		return now.Add(-24 * time.Hour), "hour", true
	case "7d":
		return now.Add(-7 * 24 * time.Hour), "day", true
	case "30d":
		return now.Add(-30 * 24 * time.Hour), "day", true
	default:
		return time.Time{}, "", false
	}
}

// isErrorStatus classifies an execution status: anything other than "ok" is
// counted as an error, matching the SQL predicate `status <> 'ok'`.
func isErrorStatus(status string) bool {
	return status != "ok"
}

// rate returns part/total guarding against division by zero.
func rate(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}

// Stats exposes aggregated consumption metrics for a period, computed with
// GROUP BY in Postgres over tool_executions. Same visibility as
// /api/executions (any authenticated user).
//
// Query params:
//   - `period` — one of `24h` (default), `7d`, `30d`.
//
// Response: {period, summary, timeseries[], by_tool[]}.
func (a *API) Stats(w http.ResponseWriter, r *http.Request) {
	period := strings.TrimSpace(r.URL.Query().Get("period"))
	since, bucket, ok := resolvePeriod(period, time.Now())
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid period (use 24h, 7d or 30d)")
		return
	}
	if period == "" {
		period = "24h"
	}

	// Time series, one bucket per hour (24h) or per day (7d/30d).
	tsRows, err := a.Pool.Query(r.Context(), `
SELECT date_trunc($1, occurred_at) AS bucket,
       COUNT(*)                                    AS total,
       COUNT(*) FILTER (WHERE status <> 'ok')      AS errors,
       COUNT(*) FILTER (WHERE cache_hit)           AS cache_hits
FROM tool_executions
WHERE occurred_at >= $2
GROUP BY bucket
ORDER BY bucket`, bucket, since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tsRows.Close()

	timeseries := []statsBucket{}
	summary := statsSummary{}
	for tsRows.Next() {
		var b statsBucket
		if err := tsRows.Scan(&b.Bucket, &b.Total, &b.Errors, &b.CacheHits); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		timeseries = append(timeseries, b)
		summary.Total += b.Total
		summary.Errors += b.Errors
		summary.CacheHits += b.CacheHits
	}
	if err := tsRows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	summary.ErrorRate = rate(summary.Errors, summary.Total)
	summary.CacheHitRate = rate(summary.CacheHits, summary.Total)

	// Top tools by execution count within the window.
	toolRows, err := a.Pool.Query(r.Context(), `
SELECT tool_slug,
       COUNT(*)                               AS total,
       COUNT(*) FILTER (WHERE status <> 'ok') AS errors
FROM tool_executions
WHERE occurred_at >= $1
GROUP BY tool_slug
ORDER BY total DESC, tool_slug
LIMIT $2`, since, statsTopTools)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer toolRows.Close()

	byTool := []statsByTool{}
	for toolRows.Next() {
		var t statsByTool
		if err := toolRows.Scan(&t.ToolSlug, &t.Total, &t.Errors); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		byTool = append(byTool, t)
	}
	if err := toolRows.Err(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"period":     period,
		"summary":    summary,
		"timeseries": timeseries,
		"by_tool":    byTool,
	})
}
