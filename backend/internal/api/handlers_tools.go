package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/codetool"
	"github.com/marcos14/contextforge/backend/internal/drivers"
	"github.com/marcos14/contextforge/backend/internal/store"
)

// ============== Tool groups ==============

type groupReq struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	HiddenByDefault *bool  `json:"hidden_by_default,omitempty"`
}

func (a *API) ListGroups(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	where := ""
	args := []any{}
	if p.Q != "" {
		where = ` WHERE (name ILIKE $1 OR description ILIKE $1)`
		args = append(args, "%"+p.Q+"%")
	}

	var total int
	if p.Paged {
		if err := a.Pool.QueryRow(r.Context(),
			`SELECT count(*) FROM tool_groups`+where, args...).Scan(&total); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	sql := `SELECT id, name, description, hidden_by_default, created_at
FROM tool_groups` + where + ` ORDER BY name`
	if p.Paged {
		sql += fmt.Sprintf(" LIMIT %d OFFSET %d", p.PageSize, p.Offset())
	}
	rows, err := a.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var name, desc string
		var hidden bool
		var created time.Time
		_ = rows.Scan(&id, &name, &desc, &hidden, &created)
		out = append(out, map[string]any{
			"id": id, "name": name, "description": desc,
			"hidden_by_default": hidden, "created_at": created,
		})
	}
	if p.Paged {
		writePage(w, out, total, p)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var in groupReq
	if err := decodeBody(r, &in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	hidden := true
	if in.HiddenByDefault != nil {
		hidden = *in.HiddenByDefault
	}
	id := uuid.New()
	_, err := a.Pool.Exec(r.Context(),
		`INSERT INTO tool_groups (id, name, description, hidden_by_default) VALUES ($1,$2,$3,$4)`,
		id, in.Name, in.Description, hidden)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *API) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in groupReq
	if err := decodeBody(r, &in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	hidden := true
	if in.HiddenByDefault != nil {
		hidden = *in.HiddenByDefault
	}
	ct, err := a.Pool.Exec(r.Context(),
		`UPDATE tool_groups SET name=$1, description=$2, hidden_by_default=$3 WHERE id=$4`,
		in.Name, in.Description, hidden, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ct.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "group not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (a *API) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	// Pre-check: tools table FK uses ON DELETE RESTRICT, so fail fast with a friendlier message.
	var count int
	if err := a.Pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM tools WHERE group_id=$1`, id).Scan(&count); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if count > 0 {
		writeErr(w, http.StatusConflict, fmt.Sprintf("group has %d tool(s); move or delete them first", count))
		return
	}
	ct, err := a.Pool.Exec(r.Context(), `DELETE FROM tool_groups WHERE id=$1`, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ct.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "group not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============== Tools ==============

var slugRe = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

type toolReq struct {
	Kind          string          `json:"kind"` // "query" (default) | "code"
	GroupID       uuid.UUID       `json:"group_id"`
	ConnectionID  uuid.UUID       `json:"connection_id"`
	Slug          string          `json:"slug"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	QueryText     string          `json:"query_text"`
	ParamsSchema  json.RawMessage `json:"params_schema"`
	OutputSchema  json.RawMessage `json:"output_schema,omitempty"`
	CodeRefs      json.RawMessage `json:"code_refs,omitempty"`
	ChatLog       json.RawMessage `json:"chat_log,omitempty"`
	LastTest      json.RawMessage `json:"last_test,omitempty"`
	RowLimit      int             `json:"row_limit"`
	TimeoutMS     int             `json:"timeout_ms"`
	CacheTTLSec   int             `json:"cache_ttl_sec"`
	CachePerToken bool            `json:"cache_per_token"`
	TestParams    map[string]any  `json:"test_params"`
	Activate      bool            `json:"activate"`
}

// kindOrDefault returns the requested tool kind, defaulting to "query".
func (in *toolReq) kindOrDefault() store.ToolKind {
	switch store.ToolKind(in.Kind) {
	case store.ToolKindCode:
		return store.ToolKindCode
	default:
		return store.ToolKindQuery
	}
}

// connIDForInsert returns the value to insert for connection_id: a uuid.UUID
// for query tools (required) or nil for code tools (allowed to be NULL).
func (in *toolReq) connIDForInsert() any {
	if in.kindOrDefault() == store.ToolKindCode {
		if in.ConnectionID == uuid.Nil {
			return nil
		}
	}
	return in.ConnectionID
}

// CreateTool validates, dry-runs (if Activate=true) and persists a new tool.
func (a *API) CreateTool(w http.ResponseWriter, r *http.Request) {
	var in toolReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if !slugRe.MatchString(in.Slug) {
		writeErr(w, http.StatusBadRequest, "slug must match [a-z0-9_]{1,64}")
		return
	}
	kind := in.kindOrDefault()
	if in.QueryText == "" || in.GroupID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "group_id and query_text are required")
		return
	}
	if kind == store.ToolKindQuery && in.ConnectionID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "connection_id is required for query tools")
		return
	}
	if kind == store.ToolKindCode {
		if err := codetool.Compile(in.QueryText); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "code does not compile: "+err.Error())
			return
		}
	}
	if in.RowLimit <= 0 {
		in.RowLimit = 1000
	}
	if in.TimeoutMS <= 0 {
		in.TimeoutMS = 15000
	}
	if len(in.ParamsSchema) == 0 {
		in.ParamsSchema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	normalized, err := validateAndNormalizeParamsSchema(in.ParamsSchema)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "invalid params_schema: "+err.Error())
		return
	}
	in.ParamsSchema = normalized
	if len(in.CodeRefs) == 0 {
		in.CodeRefs = json.RawMessage(`{"connection_ids":[],"tool_slugs":[]}`)
	}
	if len(in.ChatLog) == 0 {
		in.ChatLog = json.RawMessage(`[]`)
	} else {
		in.ChatLog = trimChatLog(in.ChatLog)
	}

	status := "draft"
	if in.Activate {
		// Dry-run before activating.
		if _, err := a.dryRunTool(r, in); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "dry-run failed: "+err.Error())
			return
		}
		status = "active"
	}

	uid, _, _ := currentUser(r.Context())
	id := uuid.New()
	_, err = a.Pool.Exec(r.Context(), `
INSERT INTO tools (id, kind, group_id, connection_id, slug, title, description,
                   query_text, params_schema, output_schema, row_limit, timeout_ms,
                   cache_ttl_sec, cache_per_token, status, created_by, code_refs,
                   chat_log, last_test)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		id, string(kind), in.GroupID, in.connIDForInsert(), in.Slug, in.Title, in.Description,
		in.QueryText, in.ParamsSchema, nullableJSON(in.OutputSchema),
		in.RowLimit, in.TimeoutMS, in.CacheTTLSec, in.CachePerToken, status, uid, in.CodeRefs,
		in.ChatLog, nullableJSON(in.LastTest))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.saveVersion(r, id, 1, in)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": status})
}

func (a *API) ListTools(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	where := ""
	args := []any{}
	if p.Q != "" {
		where = ` WHERE (slug ILIKE $1 OR title ILIKE $1 OR description ILIKE $1)`
		args = append(args, "%"+p.Q+"%")
	}

	var total int
	if p.Paged {
		if err := a.Pool.QueryRow(r.Context(),
			`SELECT count(*) FROM tools`+where, args...).Scan(&total); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	sql := `
SELECT id, kind, group_id,
       COALESCE(connection_id, '00000000-0000-0000-0000-000000000000'::uuid),
       slug, title, description, status, version,
       row_limit, timeout_ms, cache_ttl_sec, cache_per_token, created_at, updated_at
FROM   tools` + where + `
ORDER  BY created_at DESC`
	if p.Paged {
		sql += fmt.Sprintf(" LIMIT %d OFFSET %d", p.PageSize, p.Offset())
	}
	rows, err := a.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var t store.Tool
		_ = rows.Scan(&t.ID, &t.Kind, &t.GroupID, &t.ConnectionID, &t.Slug, &t.Title, &t.Description,
			&t.Status, &t.Version, &t.RowLimit, &t.TimeoutMS, &t.CacheTTLSec,
			&t.CachePerToken, &t.CreatedAt, &t.UpdatedAt)
		row := map[string]any{
			"id": t.ID, "kind": t.Kind, "group_id": t.GroupID,
			"slug": t.Slug, "title": t.Title, "description": t.Description,
			"status": t.Status, "version": t.Version,
			"row_limit": t.RowLimit, "timeout_ms": t.TimeoutMS,
			"cache_ttl_sec": t.CacheTTLSec, "cache_per_token": t.CachePerToken,
			"created_at": t.CreatedAt, "updated_at": t.UpdatedAt,
		}
		if t.ConnectionID != uuid.Nil {
			row["connection_id"] = t.ConnectionID
		} else {
			row["connection_id"] = nil
		}
		out = append(out, row)
	}
	if p.Paged {
		writePage(w, out, total, p)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) GetTool(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var t store.Tool
	err = a.Pool.QueryRow(r.Context(), `
SELECT id, kind, group_id,
       COALESCE(connection_id, '00000000-0000-0000-0000-000000000000'::uuid),
       slug, title, description, query_text,
       params_schema, output_schema, row_limit, timeout_ms,
       cache_ttl_sec, cache_per_token, status, version, created_at, updated_at,
       code_refs, chat_log, last_test
FROM tools WHERE id=$1`, id).
		Scan(&t.ID, &t.Kind, &t.GroupID, &t.ConnectionID, &t.Slug, &t.Title, &t.Description, &t.QueryText,
			&t.ParamsSchema, &t.OutputSchema, &t.RowLimit, &t.TimeoutMS,
			&t.CacheTTLSec, &t.CachePerToken, &t.Status, &t.Version, &t.CreatedAt, &t.UpdatedAt,
			&t.CodeRefs, &t.ChatLog, &t.LastTest)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *API) PreviewTool(w http.ResponseWriter, r *http.Request) {
	var in toolReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.RowLimit <= 0 || in.RowLimit > 50 {
		in.RowLimit = 10
	}
	res, err := a.dryRunTool(r, in)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// RunTool executes a saved tool by id against user-supplied parameters and
// returns the actual result. Useful for manual smoke-testing from the UI.
func (a *API) RunTool(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		Params map[string]any `json:"params"`
	}
	_ = decodeBody(r, &body)

	var t store.Tool
	err = a.Pool.QueryRow(r.Context(), `
SELECT id, kind, group_id,
       COALESCE(connection_id, '00000000-0000-0000-0000-000000000000'::uuid),
       slug, title, description, query_text,
       params_schema, output_schema, row_limit, timeout_ms,
       cache_ttl_sec, cache_per_token, status, version, created_at, updated_at
FROM tools WHERE id=$1`, id).
		Scan(&t.ID, &t.Kind, &t.GroupID, &t.ConnectionID, &t.Slug, &t.Title, &t.Description, &t.QueryText,
			&t.ParamsSchema, &t.OutputSchema, &t.RowLimit, &t.TimeoutMS,
			&t.CacheTTLSec, &t.CachePerToken, &t.Status, &t.Version, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	in := toolReq{
		Kind:         string(t.Kind),
		ConnectionID: t.ConnectionID,
		Slug:         t.Slug,
		QueryText:    t.QueryText,
		ParamsSchema: t.ParamsSchema,
		RowLimit:     t.RowLimit,
		TimeoutMS:    t.TimeoutMS,
		TestParams:   body.Params,
	}
	if in.RowLimit <= 0 || in.RowLimit > 1000 {
		in.RowLimit = 1000
	}
	res, err := a.dryRunTool(r, in)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// UpdateTool replaces all editable fields of an existing tool, bumps its
// version and saves a snapshot in tool_versions. If Activate=true a dry-run
// is performed first and the status is set to "active".
func (a *API) UpdateTool(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in toolReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if !slugRe.MatchString(in.Slug) {
		writeErr(w, http.StatusBadRequest, "slug must match [a-z0-9_]{1,64}")
		return
	}
	kind := in.kindOrDefault()
	if in.QueryText == "" || in.GroupID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "group_id and query_text are required")
		return
	}
	if kind == store.ToolKindQuery && in.ConnectionID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "connection_id is required for query tools")
		return
	}
	if kind == store.ToolKindCode {
		if err := codetool.Compile(in.QueryText); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "code does not compile: "+err.Error())
			return
		}
	}
	if in.RowLimit <= 0 {
		in.RowLimit = 1000
	}
	if in.TimeoutMS <= 0 {
		in.TimeoutMS = 15000
	}
	if len(in.ParamsSchema) == 0 {
		in.ParamsSchema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	normalized, err := validateAndNormalizeParamsSchema(in.ParamsSchema)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "invalid params_schema: "+err.Error())
		return
	}
	in.ParamsSchema = normalized
	if len(in.CodeRefs) == 0 {
		in.CodeRefs = json.RawMessage(`{"connection_ids":[],"tool_slugs":[]}`)
	}
	if len(in.ChatLog) == 0 {
		in.ChatLog = json.RawMessage(`[]`)
	} else {
		in.ChatLog = trimChatLog(in.ChatLog)
	}

	var currentVersion int
	var currentStatus string
	if err := a.Pool.QueryRow(r.Context(),
		`SELECT version, status FROM tools WHERE id=$1`, id).
		Scan(&currentVersion, &currentStatus); err != nil {
		writeErr(w, http.StatusNotFound, "tool not found")
		return
	}

	status := currentStatus
	if in.Activate {
		if _, err := a.dryRunTool(r, in); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "dry-run failed: "+err.Error())
			return
		}
		status = "active"
	}

	newVersion := currentVersion + 1
	_, err = a.Pool.Exec(r.Context(), `
UPDATE tools SET
  kind            = $2,
  group_id        = $3,
  connection_id   = $4,
  slug            = $5,
  title           = $6,
  description     = $7,
  query_text      = $8,
  params_schema   = $9,
  output_schema   = $10,
  row_limit       = $11,
  timeout_ms      = $12,
  cache_ttl_sec   = $13,
  cache_per_token = $14,
  status          = $15,
  version         = $16,
  code_refs       = $17,
  chat_log        = $18,
  last_test       = $19,
  updated_at      = now()
WHERE id = $1`,
		id, string(kind), in.GroupID, in.connIDForInsert(), in.Slug, in.Title, in.Description,
		in.QueryText, in.ParamsSchema, nullableJSON(in.OutputSchema),
		in.RowLimit, in.TimeoutMS, in.CacheTTLSec, in.CachePerToken, status, newVersion, in.CodeRefs,
		in.ChatLog, nullableJSON(in.LastTest))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.saveVersion(r, id, newVersion, in)
	// Invalidate any cached executions for this tool.
	_, _ = a.Cache.InvalidateTool(r.Context(), id.String())
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status, "version": newVersion})
}

// DeleteTool removes a tool. token_grants referencing the tool are removed by
// caller responsibility (cascade not enabled on grants.target_id).
func (a *API) DeleteTool(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	// Remove any individual grants targeting this tool first.
	if _, err := a.Pool.Exec(r.Context(),
		`DELETE FROM token_grants WHERE scope='tool' AND target_id=$1`, id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ct, err := a.Pool.Exec(r.Context(), `DELETE FROM tools WHERE id=$1`, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ct.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "tool not found")
		return
	}
	_, _ = a.Cache.InvalidateTool(r.Context(), id.String())
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) saveVersion(r *http.Request, toolID uuid.UUID, version int, req toolReq) {
	payload, _ := json.Marshal(req)
	uid, _, _ := currentUser(r.Context())
	_, _ = a.Pool.Exec(r.Context(),
		`INSERT INTO tool_versions (tool_id, version, payload, created_by) VALUES ($1,$2,$3,$4)`,
		toolID, version, payload, uid)
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// maxChatLogMessages caps the persisted assistant transcript so the row
// cannot grow without bound. Older messages are discarded.
const maxChatLogMessages = 40

// trimChatLog parses the chat array and keeps only the last maxChatLogMessages
// entries. Returns the original payload if parsing fails or the array is
// already within bounds (callers may receive a non-array if frontend is
// buggy — be defensive and just round-trip in that case).
func trimChatLog(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`[]`)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return raw
	}
	if len(arr) <= maxChatLogMessages {
		return raw
	}
	arr = arr[len(arr)-maxChatLogMessages:]
	out, err := json.Marshal(arr)
	if err != nil {
		return raw
	}
	return out
}

// dryRunTool executes a tool against either its underlying driver (kind=query)
// or the JS code runtime (kind=code), bounded to a preview row-limit. Used
// by PreviewTool and by the Activate path of Create/Update.
func (a *API) dryRunTool(r *http.Request, in toolReq) (*drivers.ExecResult, error) {
	rl := in.RowLimit
	if rl <= 0 || rl > 50 {
		rl = 10
	}
	timeout := time.Duration(in.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	if in.kindOrDefault() == store.ToolKindCode {
		if a.CodeRuntime == nil {
			return nil, fmt.Errorf("code runtime not configured")
		}
		return a.CodeRuntime.Run(r.Context(), codetool.Invocation{
			Slug:     in.Slug,
			Code:     in.QueryText,
			Params:   buildDryRunParams(in),
			RowLimit: rl,
			Timeout:  timeout,
			Preview:  true,
		})
	}

	drv, err := a.openDriver(r, in.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("open driver: %w", err)
	}
	defer drv.Close()
	return drv.Execute(r.Context(), drivers.ExecRequest{
		Query:    in.QueryText,
		Params:   buildDryRunParams(in),
		RowLimit: rl,
		Timeout:  timeout,
	})
}

var dryRunParamRe = regexp.MustCompile(`(^|[^:]):([a-zA-Z_][a-zA-Z0-9_]*)`)

// buildDryRunParams produces a parameter map suitable for a dry-run execution:
//   - starts from any user-provided TestParams,
//   - fills missing placeholders referenced in the query with a default value
//     inferred from params_schema (so optional parameters do not fail with
//     "missing parameter"),
//   - guarantees _token_id is present.
func buildDryRunParams(in toolReq) map[string]any {
	out := map[string]any{}
	for k, v := range in.TestParams {
		out[k] = v
	}

	types := schemaPropTypes(in.ParamsSchema)
	isCode := in.kindOrDefault() == store.ToolKindCode

	// For code tools the snippet accesses params by attribute name
	// (e.g. params.a) instead of SQL placeholders, so the regex-based
	// detection below would leave them empty. Pre-fill every declared
	// schema property with a sensible default so the dry-run actually
	// exercises the code instead of failing on "undefined" inputs.
	if isCode {
		for name, t := range types {
			if _, ok := out[name]; !ok {
				out[name] = defaultValueForType(t)
			}
		}
	}

	for _, m := range dryRunParamRe.FindAllStringSubmatch(in.QueryText, -1) {
		name := m[2]
		if name == "" || name == "_token_id" {
			continue
		}
		if _, ok := out[name]; ok {
			continue
		}
		if isCode {
			out[name] = defaultValueForType(types[name])
		} else {
			// SQL tools: pass NULL for any param the user did not supply.
			// The prompt requires every :param to be guarded with a
			// NULL-tolerant pattern (e.g. "(:name IS NULL OR col = :name)"),
			// so the dry-run exercises the query without forcing the driver
			// to coerce an empty string into a DATE/NUMERIC column — that
			// coercion is what raises Firebird's SQL error -303 ("conversion
			// error from string \"\"").
			out[name] = nil
		}
	}
	if _, ok := out["_token_id"]; !ok {
		out["_token_id"] = "00000000-0000-0000-0000-000000000000"
	}
	return out
}

func schemaPropTypes(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	var s struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return out
	}
	for name, p := range s.Properties {
		switch t := p.Type.(type) {
		case string:
			out[name] = t
		case []any:
			for _, v := range t {
				if vs, ok := v.(string); ok && vs != "null" {
					out[name] = vs
					break
				}
			}
		}
	}
	return out
}

func defaultValueForType(t string) any {
	switch t {
	case "integer":
		return 0
	case "number":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	case "string":
		return ""
	}
	return nil
}

func ensureTestParams(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	if _, ok := p["_token_id"]; !ok {
		p["_token_id"] = "00000000-0000-0000-0000-000000000000"
	}
	return p
}

// ============== params_schema validation ==============
//
// MCP clients (and the underlying ajv-based validators) reject tools whose
// JSON Schema is malformed. The LLM occasionally invents non-standard types
// like "int[]", "array<integer>" or emits "array" without "items". Rather
// than letting the broken schema propagate to /mcp (where the client only
// shows a cryptic error long after the tool was saved), we validate and
// normalize on save and surface a precise 422 error.

var jsonSchemaAllowedTypes = map[string]struct{}{
	"string":  {},
	"number":  {},
	"integer": {},
	"boolean": {},
	"object":  {},
	"array":   {},
	"null":    {},
}

// arrayShorthandRe matches "int[]", "string[]", "array<integer>", "array of string", etc.
var arrayShorthandRe = regexp.MustCompile(`(?i)^\s*(?:array\s*(?:<|of)\s*([a-z]+)\s*>?|([a-z]+)\s*\[\s*\]?)\s*$`)

// typeAliases maps common LLM mistakes to canonical JSON Schema types.
var typeAliases = map[string]string{
	"int":       "integer",
	"long":      "integer",
	"float":     "number",
	"double":    "number",
	"decimal":   "number",
	"bool":      "boolean",
	"str":       "string",
	"text":      "string",
	"date":      "string",
	"time":      "string",
	"timestamp": "string",
	"datetime":  "string",
	"uuid":      "string",
}

// validateAndNormalizeParamsSchema parses the user-supplied JSON Schema,
// fixes common LLM-introduced shorthands (e.g. "int[]" -> array of integer,
// "int" -> "integer") and rejects anything still not valid for MCP.
func validateAndNormalizeParamsSchema(raw json.RawMessage) (json.RawMessage, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}
	// Force top-level type=object so MCP listing works.
	root["type"] = "object"
	props, _ := root["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		root["properties"] = props
	}
	for name, v := range props {
		p, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("property %q is not an object", name)
		}
		if err := normalizeProp(name, p); err != nil {
			return nil, err
		}
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeProp(name string, p map[string]any) error {
	t, _ := p["type"].(string)
	if t == "" {
		// No type given — let MCP infer from other keywords.
		return nil
	}
	// Strip whitespace and try shorthand rewrites first.
	tn := strings.TrimSpace(t)
	if m := arrayShorthandRe.FindStringSubmatch(tn); m != nil {
		inner := strings.ToLower(m[1])
		if inner == "" {
			inner = strings.ToLower(m[2])
		}
		inner = canonType(inner)
		if _, ok := jsonSchemaAllowedTypes[inner]; !ok {
			return fmt.Errorf("property %q: unknown array item type %q", name, inner)
		}
		p["type"] = "array"
		if _, has := p["items"]; !has {
			p["items"] = map[string]any{"type": inner}
		}
	} else {
		canon := canonType(strings.ToLower(tn))
		if _, ok := jsonSchemaAllowedTypes[canon]; !ok {
			return fmt.Errorf("property %q: type %q is not a valid JSON Schema type (allowed: string, number, integer, boolean, object, array, null)", name, t)
		}
		p["type"] = canon
	}
	// If type is array, ensure items is present and well-formed.
	if p["type"] == "array" {
		items, ok := p["items"].(map[string]any)
		if !ok {
			// Default to string items rather than failing — most LLM-omitted
			// "array" params are arrays of identifiers.
			items = map[string]any{"type": "string"}
			p["items"] = items
		}
		if err := normalizeProp(name+".items", items); err != nil {
			return err
		}
	}
	return nil
}

func canonType(t string) string {
	if alias, ok := typeAliases[t]; ok {
		return alias
	}
	return t
}
