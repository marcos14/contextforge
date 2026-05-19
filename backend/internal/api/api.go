// Package api hosts the REST admin handlers for the management UI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marcos14/contextforge/backend/internal/auth"
	"github.com/marcos14/contextforge/backend/internal/cache"
	"github.com/marcos14/contextforge/backend/internal/codetool"
	"github.com/marcos14/contextforge/backend/internal/crypto"
	"github.com/marcos14/contextforge/backend/internal/executor"
	"github.com/marcos14/contextforge/backend/internal/llm"
	"github.com/marcos14/contextforge/backend/internal/registry"
)

// API bundles the dependencies handlers need.
type API struct {
	Pool            *pgxpool.Pool
	Signer          *auth.Signer
	Cipher          *crypto.Cipher
	Registry        *registry.Registry
	Executor        *executor.Executor
	CodeRuntime     *codetool.Runtime
	LLM             *llm.Client
	Cache           *cache.Cache
	DefaultRowLimit int
}

type ctxKey int

const (
	ctxKeyUserID ctxKey = iota
	ctxKeyRole
)

// RequireAuth verifies a JWT access token in the Authorization header.
func (a *API) RequireAuth(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if raw == "" {
				writeErr(w, http.StatusUnauthorized, "missing token")
				return
			}
			c, err := a.Signer.Verify(raw)
			if err != nil || c.Type != "access" {
				writeErr(w, http.StatusUnauthorized, "invalid token")
				return
			}
			if len(roles) > 0 && !containsStr(roles, c.Role) {
				writeErr(w, http.StatusForbidden, "insufficient role")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyUserID, c.UserID)
			ctx = context.WithValue(ctx, ctxKeyRole, c.Role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func decodeBody(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("empty body")
	}
	return json.NewDecoder(r.Body).Decode(v)
}

func currentUser(ctx context.Context) (uuid.UUID, string, bool) {
	uid, ok1 := ctx.Value(ctxKeyUserID).(uuid.UUID)
	role, ok2 := ctx.Value(ctxKeyRole).(string)
	return uid, role, ok1 && ok2
}

// listParams carries pagination + search hints parsed from a list endpoint.
// When Paged is false the handler should preserve legacy behavior (return a
// flat JSON array) so existing callers (selectors, MCP server) keep working.
type listParams struct {
	Page     int
	PageSize int
	Q        string
	Paged    bool
}

func parseListParams(r *http.Request) listParams {
	q := r.URL.Query()
	pageStr := q.Get("page")
	sizeStr := q.Get("page_size")
	p := listParams{
		Q:     strings.TrimSpace(q.Get("q")),
		Paged: pageStr != "" || sizeStr != "",
	}
	p.Page, _ = strconv.Atoi(pageStr)
	p.PageSize, _ = strconv.Atoi(sizeStr)
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 25
	}
	if p.PageSize > 200 {
		p.PageSize = 200
	}
	return p
}

func (p listParams) Offset() int { return (p.Page - 1) * p.PageSize }

func writePage(w http.ResponseWriter, items any, total int, p listParams) {
	if items == nil {
		items = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"total":     total,
		"page":      p.Page,
		"page_size": p.PageSize,
	})
}
