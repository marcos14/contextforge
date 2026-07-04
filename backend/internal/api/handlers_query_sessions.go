package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/store"
)

// ============== Query Studio — session history (Fase 2b) ==============
//
// Persists a lightweight, per-user history of query-building sessions so a user
// can re-open a previous session (title + SQL + chat + last plan). Sessions are
// scoped to their creator: a user only lists/reads/updates/deletes their own.
//
// Endpoints (all under RequireAuth("admin","editor")):
//   GET    /query-studio/sessions        list (legacy array OR paged {items,...})
//   POST   /query-studio/sessions        create, or update when {id} is supplied
//   GET    /query-studio/sessions/{id}   full session (title + SQL + chat + plan)
//   DELETE /query-studio/sessions/{id}   remove
//
// Design notes:
//   - Row data never lives here: chat_log holds the assistant transcript and
//     last_explain the (row-free) execution plan; neither carries preview rows.
//   - Sessions are OUTSIDE the encrypted backup scope (like tokens/users) — see
//     PLANO §9 and the Registro de Andamento entry for Fase 2b.

type querySessionReq struct {
	ID           uuid.UUID       `json:"id,omitempty"`
	ConnectionID uuid.UUID       `json:"connection_id"`
	Title        string          `json:"title"`
	QueryText    string          `json:"query_text,omitempty"`
	ChatLog      json.RawMessage `json:"chat_log,omitempty"`
	LastExplain  json.RawMessage `json:"last_explain,omitempty"`
}

// emptyJSONArray is the default for the NOT NULL chat_log column when the client
// omits the transcript.
var emptyJSONArray = json.RawMessage("[]")

// ListQuerySessions returns the current user's saved sessions. It preserves the
// project's dual-mode convention: without pagination query params it returns a
// flat array; with page/page_size/q it returns {items,total,page,page_size}.
func (a *API) ListQuerySessions(w http.ResponseWriter, r *http.Request) {
	uid, _, _ := currentUser(r.Context())
	p := parseListParams(r)

	where := ` WHERE created_by = $1`
	args := []any{uid}
	if p.Q != "" {
		where += ` AND title ILIKE $2`
		args = append(args, "%"+p.Q+"%")
	}

	var total int
	if p.Paged {
		if err := a.Pool.QueryRow(r.Context(),
			`SELECT count(*) FROM query_sessions`+where, args...).Scan(&total); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	sql := `
SELECT id, connection_id, title, query_text, created_at, updated_at
FROM   query_sessions` + where + `
ORDER  BY updated_at DESC`
	if p.Paged {
		sql += " LIMIT " + strconv.Itoa(p.PageSize) + " OFFSET " + strconv.Itoa(p.Offset())
	}
	rows, err := a.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var s store.QuerySession
		if err := rows.Scan(&s.ID, &s.ConnectionID, &s.Title, &s.QueryText,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, map[string]any{
			"id":            s.ID,
			"connection_id": s.ConnectionID,
			"title":         s.Title,
			"query_text":    s.QueryText,
			"created_at":    s.CreatedAt,
			"updated_at":    s.UpdatedAt,
		})
	}
	if p.Paged {
		writePage(w, out, total, p)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SaveQuerySession creates a session, or updates an existing one when the body
// carries an {id}. Updates are scoped to the current user (a user cannot touch
// another user's session). Returns the persisted row.
func (a *API) SaveQuerySession(w http.ResponseWriter, r *http.Request) {
	var in querySessionReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		writeErr(w, http.StatusBadRequest, "title required")
		return
	}
	if in.ConnectionID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "connection_id is required")
		return
	}
	uid, _, _ := currentUser(r.Context())
	var creator *uuid.UUID
	if uid != uuid.Nil {
		creator = &uid
	}

	chatLog := in.ChatLog
	if len(chatLog) == 0 {
		chatLog = emptyJSONArray
	}

	const returning = ` RETURNING id, connection_id, title, query_text, chat_log, last_explain, created_by, created_at, updated_at`
	var s store.QuerySession
	if in.ID != uuid.Nil {
		// Update path — only the owner may update.
		err := a.Pool.QueryRow(r.Context(), `
UPDATE query_sessions
SET    connection_id=$1, title=$2, query_text=$3, chat_log=$4, last_explain=$5, updated_at=now()
WHERE  id=$6 AND created_by=$7`+returning,
			in.ConnectionID, in.Title, in.QueryText, chatLog, nullableJSON(in.LastExplain),
			in.ID, uid).
			Scan(&s.ID, &s.ConnectionID, &s.Title, &s.QueryText, &s.ChatLog, &s.LastExplain,
				&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
		if err != nil {
			writeErr(w, http.StatusNotFound, "session not found")
			return
		}
		writeJSON(w, http.StatusOK, s)
		return
	}

	err := a.Pool.QueryRow(r.Context(), `
INSERT INTO query_sessions (connection_id, title, query_text, chat_log, last_explain, created_by)
VALUES ($1,$2,$3,$4,$5,$6)`+returning,
		in.ConnectionID, in.Title, in.QueryText, chatLog, nullableJSON(in.LastExplain), creator).
		Scan(&s.ID, &s.ConnectionID, &s.Title, &s.QueryText, &s.ChatLog, &s.LastExplain,
			&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s)
}

// GetQuerySession returns a full session (including chat_log and last_explain),
// scoped to the current user.
func (a *API) GetQuerySession(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	uid, _, _ := currentUser(r.Context())
	var s store.QuerySession
	err = a.Pool.QueryRow(r.Context(), `
SELECT id, connection_id, title, query_text, chat_log, last_explain, created_by, created_at, updated_at
FROM   query_sessions
WHERE  id=$1 AND created_by=$2`, id, uid).
		Scan(&s.ID, &s.ConnectionID, &s.Title, &s.QueryText, &s.ChatLog, &s.LastExplain,
			&s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// DeleteQuerySession removes one of the current user's sessions.
func (a *API) DeleteQuerySession(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	uid, _, _ := currentUser(r.Context())
	ct, err := a.Pool.Exec(r.Context(),
		`DELETE FROM query_sessions WHERE id=$1 AND created_by=$2`, id, uid)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ct.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
