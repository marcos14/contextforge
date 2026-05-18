package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/auth"
)

// ============== Tokens ==============

type tokenReq struct {
	Name            string   `json:"name"`
	RateLimitPerMin int      `json:"rate_limit_per_min"`
	IPAllowlist     []string `json:"ip_allowlist"`
}

func (a *API) CreateToken(w http.ResponseWriter, r *http.Request) {
	var in tokenReq
	if err := decodeBody(r, &in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	if in.RateLimitPerMin <= 0 {
		in.RateLimitPerMin = 120
	}
	if in.IPAllowlist == nil {
		in.IPAllowlist = []string{}
	}
	token, prefix, hash, err := auth.GenerateClientToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	uid, _, _ := currentUser(r.Context())
	id := uuid.New()
	_, err = a.Pool.Exec(r.Context(), `
INSERT INTO tokens (id, name, prefix, hashed_secret, owner_user_id,
                    rate_limit_per_min, ip_allowlist)
VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		id, in.Name, prefix, hash, uid, in.RateLimitPerMin, in.IPAllowlist)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Plaintext token returned exactly once.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":     id,
		"prefix": prefix,
		"token":  token,
		"name":   in.Name,
	})
}

func (a *API) ListTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Pool.Query(r.Context(), `
SELECT id, name, prefix, rate_limit_per_min, ip_allowlist, revoked_at, last_used_at, created_at
FROM tokens ORDER BY created_at DESC`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		m := map[string]any{}
		var id uuid.UUID
		var name, prefix string
		var rate int
		var allow []string
		var revoked, lastUsed, created any
		_ = rows.Scan(&id, &name, &prefix, &rate, &allow, &revoked, &lastUsed, &created)
		m["id"], m["name"], m["prefix"] = id, name, prefix
		m["rate_limit_per_min"], m["ip_allowlist"] = rate, allow
		m["revoked_at"], m["last_used_at"], m["created_at"] = revoked, lastUsed, created
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) RevokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	_, err = a.Pool.Exec(r.Context(), `UPDATE tokens SET revoked_at = now() WHERE id=$1`, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) ReactivateToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	_, err = a.Pool.Exec(r.Context(), `UPDATE tokens SET revoked_at = NULL WHERE id=$1`, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============== Grants ==============

type grantReq struct {
	Scope                   string `json:"scope"` // "tool" | "group"
	TargetID                string `json:"target_id"`
	RateLimitPerMinOverride *int   `json:"rate_limit_per_min_override,omitempty"`
}

func (a *API) AddGrant(w http.ResponseWriter, r *http.Request) {
	tokenID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in grantReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.Scope != "tool" && in.Scope != "group" {
		writeErr(w, http.StatusBadRequest, "invalid scope")
		return
	}
	target, err := uuid.Parse(in.TargetID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad target")
		return
	}
	_, err = a.Pool.Exec(r.Context(), `
INSERT INTO token_grants (token_id, scope, target_id, rate_limit_per_min_override)
VALUES ($1,$2,$3,$4)
ON CONFLICT (token_id, scope, target_id)
DO UPDATE SET rate_limit_per_min_override = EXCLUDED.rate_limit_per_min_override`,
		tokenID, in.Scope, target, in.RateLimitPerMinOverride)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (a *API) DeleteGrant(w http.ResponseWriter, r *http.Request) {
	tokenID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	grantID, err := uuid.Parse(chi.URLParam(r, "grantId"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad grant id")
		return
	}
	_, err = a.Pool.Exec(r.Context(),
		`DELETE FROM token_grants WHERE id=$1 AND token_id=$2`, grantID, tokenID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) ListGrants(w http.ResponseWriter, r *http.Request) {
	tokenID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	rows, err := a.Pool.Query(r.Context(),
		`SELECT id, scope, target_id, rate_limit_per_min_override
		   FROM token_grants WHERE token_id=$1 ORDER BY scope, target_id`, tokenID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var gid, tgt uuid.UUID
		var scope string
		var override *int
		_ = rows.Scan(&gid, &scope, &tgt, &override)
		out = append(out, map[string]any{
			"id": gid, "scope": scope, "target_id": tgt,
			"rate_limit_per_min_override": override,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ============== Cache invalidation ==============

func (a *API) InvalidateToolCache(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	n, err := a.Cache.InvalidateTool(r.Context(), id.String())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// ============== Dashboard / audit ==============

// RecentExecutions returns tool_executions in reverse chronological order.
//
// Legacy mode (no `page`/`page_size`): returns the latest 200 rows as a plain
// JSON array. Paginated mode (any of `page`, `page_size`): returns
// `{items,total,page,page_size}` and accepts optional filters:
//   - `q`     — substring match on `tool_slug` or `error_message`
//   - `status` — one of `success` (status='ok') or `error` (status<>'ok')
func (a *API) RecentExecutions(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))

	if !p.Paged && statusFilter == "" {
		rows, err := a.Pool.Query(r.Context(), `
SELECT occurred_at, token_id, tool_slug, duration_ms, rows_returned,
       cache_hit, status, error_message, client_ip
FROM tool_executions
ORDER BY occurred_at DESC
LIMIT 200`)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer rows.Close()
		out := scanExecutions(rows)
		writeJSON(w, http.StatusOK, out)
		return
	}

	// Build filtered query.
	conds := []string{}
	args := []any{}
	if p.Q != "" {
		args = append(args, "%"+p.Q+"%")
		conds = append(conds, "(tool_slug ILIKE $"+itoa(len(args))+
			" OR COALESCE(error_message,'') ILIKE $"+itoa(len(args))+")")
	}
	switch statusFilter {
	case "success", "ok":
		conds = append(conds, "status = 'ok'")
	case "error", "err", "fail", "failed":
		conds = append(conds, "status <> 'ok'")
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := a.Pool.QueryRow(r.Context(),
		"SELECT COUNT(*) FROM tool_executions"+where, args...).Scan(&total); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	args = append(args, p.PageSize, p.Offset())
	q := `SELECT occurred_at, token_id, tool_slug, duration_ms, rows_returned,
       cache_hit, status, error_message, client_ip
FROM tool_executions` + where +
		" ORDER BY occurred_at DESC LIMIT $" + itoa(len(args)-1) +
		" OFFSET $" + itoa(len(args))
	rows, err := a.Pool.Query(r.Context(), q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	writePage(w, scanExecutions(rows), total, p)
}

// scanExecutions reads tool_executions rows produced by the standard SELECT
// used by RecentExecutions.
func scanExecutions(rows interface {
	Next() bool
	Scan(...any) error
}) []map[string]any {
	out := []map[string]any{}
	for rows.Next() {
		m := map[string]any{}
		var occurred any
		var tokenID *uuid.UUID
		var slug, status string
		var dur, rowsRet *int
		var hit bool
		var errMsg *string
		var ip *string
		_ = rows.Scan(&occurred, &tokenID, &slug, &dur, &rowsRet, &hit, &status, &errMsg, &ip)
		m["occurred_at"], m["token_id"], m["tool_slug"] = occurred, tokenID, slug
		m["duration_ms"], m["rows_returned"], m["cache_hit"] = dur, rowsRet, hit
		m["status"], m["error_message"], m["client_ip"] = status, errMsg, ip
		out = append(out, m)
	}
	return out
}
