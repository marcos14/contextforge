package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcos14/contextforge/backend/internal/drivers"
)

// GenerateQueryInput is the input to GenerateQuery.
type GenerateQueryInput struct {
	ConnectionKind string
	Tables         []drivers.Table
	UserPrompt     string
}

// GenerateQueryOutput is the model's structured response.
type GenerateQueryOutput struct {
	Query  string         `json:"query"`
	Params []ParamSpec    `json:"params"`
	Notes  string         `json:"notes,omitempty"`
	Raw    map[string]any `json:"-"`
}

type ParamSpec struct {
	Name        string `json:"name"`
	Type        string `json:"type"`                 // string|number|integer|boolean|array
	ItemsType   string `json:"items_type,omitempty"` // required when Type=="array" (string|number|integer|boolean)
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

const genQuerySystem = `You are an expert data engineer. The user will describe what they want
extracted from a database. You must produce a single SELECT-only query
(no INSERT/UPDATE/DELETE/DDL) using named parameters with the syntax :name.

Rules:
- Only SELECT or WITH ... SELECT statements.
- Produce EXACTLY ONE statement. Do not include a trailing semicolon. Never
  emit two statements separated by ";".
- Use placeholders :param_name for any user-supplied input.
- Do not concatenate untrusted strings.
- Do not invent tables/columns; use only those provided.
- Prefer explicit column lists over SELECT *.
- For Mongo connections, output a JSON spec instead of SQL with this shape:
  {"collection":"...","find":{"filter":{...},"limit":N}}
  or {"collection":"...","aggregate":[{...}, ...]}.
  Replace user input with the literal string "$param:<name>".

Respond strictly as JSON with this schema:
{"query": "<string>",
 "params": [{"name":"...", "type":"string|number|integer|boolean|array", "items_type":"string|number|integer|boolean (only when type==array)", "description":"...", "required":true|false}],
 "notes": "<optional explanation>"}`

// dialectGuidance returns a per-driver system note appended to the prompt so
// the LLM produces SQL the driver actually accepts. Returns "" for drivers
// without special rules.
func dialectGuidance(kind string) string {
	switch kind {
	case "firebird":
		// Firebird has no schemas. The UI surfaces a synthetic "PUBLIC"
		// schema in mention navigation, but it must not appear in SQL —
		// "SELECT ... FROM PUBLIC.CONTAS" raises "Procedure unknown
		// PUBLIC.CONTAS". Tables are referenced by name only.
		return "Dialect: Firebird. Firebird has no schemas — reference every table by its NAME only (e.g. \"SELECT ... FROM CONTAS\"), never qualified with a schema. Identifier quoting uses double quotes when needed."
	}
	return ""
}

// formatTableForPrompt renders one table reference for the LLM. For drivers
// without real schemas we omit the synthetic schema prefix so the model does
// not paste it into SQL.
func formatTableForPrompt(t drivers.Table, kind string) string {
	if kind == "firebird" || t.Schema == "" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// GenerateQuery asks the model to produce a query and params spec.
func (c *Client) GenerateQuery(ctx context.Context, in GenerateQueryInput) (*GenerateQueryOutput, error) {
	var sb strings.Builder
	sb.WriteString("Connection kind: ")
	sb.WriteString(in.ConnectionKind)
	sb.WriteString("\n\nAvailable tables:\n")
	for _, t := range in.Tables {
		fmt.Fprintf(&sb, "- %s\n", formatTableForPrompt(t, in.ConnectionKind))
		for _, col := range t.Columns {
			fmt.Fprintf(&sb, "    %s %s%s\n", col.Name, col.Type, nullableTag(col.Nullable))
		}
	}
	sb.WriteString("\nUser request:\n")
	sb.WriteString(in.UserPrompt)

	msgs := []Message{{Role: "system", Content: genQuerySystem}}
	if hint := dialectGuidance(in.ConnectionKind); hint != "" {
		msgs = append(msgs, Message{Role: "system", Content: hint})
	}
	msgs = append(msgs, Message{Role: "user", Content: sb.String()})
	raw, err := c.Chat(ctx, msgs, true)
	if err != nil {
		return nil, err
	}
	out := &GenerateQueryOutput{}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return nil, fmt.Errorf("llm output is not valid JSON: %w (raw=%s)", err, truncate(raw, 256))
	}
	if out.Query == "" {
		return nil, fmt.Errorf("llm returned empty query")
	}
	return out, nil
}

func nullableTag(n bool) string {
	if n {
		return " NULL"
	}
	return " NOT NULL"
}

// DocumentToolInput feeds the documenter prompt.
type DocumentToolInput struct {
	Query        string
	Params       []ParamSpec
	SampleResult *drivers.ExecResult
}

// DocumentToolOutput contains the generated documentation.
type DocumentToolOutput struct {
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	ParamsSchema json.RawMessage `json:"params_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

const docSystem = `You document a database-backed tool that will be exposed via the Model
Context Protocol. Given the query, declared parameters, and a sample result,
produce:

- a concise human-friendly title (max 80 chars)
- a description (1-3 sentences) explaining when an AI agent should call the
  tool and what it returns;
- a JSON Schema (params_schema) for the parameters, suitable for MCP tool
  definitions ("type":"object","properties":{...},"required":[...]);
- a JSON Schema (output_schema) describing the shape of the result.

Respond strictly as JSON with this shape:
{"title":"...","description":"...","params_schema":{...},"output_schema":{...}}`

// DocumentTool returns generated documentation for a tool.
func (c *Client) DocumentTool(ctx context.Context, in DocumentToolInput) (*DocumentToolOutput, error) {
	var sb strings.Builder
	sb.WriteString("Query:\n")
	sb.WriteString(in.Query)
	sb.WriteString("\n\nDeclared parameters:\n")
	for _, p := range in.Params {
		fmt.Fprintf(&sb, "- %s (%s, required=%v): %s\n", p.Name, p.Type, p.Required, p.Description)
	}
	if in.SampleResult != nil {
		b, _ := json.MarshalIndent(in.SampleResult, "", "  ")
		sb.WriteString("\nSample result (truncated):\n")
		sb.WriteString(truncate(string(b), 4000))
	}
	raw, err := c.Chat(ctx, []Message{
		{Role: "system", Content: docSystem},
		{Role: "user", Content: sb.String()},
	}, true)
	if err != nil {
		return nil, err
	}
	out := &DocumentToolOutput{}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return nil, fmt.Errorf("llm output is not valid JSON: %w (raw=%s)", err, truncate(raw, 256))
	}
	return out, nil
}

// ChatToolInput is the input to ChatTool: a conversation with the LLM where
// the user iteratively describes the tool they want to build.
type ChatToolInput struct {
	ConnectionKind string
	Tables         []drivers.Table
	History        []Message
	Current        *CurrentTool
}

// CurrentTool describes the tool currently loaded in the editor form. When
// provided, the assistant should treat it as the working draft and help the
// user iterate on it (refactor the query, tweak params, improve docs) instead
// of designing a brand-new tool from scratch.
type CurrentTool struct {
	Editing      bool            `json:"editing"`
	Slug         string          `json:"slug,omitempty"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	QueryText    string          `json:"query_text,omitempty"`
	ParamsSchema json.RawMessage `json:"params_schema,omitempty"`
}

// ChatToolOutput is a structured assistant turn. Reply is always present and
// shown to the user. When the assistant is confident enough to propose a
// query, Query/Params are populated so the UI can offer "Use this query".
type ChatToolOutput struct {
	Reply       string      `json:"reply"`
	Query       string      `json:"query,omitempty"`
	Params      []ParamSpec `json:"params,omitempty"`
	Slug        string      `json:"slug,omitempty"`
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	Notes       string      `json:"notes,omitempty"`
}

const chatToolSystem = `You help an engineer design a database-backed tool that will be exposed
via the Model Context Protocol. You converse with the user in their language
(default: Portuguese) and, when you have enough information, propose a single
SELECT-only query (no INSERT/UPDATE/DELETE/DDL) using named parameters :name.

Behaviour:
- Ask short, targeted follow-up questions when the request is ambiguous.
- Only use tables/columns provided in the system context. If something is
  missing, ask the user instead of guessing.
- The "query" field MUST contain EXACTLY ONE statement. Do not include a
  trailing semicolon. Never emit two statements separated by ";".
- Prefer explicit column lists over SELECT *.
- EVERY :param MUST be guarded with a NULL-tolerant pattern such as
  "(:name IS NULL OR column = :name)" (or, for ranges, ":name IS NULL OR
  date_col >= :name"). The activation dry-run passes NULL for every :param
  the user does not supply — without the NULL guard the driver will try to
  coerce NULL into the column type and the dry-run will fail (e.g. Firebird
  raises "conversion error from string"). This applies to BOTH required
  AND optional params: required only means the MCP caller must supply a
  value at runtime, not that the dry-run will.
- For parameters that accept multiple values (e.g. lists of ids), declare
  them with type="array" and a matching items_type (string/integer/...).
  Use the dialect's array operator (Postgres: "column = ANY(:name)";
  MySQL/Oracle/MSSQL: prefer a single id parameter, or document that the
  caller must pass a comma-separated string the query splits).
- For Mongo connections, output a JSON spec instead of SQL:
  {"collection":"...","find":{"filter":{...},"limit":N}}
  or {"collection":"...","aggregate":[{...}, ...]}.
  Replace user input with the literal string "$param:<name>".
- Never invent data. If unsure, say so.

Always respond strictly as JSON with this schema:
{
  "reply": "<message shown in the chat, plain prose, in the user's language>",
  "query": "<optional: the proposed query, only when you are proposing one>",
  "params": [{"name":"...","type":"string|number|integer|boolean|array","items_type":"string|number|integer|boolean (only when type==array)","description":"...","required":true|false}],
  "slug": "<optional: short snake_case identifier matching [a-z0-9_]{1,64}>",
  "title": "<optional: short human title, max 80 chars>",
  "description": "<optional: 1-3 sentence description for an AI agent>",
  "notes": "<optional short explanation of trade-offs>"
}

Rules for the JSON:
- "reply" is REQUIRED and must always be non-empty.
- Omit "query", "params", "slug", "title", "description" while you are still
  gathering information.
- Whenever you include "query", ALSO include matching "slug", "title" and
  "description" so the user can save the tool with one click.`

// ChatTool runs one turn of the tool-design conversation.
func (c *Client) ChatTool(ctx context.Context, in ChatToolInput) (*ChatToolOutput, error) {
	var sb strings.Builder
	sb.WriteString("Connection kind: ")
	sb.WriteString(in.ConnectionKind)
	sb.WriteString("\n\nAvailable tables:\n")
	if len(in.Tables) == 0 {
		sb.WriteString("(none provided yet — ask the user to run \"Carregar schema\" if you need column details)\n")
	}
	for _, t := range in.Tables {
		fmt.Fprintf(&sb, "- %s\n", formatTableForPrompt(t, in.ConnectionKind))
		for _, col := range t.Columns {
			fmt.Fprintf(&sb, "    %s %s%s\n", col.Name, col.Type, nullableTag(col.Nullable))
		}
	}

	msgs := []Message{
		{Role: "system", Content: chatToolSystem},
		{Role: "system", Content: sb.String()},
	}
	if hint := dialectGuidance(in.ConnectionKind); hint != "" {
		msgs = append(msgs, Message{Role: "system", Content: hint})
	}
	if in.Current != nil && (in.Current.QueryText != "" || in.Current.Slug != "" || in.Current.Title != "" || in.Current.Description != "" || len(in.Current.ParamsSchema) > 0) {
		var cb strings.Builder
		if in.Current.Editing {
			cb.WriteString("The user is EDITING an existing tool. Treat the fields below as the current draft and help them iterate on it (refactor the query, tweak params, improve title/description). Only propose a brand-new query if the user explicitly asks for one.\n\n")
		} else {
			cb.WriteString("The user already has the following draft in the form. Use it as the starting point when proposing changes.\n\n")
		}
		if in.Current.Slug != "" {
			fmt.Fprintf(&cb, "Slug: %s\n", in.Current.Slug)
		}
		if in.Current.Title != "" {
			fmt.Fprintf(&cb, "Title: %s\n", in.Current.Title)
		}
		if in.Current.Description != "" {
			fmt.Fprintf(&cb, "Description: %s\n", in.Current.Description)
		}
		if in.Current.QueryText != "" {
			cb.WriteString("Current query:\n")
			cb.WriteString(in.Current.QueryText)
			cb.WriteString("\n")
		}
		if len(in.Current.ParamsSchema) > 0 && string(in.Current.ParamsSchema) != "null" {
			cb.WriteString("Current params_schema (JSON Schema):\n")
			cb.Write(in.Current.ParamsSchema)
			cb.WriteString("\n")
		}
		msgs = append(msgs, Message{Role: "system", Content: cb.String()})
	}
	msgs = append(msgs, in.History...)

	raw, err := c.Chat(ctx, msgs, true)
	if err != nil {
		return nil, err
	}
	out := &ChatToolOutput{}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return nil, fmt.Errorf("llm output is not valid JSON: %w (raw=%s)", err, truncate(raw, 256))
	}
	if out.Reply == "" {
		return nil, fmt.Errorf("llm returned empty reply")
	}
	return out, nil
}

// ============== Query Studio (SQL-for-general-use assistant) ==============

// QueryStudioInput is the input to QueryStudioChat: a multi-turn conversation
// where the user describes the query they need for use in ANY external system
// (reports, ETL, dashboards, app code) — NOT an MCP tool. The assistant's job
// is to understand the need and propose performant, idiomatic SQL for the
// connection's dialect.
//
// Only fields available in Fase 1 are declared here. Rich schema data (FK
// relations + existing indexes) is added by Fase 2a via a RichIntrospector;
// this struct will gain Relations/Indexes fields then.
type QueryStudioInput struct {
	ConnectionKind string          // "pg" | "mysql" | "mssql" | "oracle" | "firebird" | ...
	Tables         []drivers.Table // schema from Introspect
	History        []Message       // conversation so far (user/assistant turns)
	CurrentQuery   string          // query currently in the editor, if any
	ExplainResult  string          // execution plan of the last validation (refine loop)
}

// QueryStudioOutput is a structured assistant turn. Reply is always present and
// shown in the chat. When the assistant proposes a query, Query/Explanation and
// the performance fields are populated so the UI can render them.
type QueryStudioOutput struct {
	Reply            string   `json:"reply"`
	Query            string   `json:"query,omitempty"`             // SQL for the dialect (SELECT-only)
	Explanation      string   `json:"explanation,omitempty"`       // what the query does, in the user's language
	SuggestedIndexes []string `json:"suggested_indexes,omitempty"` // DDL of indexes that would speed the query (TEXT, never executed)
	PerformanceNotes []string `json:"performance_notes,omitempty"` // anti-patterns avoided, warnings
	Assumptions      []string `json:"assumptions,omitempty"`       // assumptions made about the schema
	Raw              string   `json:"-"`                           // raw model response (debug)
}

const queryStudioSystem = `You are a senior database engineer specialised in query PERFORMANCE. You
converse with the user in their language (default: Portuguese) to understand
which query they need, then propose performant, idiomatic SQL for the target
dialect. The query is for use in ANY external system (reports, ETL, dashboards,
application code) — it is NOT an MCP tool, so do NOT ask for or produce a slug,
params_schema, output_schema, or tool metadata.

Behaviour:
- Ask short, targeted follow-up questions when the request is ambiguous.
- The user speaks in business terms ("faturamento por cliente no último
  trimestre"); translate that into SQL. Do not require the user to know SQL.
- Only use tables/columns provided in the system context. If something is
  missing, ask the user instead of guessing; record any guess in "assumptions".
- The "query" field MUST contain EXACTLY ONE statement. Do not include a
  trailing semicolon. Never emit two statements separated by ";".
- SELECT-only. Never propose INSERT/UPDATE/DELETE/DDL as the query. Suggested
  indexes are TEXT for the user to apply manually — they are placed in
  "suggested_indexes", never in "query".

Performance rules to APPLY and EXPLAIN (surface the relevant ones in
"performance_notes"):
- Avoid SELECT *; list only the columns needed.
- Prefer JOINs over correlated subqueries when possible.
- Avoid functions over indexed columns in WHERE (breaks index usage); rewrite
  to keep the column bare (e.g. "col >= :from AND col < :to" instead of
  "DATE(col) = :day").
- Prefer EXISTS over "IN (large subquery)" when it fits.
- Warn about a missing WHERE / full table scans.
- Prefer keyset pagination over large OFFSET when applicable.
- Suggest indexes (including composite and covering) coherent with the dialect,
  ordered by selectivity; do not suggest an index that would obviously already
  exist as a primary key.
- Consider column types and cardinality from the introspection.

When "Last execution plan (EXPLAIN)" is provided in the context, USE it to
rewrite/optimise the query (e.g. an index is not used, a sequential scan is
costly) and explain in "reply"/"performance_notes" what changed and why.

Always respond strictly as JSON with this schema:
{
  "reply": "<message shown in the chat, plain prose, in the user's language>",
  "query": "<optional: the proposed SQL, only when you are proposing one>",
  "explanation": "<optional: what the query does, in the user's language>",
  "suggested_indexes": ["<optional DDL, e.g. CREATE INDEX ...>"],
  "performance_notes": ["<optional: anti-patterns avoided / warnings>"],
  "assumptions": ["<optional: assumptions made about the schema>"]
}

Rules for the JSON:
- "reply" is REQUIRED and must always be non-empty.
- Omit "query" and the performance fields while you are still gathering
  information.
- Whenever you include "query", ALSO include "explanation".`

// queryStudioDialectGuidance returns per-dialect guidance (EXPLAIN syntax,
// row-limiting, index creation) appended to the prompt so the model produces
// SQL and index DDL the target database actually accepts. Returns "" for
// dialects without special rules.
func queryStudioDialectGuidance(kind string) string {
	switch kind {
	case "pg", "postgres", "postgresql":
		return "Dialect: PostgreSQL. Row limiting: LIMIT n [OFFSET m]. Plan inspection: EXPLAIN (FORMAT JSON) <q>; real: EXPLAIN (ANALYZE, BUFFERS) <q>. Indexes: CREATE INDEX idx_name ON table (col1, col2); partial/expression indexes are available. Prefer keyset pagination over high OFFSET."
	case "mysql", "mariadb":
		return "Dialect: MySQL. Row limiting: LIMIT n [OFFSET m]. Plan inspection: EXPLAIN FORMAT=JSON <q>; real (8.0.18+): EXPLAIN ANALYZE <q>. Indexes: CREATE INDEX idx_name ON table (col1, col2). Watch out for implicit type conversions that disable index usage."
	case "mssql", "sqlserver":
		return "Dialect: SQL Server. Row limiting: TOP n, or OFFSET m ROWS FETCH NEXT n ROWS ONLY (requires ORDER BY). Plan inspection: SET SHOWPLAN_XML ON (estimated) / actual execution plan. Indexes: CREATE [NONCLUSTERED] INDEX idx_name ON table (col1, col2) INCLUDE (...); use INCLUDE for covering indexes."
	case "oracle":
		return "Dialect: Oracle. Row limiting: FETCH FIRST n ROWS ONLY (12c+) or WHERE ROWNUM <= n. Plan inspection: EXPLAIN PLAN FOR <q> + SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY). Indexes: CREATE INDEX idx_name ON table (col1, col2). Avoid functions on indexed columns unless a function-based index exists."
	case "firebird":
		return "Dialect: Firebird. Firebird has no schemas — reference every table by its NAME only, never qualified with a schema. Row limiting: FIRST n [SKIP m] or ROWS n. Plan inspection: SET PLAN ON (textual plan). Indexes: CREATE INDEX idx_name ON table (col1, col2). Identifier quoting uses double quotes when needed."
	}
	return ""
}

// QueryStudioChat runs one turn of the Query Studio conversation: it feeds the
// dialect-aware performance system prompt, the schema, any current query and
// the last EXPLAIN plan, then returns the structured proposal.
func (c *Client) QueryStudioChat(ctx context.Context, in QueryStudioInput) (*QueryStudioOutput, error) {
	var sb strings.Builder
	sb.WriteString("Connection kind: ")
	sb.WriteString(in.ConnectionKind)
	sb.WriteString("\n\nAvailable tables:\n")
	if len(in.Tables) == 0 {
		sb.WriteString("(none provided yet — ask the user to load the schema if you need column details)\n")
	}
	for _, t := range in.Tables {
		fmt.Fprintf(&sb, "- %s\n", formatTableForPrompt(t, in.ConnectionKind))
		for _, col := range t.Columns {
			fmt.Fprintf(&sb, "    %s %s%s\n", col.Name, col.Type, nullableTag(col.Nullable))
		}
	}
	if in.CurrentQuery != "" {
		sb.WriteString("\nCurrent query in the editor:\n")
		sb.WriteString(in.CurrentQuery)
		sb.WriteString("\n")
	}
	if in.ExplainResult != "" {
		sb.WriteString("\nLast execution plan (EXPLAIN) — use it to optimise:\n")
		sb.WriteString(truncate(in.ExplainResult, 8000))
		sb.WriteString("\n")
	}

	msgs := []Message{
		{Role: "system", Content: queryStudioSystem},
		{Role: "system", Content: sb.String()},
	}
	if hint := queryStudioDialectGuidance(in.ConnectionKind); hint != "" {
		msgs = append(msgs, Message{Role: "system", Content: hint})
	}
	msgs = append(msgs, in.History...)

	raw, err := c.Chat(ctx, msgs, true)
	if err != nil {
		return nil, err
	}
	return parseQueryStudioOutput(raw)
}

// parseQueryStudioOutput decodes and validates the model's JSON response.
// Extracted from QueryStudioChat so it can be unit-tested without a live LLM.
func parseQueryStudioOutput(raw string) (*QueryStudioOutput, error) {
	out := &QueryStudioOutput{}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return nil, fmt.Errorf("llm output is not valid JSON: %w (raw=%s)", err, truncate(raw, 256))
	}
	if out.Reply == "" {
		return nil, fmt.Errorf("llm returned empty reply")
	}
	out.Raw = raw
	return out, nil
}

// ============== Code-tool generation ==============

// AvailableConnection describes one connection the JS runtime can target
// via db("<name>", ...). Used by GenerateCode to ground the LLM.
type AvailableConnection struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// AvailableTool describes one already-active tool the JS runtime can compose
// via tools.call("<slug>", params). Used by GenerateCode.
type AvailableTool struct {
	Slug        string `json:"slug"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// GenerateCodeInput is the input to GenerateCode.
type GenerateCodeInput struct {
	UserPrompt  string
	Connections []AvailableConnection
	Tools       []AvailableTool
	History     []Message
	Current     *CurrentTool
}

// GenerateCodeOutput is the LLM's structured response for a JS code-tool.
type GenerateCodeOutput struct {
	Reply       string      `json:"reply,omitempty"`
	Code        string      `json:"code"`
	Params      []ParamSpec `json:"params"`
	Slug        string      `json:"slug,omitempty"`
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description,omitempty"`
	Notes       string      `json:"notes,omitempty"`
}

const genCodeSystem = `You are an expert backend engineer helping the user design a tool exposed
via the Model Context Protocol. The tool body is a JavaScript snippet (ES5.1
+ most ES6) that runs in a sandboxed embedded VM with these globals:

  params              // object with validated input params (declared below)
  context.token_id    // current MCP token UUID (string)
  db(connName, sql, params)        // run a SELECT against an existing
                                   // connection by name; returns
                                   // { rows: [...], columns: [...], count }
                                   // Use :name placeholders for SQL params.
  tools.call(slug, params)         // invoke another active tool; returns
                                   // the same { rows, columns, count } shape
  fetch(url, opts)                 // synchronous HTTP. opts: { method,
                                   // headers, query, body }. Returns
                                   // { status, headers, body, json() }.
  log(...args), console.log(...)

Conversation rules:
- Converse in the user's language (default: Portuguese).
- Read the chat history. The user iterates: each new message refines the
  previous draft. Update the snippet to reflect the LATEST request, not the
  first one. Do NOT keep features the user explicitly dropped.
- If the request is ambiguous, ask a short follow-up question in "reply" and
  return an empty "code". Otherwise produce a complete, working snippet.

Code rules:
- Output MUST be plain JavaScript with a top-level "return" statement
  (the snippet is wrapped in an IIFE by the runtime).
- DO NOT use async/await, Promises, require, import, or Node APIs.
  The runtime is single-threaded and synchronous.
- Implement the actual logic the user asked for. Never return a placeholder
  like { value: 'ready' } unless the user explicitly asks for a stub.
- Prefer returning { rows: [...], columns: [...] }. Scalars become a single
  { value: ... } row automatically.
- Validate inputs early; throw on invalid combinations.
- IMPORTANT: when checking for "required" parameters, use
  "params.x === undefined" or "params.x == null". Do NOT write
  "if (!params.x)" because the dry-run passes zero defaults for numeric
  params (0), empty strings for string params, and false for booleans -
  all of which are falsy but valid inputs, and your validation would
  reject them.
- For enum-style string params, accept the dry-run empty default by
  short-circuiting at the very top (e.g. "if (params.operation === '')
  return { rows: [] };") OR by listing the enum in the params_schema
  description so the user knows which values are accepted.
- Use only the connections and tools provided in the system context.
- Use :name placeholders for SQL params (never concatenate user input).
- CRITICAL: inside SQL passed to db(connName, sql, params), reference tables
  as "schema.table" (or just "table"), NEVER as "connName.schema.table".
  The connection name is already selected by the first argument to db();
  prefixing it inside the SQL causes "cross-database references" errors on
  PostgreSQL/SQL Server. Example: db("agronavis_dev", "SELECT ... FROM hub.geom_city WHERE ...", {...})
  - NOT "FROM agronavis_dev.hub.geom_city".
- The user may mention tables in chat using "@conn.schema.table" syntax for
  disambiguation; strip the connection prefix when writing the SQL.

Also propose a slug (a-z0-9_, max 64), a short Title and a one-line
Description for the tool. Reuse the user's terminology.

Respond strictly as JSON:
{
  "reply": "<short message to the user, same language as the user>",
  "code": "<javascript snippet, with a top-level return; empty if you only have a follow-up question>",
  "slug": "<lower_snake_case>",
  "title": "<short title>",
  "description": "<one-line description>",
  "params": [{"name":"...","type":"string|number|integer|boolean|array",
              "items_type":"string|number|integer|boolean (when type==array)",
              "description":"...","required":true|false}],
  "notes": "<optional short explanation of the snippet>"
}`

// GenerateCode asks the model to produce a JS code-tool body + params spec.
func (c *Client) GenerateCode(ctx context.Context, in GenerateCodeInput) (*GenerateCodeOutput, error) {
	var sb strings.Builder
	sb.WriteString("Available connections (use via db(<name>, ...)):\n")
	if len(in.Connections) == 0 {
		sb.WriteString("(none configured)\n")
	}
	for _, conn := range in.Connections {
		fmt.Fprintf(&sb, "- %s (%s)", conn.Name, conn.Type)
		if conn.Type == "firebird" {
			sb.WriteString(" — Firebird has no schemas; reference tables by NAME only (e.g. \"SELECT ... FROM CONTAS\"), never as \"PUBLIC.CONTAS\"")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\nAvailable tools (use via tools.call(<slug>, params)):\n")
	if len(in.Tools) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, t := range in.Tools {
		fmt.Fprintf(&sb, "- %s - %s\n", t.Slug, t.Title)
		if t.Description != "" {
			fmt.Fprintf(&sb, "    %s\n", t.Description)
		}
	}

	msgs := []Message{
		{Role: "system", Content: genCodeSystem},
		{Role: "system", Content: sb.String()},
	}
	if in.Current != nil && (in.Current.QueryText != "" || in.Current.Slug != "" || in.Current.Title != "" || in.Current.Description != "") {
		var cb strings.Builder
		if in.Current.Editing {
			cb.WriteString("The user is EDITING an existing tool. Treat the fields below as the current draft and help them iterate on it.\n\n")
		} else {
			cb.WriteString("The user already has the following draft in the form. Use it as the starting point.\n\n")
		}
		if in.Current.Slug != "" {
			fmt.Fprintf(&cb, "Slug: %s\n", in.Current.Slug)
		}
		if in.Current.Title != "" {
			fmt.Fprintf(&cb, "Title: %s\n", in.Current.Title)
		}
		if in.Current.Description != "" {
			fmt.Fprintf(&cb, "Description: %s\n", in.Current.Description)
		}
		if in.Current.QueryText != "" {
			cb.WriteString("Current code:\n")
			cb.WriteString(in.Current.QueryText)
			cb.WriteString("\n")
		}
		msgs = append(msgs, Message{Role: "system", Content: cb.String()})
	}
	if len(in.History) > 0 {
		msgs = append(msgs, in.History...)
	} else if in.UserPrompt != "" {
		msgs = append(msgs, Message{Role: "user", Content: in.UserPrompt})
	}

	raw, err := c.Chat(ctx, msgs, true)
	if err != nil {
		return nil, err
	}
	out := &GenerateCodeOutput{}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return nil, fmt.Errorf("llm output is not valid JSON: %w (raw=%s)", err, truncate(raw, 256))
	}
	if out.Code == "" && out.Reply == "" {
		return nil, fmt.Errorf("llm returned empty response")
	}
	return out, nil
}
