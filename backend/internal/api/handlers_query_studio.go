package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/drivers"
	"github.com/marcos14/contextforge/backend/internal/llm"
)

// ============== Query Studio ==============
//
// The Query Studio module helps a user shape a performant SQL query for use in
// ANY external system (reports, ETL, dashboards, application code) — it does
// NOT produce an MCP tool. It exposes three endpoints, all under
// RequireAuth("admin","editor"):
//
//   POST /query-studio/chat     multi-turn LLM assistant (schema + history +
//                               current query + last EXPLAIN plan) → structured
//                               proposal (SQL + explanation + suggested indexes
//                               + performance notes).
//   POST /query-studio/explain  runs EXPLAIN (optionally ANALYZE) of the query
//                               and returns the plan. SELECT-only.
//   POST /query-studio/preview  runs the query with a server-forced LIMIT and a
//                               short timeout, returning a small sample of rows.
//
// Safety invariants (see PLANO §8):
//   - explain/preview validate the query with drivers.EnforceSelectOnly BEFORE
//     any database access, rejecting SHOW/EXPLAIN/DDL/DML.
//   - preview imposes its own row limit regardless of any LIMIT the LLM emitted.
//   - EXPLAIN ANALYZE executes the query for real: it requires an explicit
//     analyze:true flag, gets a short timeout, and is written to audit_logs.

const (
	// queryStudioPreviewMaxRows is the hard ceiling the server imposes on
	// preview results, independent of any LIMIT the LLM put in the query.
	queryStudioPreviewMaxRows = 100
	// queryStudioPreviewTimeout bounds a preview execution.
	queryStudioPreviewTimeout = 15 * time.Second
	// queryStudioExplainTimeout bounds a plain (non-executing) EXPLAIN.
	queryStudioExplainTimeout = 10 * time.Second
	// queryStudioAnalyzeTimeout bounds an EXPLAIN ANALYZE, which executes the
	// query for real. Kept short as a defence-in-depth measure.
	queryStudioAnalyzeTimeout = 30 * time.Second
	// queryStudioIntrospectTimeout bounds the best-effort rich introspection
	// (FKs + indexes) done during a chat turn.
	queryStudioIntrospectTimeout = 10 * time.Second
)

// clampPreviewLimit forces the preview row limit into the server-imposed range.
// A non-positive or over-limit request collapses to queryStudioPreviewMaxRows;
// the LLM's own LIMIT is never trusted for this bound.
func clampPreviewLimit(requested int) int {
	if requested <= 0 || requested > queryStudioPreviewMaxRows {
		return queryStudioPreviewMaxRows
	}
	return requested
}

type queryStudioChatReq struct {
	ConnectionID  uuid.UUID       `json:"connection_id"`
	Tables        []drivers.Table `json:"tables"`
	Messages      []llm.Message   `json:"messages"`
	CurrentQuery  string          `json:"current_query,omitempty"`
	ExplainResult string          `json:"explain_result,omitempty"`
}

// QueryStudioChat runs one turn of the assistant conversation. It only sends
// schema metadata and the (row-free) EXPLAIN plan to the LLM — never row data.
func (a *API) QueryStudioChat(w http.ResponseWriter, r *http.Request) {
	var in queryStudioChatReq
	if err := decodeBody(r, &in); err != nil || len(in.Messages) == 0 {
		writeErr(w, http.StatusBadRequest, "messages required")
		return
	}
	if a.LLM == nil {
		writeErr(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	kind := ""
	var relations []drivers.Relation
	var indexes []drivers.IndexInfo
	if in.ConnectionID != uuid.Nil {
		drv, err := a.openDriver(r, in.ConnectionID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "connection not found")
			return
		}
		defer drv.Close()
		kind = string(drv.Kind())
		// Best-effort rich introspection: feed FK relations and existing indexes
		// to the LLM so it proposes correct JOINs and avoids suggesting indexes
		// that already exist. Only schema metadata (no row data) is sent. If the
		// database is unreachable or lacks rich support, we still answer with
		// the schema-only context supplied by the client.
		ictx, cancel := context.WithTimeout(r.Context(), queryStudioIntrospectTimeout)
		if graph, gerr := drivers.IntrospectRich(ictx, drv); gerr == nil && graph != nil {
			relations = graph.Relations
			indexes = graph.Indexes
		}
		cancel()
	}
	out, err := a.LLM.QueryStudioChat(r.Context(), llm.QueryStudioInput{
		ConnectionKind: kind,
		Tables:         in.Tables,
		Relations:      relations,
		Indexes:        indexes,
		History:        in.Messages,
		CurrentQuery:   in.CurrentQuery,
		ExplainResult:  in.ExplainResult,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type queryStudioExplainReq struct {
	ConnectionID uuid.UUID `json:"connection_id"`
	Query        string    `json:"query"`
	Analyze      bool      `json:"analyze"`
}

// QueryStudioExplain runs EXPLAIN (optionally ANALYZE) of a SELECT query and
// returns the driver's plan. The query is validated SELECT-only before any DB
// access; ANALYZE requires an explicit flag and is audited.
func (a *API) QueryStudioExplain(w http.ResponseWriter, r *http.Request) {
	var in queryStudioExplainReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	clean, err := drivers.EnforceSelectOnly(in.Query)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.ConnectionID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "connection_id is required")
		return
	}
	drv, err := a.openDriver(r, in.ConnectionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer drv.Close()

	timeout := queryStudioExplainTimeout
	if in.Analyze {
		timeout = queryStudioAnalyzeTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	res, err := drivers.Explain(ctx, drv, clean, in.Analyze)
	if err != nil {
		if errors.Is(err, drivers.ErrUnsupported) {
			writeErr(w, http.StatusUnprocessableEntity, "explain not supported for this connection")
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	// EXPLAIN ANALYZE executes the query for real — record it.
	if in.Analyze {
		a.audit(r, "query_studio.explain_analyze", in.ConnectionID.String(),
			map[string]any{"dialect": res.Dialect})
	}
	writeJSON(w, http.StatusOK, res)
}

type queryStudioPreviewReq struct {
	ConnectionID uuid.UUID `json:"connection_id"`
	Query        string    `json:"query"`
	RowLimit     int       `json:"row_limit,omitempty"`
}

// QueryStudioPreview executes a SELECT query with a server-forced row limit and
// a short timeout, returning a small sample. The row data stays with the caller
// and is never forwarded to the LLM.
func (a *API) QueryStudioPreview(w http.ResponseWriter, r *http.Request) {
	var in queryStudioPreviewReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	clean, err := drivers.EnforceSelectOnly(in.Query)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.ConnectionID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "connection_id is required")
		return
	}
	limit := clampPreviewLimit(in.RowLimit)

	drv, err := a.openDriver(r, in.ConnectionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer drv.Close()

	ctx, cancel := context.WithTimeout(r.Context(), queryStudioPreviewTimeout)
	defer cancel()

	res, err := drv.Execute(ctx, drivers.ExecRequest{
		Query:    clean,
		RowLimit: limit,
		Timeout:  queryStudioPreviewTimeout,
	})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
