package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/drivers"
	"github.com/marcos14/contextforge/backend/internal/llm"
)

// ============== LLM ==============

type genQueryReq struct {
	ConnectionID uuid.UUID       `json:"connection_id"`
	Tables       []drivers.Table `json:"tables"`
	Prompt       string          `json:"prompt"`
}

func (a *API) GenerateQuery(w http.ResponseWriter, r *http.Request) {
	var in genQueryReq
	if err := decodeBody(r, &in); err != nil || in.Prompt == "" {
		writeErr(w, http.StatusBadRequest, "prompt required")
		return
	}
	if a.LLM == nil {
		writeErr(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	var kind string
	if err := a.Pool.QueryRow(r.Context(),
		`SELECT type FROM connections WHERE id=$1`, in.ConnectionID).Scan(&kind); err != nil {
		writeErr(w, http.StatusBadRequest, "connection not found")
		return
	}
	out, err := a.LLM.GenerateQuery(r.Context(), llm.GenerateQueryInput{
		ConnectionKind: kind,
		Tables:         in.Tables,
		UserPrompt:     in.Prompt,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type docReq struct {
	Query        string              `json:"query"`
	Params       []llm.ParamSpec     `json:"params"`
	SampleResult *drivers.ExecResult `json:"sample_result,omitempty"`
}

type chatToolReq struct {
	ConnectionID uuid.UUID        `json:"connection_id"`
	Tables       []drivers.Table  `json:"tables"`
	Messages     []llm.Message    `json:"messages"`
	Current      *llm.CurrentTool `json:"current,omitempty"`
}

func (a *API) ChatTool(w http.ResponseWriter, r *http.Request) {
	var in chatToolReq
	if err := decodeBody(r, &in); err != nil || len(in.Messages) == 0 {
		writeErr(w, http.StatusBadRequest, "messages required")
		return
	}
	if a.LLM == nil {
		writeErr(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	kind := ""
	if in.ConnectionID != uuid.Nil {
		if err := a.Pool.QueryRow(r.Context(),
			`SELECT type FROM connections WHERE id=$1`, in.ConnectionID).Scan(&kind); err != nil {
			writeErr(w, http.StatusBadRequest, "connection not found")
			return
		}
	}
	out, err := a.LLM.ChatTool(r.Context(), llm.ChatToolInput{
		ConnectionKind: kind,
		Tables:         in.Tables,
		History:        in.Messages,
		Current:        in.Current,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) DocumentTool(w http.ResponseWriter, r *http.Request) {
	var in docReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if a.LLM == nil {
		writeErr(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	out, err := a.LLM.DocumentTool(r.Context(), llm.DocumentToolInput{
		Query: in.Query, Params: in.Params, SampleResult: in.SampleResult,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type genCodeReq struct {
	Prompt   string           `json:"prompt"`
	Messages []llm.Message    `json:"messages,omitempty"`
	Current  *llm.CurrentTool `json:"current,omitempty"`
}

// GenerateCode produces a JavaScript snippet for a code-kind tool, grounded
// on the list of currently configured connections and active tools.
func (a *API) GenerateCode(w http.ResponseWriter, r *http.Request) {
	var in genCodeReq
	if err := decodeBody(r, &in); err != nil || (in.Prompt == "" && len(in.Messages) == 0) {
		writeErr(w, http.StatusBadRequest, "prompt or messages required")
		return
	}
	if a.LLM == nil {
		writeErr(w, http.StatusServiceUnavailable, "llm not configured")
		return
	}
	// Connections list.
	rows, err := a.Pool.Query(r.Context(), `SELECT name, type FROM connections ORDER BY name`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	var conns []llm.AvailableConnection
	for rows.Next() {
		var c llm.AvailableConnection
		if err := rows.Scan(&c.Name, &c.Type); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		conns = append(conns, c)
	}
	// Active tools list (only those callable via tools.call).
	snap := a.Registry.Get()
	tools := make([]llm.AvailableTool, 0, len(snap.Tools))
	for _, t := range snap.Tools {
		tools = append(tools, llm.AvailableTool{
			Slug: t.Slug, Title: t.Title, Description: t.Description,
		})
	}
	out, err := a.LLM.GenerateCode(r.Context(), llm.GenerateCodeInput{
		UserPrompt:  in.Prompt,
		Connections: conns,
		Tools:       tools,
		History:     in.Messages,
		Current:     in.Current,
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ============== Public registry view (for the UI) ==============

func (a *API) RegistrySnapshot(w http.ResponseWriter, r *http.Request) {
	snap := a.Registry.Get()
	tools := make([]map[string]any, 0, len(snap.Tools))
	for _, t := range snap.Tools {
		tools = append(tools, map[string]any{
			"id":           t.ID,
			"slug":         t.Slug,
			"title":        t.Title,
			"group_name":   t.GroupName,
			"group_hidden": t.GroupHidden,
			"version":      t.Version,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"built_at":    snap.BuiltAt,
		"tools_count": len(snap.Tools),
		"tokens":      len(snap.Tokens),
		"tools":       tools,
	})
}

// ============== Router ==============

// Mount registers all admin REST routes on r.
func (a *API) Mount(r chi.Router) {
	r.Post("/auth/login", a.Login)
	r.Post("/auth/refresh", a.Refresh)
	r.Get("/health", a.Health)

	r.Group(func(r chi.Router) {
		r.Use(a.RequireAuth())
		r.Get("/me", a.Me)
		r.Get("/registry", a.RegistrySnapshot)
		r.Get("/executions", a.RecentExecutions)
	})

	// Editor and admin can manage data.
	r.Group(func(r chi.Router) {
		r.Use(a.RequireAuth("admin", "editor"))

		r.Get("/connections", a.ListConnections)
		r.Post("/connections", a.CreateConnection)
		r.Get("/connections/{id}", a.GetConnection)
		r.Put("/connections/{id}", a.UpdateConnection)
		r.Post("/connections/{id}/test", a.TestConnection)
		r.Get("/connections/{id}/introspect", a.IntrospectConnection)
		r.Delete("/connections/{id}", a.DeleteConnection)

		r.Get("/groups", a.ListGroups)
		r.Post("/groups", a.CreateGroup)
		r.Put("/groups/{id}", a.UpdateGroup)
		r.Delete("/groups/{id}", a.DeleteGroup)

		r.Get("/tools", a.ListTools)
		r.Post("/tools", a.CreateTool)
		r.Get("/tools/{id}", a.GetTool)
		r.Put("/tools/{id}", a.UpdateTool)
		r.Delete("/tools/{id}", a.DeleteTool)
		r.Post("/tools/preview", a.PreviewTool)
		r.Post("/tools/{id}/run", a.RunTool)
		r.Post("/tools/{id}/cache/invalidate", a.InvalidateToolCache)

		r.Get("/tokens", a.ListTokens)
		r.Post("/tokens", a.CreateToken)
		r.Post("/tokens/{id}/revoke", a.RevokeToken)
		r.Post("/tokens/{id}/reactivate", a.ReactivateToken)
		r.Get("/tokens/{id}/grants", a.ListGrants)
		r.Post("/tokens/{id}/grants", a.AddGrant)
		r.Delete("/tokens/{id}/grants/{grantId}", a.DeleteGrant)

		r.Post("/llm/generate-query", a.GenerateQuery)
		r.Post("/llm/generate-code", a.GenerateCode)
		r.Post("/llm/document-tool", a.DocumentTool)
		r.Post("/llm/chat-tool", a.ChatTool)

		r.Post("/backup/export", a.BackupExport)
		r.Post("/backup/preview", a.BackupPreview)
		r.Post("/backup/restore", a.BackupRestore)
	})

	// Admin-only routes.
	r.Group(func(r chi.Router) {
		r.Use(a.RequireAuth("admin"))
		r.Get("/users", a.ListUsers)
		r.Post("/users", a.CreateUser)
		r.Get("/users/{id}", a.GetUser)
		r.Put("/users/{id}", a.UpdateUser)
		r.Post("/users/{id}/password", a.SetUserPassword)
		r.Delete("/users/{id}", a.DeleteUser)

		r.Get("/audit-logs", a.ListAuditLogs)
		r.Get("/settings", a.GetSettings)
	})
}

// Unused but kept so go imports the http package symbol everywhere.
var _ = http.StatusOK
