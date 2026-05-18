package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/auth"
)

// ============== User management (admin) ==============

type updateUserReq struct {
	Email    *string `json:"email,omitempty"`
	Role     *string `json:"role,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}

// GetUser returns a single user record (without the password hash).
func (a *API) GetUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var (
		email, role string
		disabled    bool
		created     time.Time
		updated     time.Time
	)
	err = a.Pool.QueryRow(r.Context(),
		`SELECT email, role, disabled, created_at, updated_at FROM users WHERE id=$1`, id).
		Scan(&email, &role, &disabled, &created, &updated)
	if err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         id,
		"email":      email,
		"role":       role,
		"disabled":   disabled,
		"created_at": created,
		"updated_at": updated,
	})
}

// UpdateUser changes email/role/disabled. Admins cannot demote or disable
// themselves to avoid lock-out.
func (a *API) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in updateUserReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.Role != nil && *in.Role != "admin" && *in.Role != "editor" && *in.Role != "viewer" {
		writeErr(w, http.StatusBadRequest, "invalid role")
		return
	}
	actor, _, _ := currentUser(r.Context())
	if actor == id {
		if in.Role != nil && *in.Role != "admin" {
			writeErr(w, http.StatusBadRequest, "cannot demote yourself")
			return
		}
		if in.Disabled != nil && *in.Disabled {
			writeErr(w, http.StatusBadRequest, "cannot disable yourself")
			return
		}
	}

	sets := []string{"updated_at = now()"}
	args := []any{}
	idx := 1
	if in.Email != nil {
		email := strings.ToLower(strings.TrimSpace(*in.Email))
		if email == "" {
			writeErr(w, http.StatusBadRequest, "email cannot be empty")
			return
		}
		sets = append(sets, "email = $"+itoa(idx))
		args = append(args, email)
		idx++
	}
	if in.Role != nil {
		sets = append(sets, "role = $"+itoa(idx))
		args = append(args, *in.Role)
		idx++
	}
	if in.Disabled != nil {
		sets = append(sets, "disabled = $"+itoa(idx))
		args = append(args, *in.Disabled)
		idx++
	}
	if len(args) == 0 {
		writeErr(w, http.StatusBadRequest, "nothing to update")
		return
	}
	args = append(args, id)
	q := "UPDATE users SET " + strings.Join(sets, ", ") + " WHERE id = $" + itoa(idx)
	if _, err := a.Pool.Exec(r.Context(), q, args...); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "user.update", "user:"+id.String(), map[string]any{
		"email": in.Email, "role": in.Role, "disabled": in.Disabled,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type setPasswordReq struct {
	Password string `json:"password"`
}

// SetUserPassword sets a new password for a user.
func (a *API) SetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in setPasswordReq
	if err := decodeBody(r, &in); err != nil || len(in.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ct, err := a.Pool.Exec(r.Context(),
		`UPDATE users SET password_hash=$1, updated_at=now() WHERE id=$2`, hash, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ct.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	a.audit(r, "user.set_password", "user:"+id.String(), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DeleteUser removes a user. Self-delete is forbidden and the last admin
// cannot be removed.
func (a *API) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	actor, _, _ := currentUser(r.Context())
	if actor == id {
		writeErr(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	// Ensure we don't delete the last active admin.
	var role string
	var disabled bool
	if err := a.Pool.QueryRow(r.Context(),
		`SELECT role, disabled FROM users WHERE id=$1`, id).Scan(&role, &disabled); err != nil {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	if role == "admin" && !disabled {
		var count int
		_ = a.Pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=false`).Scan(&count)
		if count <= 1 {
			writeErr(w, http.StatusBadRequest, "cannot delete the last active admin")
			return
		}
	}
	if _, err := a.Pool.Exec(r.Context(), `DELETE FROM users WHERE id=$1`, id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.audit(r, "user.delete", "user:"+id.String(), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ============== Audit log listing (admin) ==============

// ListAuditLogs returns recent audit log entries with optional pagination
// and free-text search over the `action` and `target` columns.
func (a *API) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	where := ""
	args := []any{}
	if p.Q != "" {
		where = " WHERE action ILIKE $1 OR target ILIKE $1"
		args = append(args, "%"+p.Q+"%")
	}
	var total int
	if err := a.Pool.QueryRow(r.Context(),
		"SELECT COUNT(*) FROM audit_logs"+where, args...).Scan(&total); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := "SELECT a.id, a.occurred_at, a.actor_id, u.email, a.action, a.target, a.details " +
		"FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_id" + where +
		" ORDER BY a.occurred_at DESC LIMIT $" + itoa(len(args)+1) +
		" OFFSET $" + itoa(len(args)+2)
	args = append(args, p.PageSize, p.Offset())
	rows, err := a.Pool.Query(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id         int64
			occurred   time.Time
			actor      *uuid.UUID
			actorEmail *string
			action     string
			target     *string
			detailsRaw []byte
		)
		if err := rows.Scan(&id, &occurred, &actor, &actorEmail, &action, &target, &detailsRaw); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		var details any
		if len(detailsRaw) > 0 {
			_ = json.Unmarshal(detailsRaw, &details)
		}
		out = append(out, map[string]any{
			"id":          id,
			"occurred_at": occurred,
			"actor_id":    actor,
			"actor_email": actorEmail,
			"action":      action,
			"target":      target,
			"details":     details,
		})
	}
	writePage(w, out, total, p)
}

// ============== Server settings / info (admin) ==============

// GetSettings exposes read-only server configuration and runtime status.
// Secrets are never returned — only presence flags and fingerprints.
func (a *API) GetSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}

	// Master key fingerprint (first 8 bytes of SHA-256 of the key).
	if a.Cipher != nil {
		out["master_key_fingerprint"] = a.Cipher.Fingerprint()
	}

	// LLM configuration (presence only — never the API key).
	llmInfo := map[string]any{"configured": a.LLM != nil}
	if a.LLM != nil {
		llmInfo["model"] = a.LLM.Model()
		llmInfo["base_url"] = a.LLM.BaseURL()
	}
	out["llm"] = llmInfo

	// Redis status.
	redisInfo := map[string]any{"configured": a.Cache != nil}
	if a.Cache != nil {
		if err := a.Cache.Ping(r.Context()); err != nil {
			redisInfo["status"] = "down"
			redisInfo["error"] = err.Error()
		} else {
			redisInfo["status"] = "up"
		}
	}
	out["redis"] = redisInfo

	// Database status.
	dbInfo := map[string]any{}
	if err := a.Pool.Ping(r.Context()); err != nil {
		dbInfo["status"] = "down"
		dbInfo["error"] = err.Error()
	} else {
		dbInfo["status"] = "up"
	}
	out["database"] = dbInfo

	// Counts overview.
	counts := map[string]int{}
	for _, c := range []struct {
		key string
		sql string
	}{
		{"users", "SELECT COUNT(*) FROM users"},
		{"connections", "SELECT COUNT(*) FROM connections"},
		{"groups", "SELECT COUNT(*) FROM tool_groups"},
		{"tools", "SELECT COUNT(*) FROM tools"},
		{"tokens", "SELECT COUNT(*) FROM tokens WHERE revoked_at IS NULL"},
	} {
		var n int
		_ = a.Pool.QueryRow(r.Context(), c.sql).Scan(&n)
		counts[c.key] = n
	}
	out["counts"] = counts

	writeJSON(w, http.StatusOK, out)
}

// itoa is a tiny helper to avoid importing strconv just for placeholder
// indices.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
