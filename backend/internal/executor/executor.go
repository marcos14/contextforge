// Package executor orchestrates a single tool execution: rate limiting,
// cache lookup, driver dispatch, observability and cache write-through.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marcos14/contextforge/backend/internal/cache"
	"github.com/marcos14/contextforge/backend/internal/crypto"
	"github.com/marcos14/contextforge/backend/internal/drivers"
	"github.com/marcos14/contextforge/backend/internal/ratelimit"
	"github.com/marcos14/contextforge/backend/internal/registry"
)

// Result is the executor-level outcome.
type Result struct {
	Data     *drivers.ExecResult `json:"data"`
	CacheHit bool                `json:"cache_hit"`
	Duration time.Duration       `json:"duration"`
}

// ErrAccessDenied is returned when a token cannot access a tool.
var ErrAccessDenied = errors.New("access denied")

// ErrRateLimited is returned when a token exceeds its quota.
var ErrRateLimited = errors.New("rate limited")

// Executor is the central orchestrator.
type Executor struct {
	pool   *pgxpool.Pool
	reg    *registry.Registry
	cache  *cache.Cache
	rl     *ratelimit.Limiter
	cipher *crypto.Cipher
	ipMax  int

	// CodeRuntime executes kind=='code' tools. Optional; nil means code
	// tools fail with a clear error.
	code CodeRunner

	mu      sync.Mutex
	drivers map[uuid.UUID]drivers.Driver // cached by connection id
}

// CodeRunner is the minimal contract the executor needs from the JS code-tool
// runtime. The real implementation lives in internal/codetool; using a
// function type here avoids an import cycle (codetool needs to call back into
// the executor for tools.call() and db()). Wire it in main with an adapter
// closure that translates CodeInvocation -> codetool.Invocation.
type CodeRunner func(ctx context.Context, inv CodeInvocation) (*drivers.ExecResult, error)

// CodeInvocation mirrors codetool.Invocation but lives here so the executor
// package does not import codetool. The codetool runtime accepts a struct
// shaped like this — see codetool.Invocation.
type CodeInvocation struct {
	Slug      string
	Code      string
	Params    map[string]any
	TokenView *registry.TokenView
	TokenID   uuid.UUID
	ClientIP  string
	RowLimit  int
	Timeout   time.Duration
	// Preview indicates a dry-run from the admin UI: ACL, rate-limit, cache
	// and audit are bypassed so the author can test composition of tools
	// without holding a token grant.
	Preview bool
}

// Deps groups executor dependencies.
type Deps struct {
	Pool        *pgxpool.Pool
	Registry    *registry.Registry
	Cache       *cache.Cache
	Limiter     *ratelimit.Limiter
	Cipher      *crypto.Cipher
	IPMaxPerMin int
	CodeRuntime CodeRunner
}

func New(d Deps) *Executor {
	return &Executor{
		pool:    d.Pool,
		reg:     d.Registry,
		cache:   d.Cache,
		rl:      d.Limiter,
		cipher:  d.Cipher,
		ipMax:   d.IPMaxPerMin,
		code:    d.CodeRuntime,
		drivers: map[uuid.UUID]drivers.Driver{},
	}
}

// SetCodeRuntime wires the JS runtime after construction (used to break the
// circular dependency at startup: the runtime needs an Executor, the
// executor needs a runtime).
func (e *Executor) SetCodeRuntime(cr CodeRunner) {
	e.code = cr
}

// Invocation is the input to Run.
type Invocation struct {
	TokenID   uuid.UUID
	TokenView *registry.TokenView
	ToolSlug  string
	Params    map[string]any
	ClientIP  string
	NoCache   bool
	// Preview bypasses ACL, rate-limit, cache and audit. Set only by the
	// admin dry-run path (PreviewTool / Activate).
	Preview bool
}

// Run executes one tool call end-to-end.
func (e *Executor) Run(ctx context.Context, in Invocation) (*Result, error) {
	start := time.Now()
	snap := e.reg.Get()
	tool, ok := snap.BySlug[in.ToolSlug]
	if !ok {
		return nil, fmt.Errorf("tool %q not found", in.ToolSlug)
	}
	if !in.Preview {
		if in.TokenView == nil || !in.TokenView.CanAccess(tool) {
			e.recordExec(ctx, in, tool, 0, 0, false, "denied", ErrAccessDenied.Error())
			return nil, ErrAccessDenied
		}

		// ---- Rate limit ----
		if e.ipMax > 0 {
			ok, err := e.rl.Allow(ctx, "ip:"+in.ClientIP, e.ipMax)
			if err == nil && !ok {
				e.recordExec(ctx, in, tool, 0, 0, false, "rate_limited", "ip ceiling")
				return nil, ErrRateLimited
			}
		}
		if ok, err := e.rl.Allow(ctx, "token:"+in.TokenID.String(), in.TokenView.RateLimitPerMin); err == nil && !ok {
			e.recordExec(ctx, in, tool, 0, 0, false, "rate_limited", "token quota")
			return nil, ErrRateLimited
		}
		if ttRate := in.TokenView.RateForTool(tool); ttRate > 0 && ttRate != in.TokenView.RateLimitPerMin {
			if ok, err := e.rl.Allow(ctx, "tt:"+in.TokenID.String()+":"+tool.ID.String(), ttRate); err == nil && !ok {
				e.recordExec(ctx, in, tool, 0, 0, false, "rate_limited", "tool quota")
				return nil, ErrRateLimited
			}
		}
	}

	// ---- Cache lookup ----
	var cacheKey string
	if !in.Preview && tool.CacheTTLSec > 0 && !in.NoCache {
		cacheKey = cache.Key(tool.ID.String(), tool.Version, in.Params, tool.CachePerToken, in.TokenID.String())
		var cached drivers.ExecResult
		if err := e.cache.Get(ctx, cacheKey, &cached); err == nil {
			dur := time.Since(start)
			e.recordExec(ctx, in, tool, dur, cached.Count, true, "ok", "")
			return &Result{Data: &cached, CacheHit: true, Duration: dur}, nil
		}
	}

	// ---- Resolve and dispatch by kind ----
	var data *drivers.ExecResult
	var err error
	switch tool.Kind {
	case "code":
		if e.code == nil {
			err = errors.New("code runtime not configured")
			e.recordExec(ctx, in, tool, time.Since(start), 0, false, "error", err.Error())
			return nil, err
		}
		// Inject context params (token_id) into params for the JS scope.
		params := map[string]any{}
		for k, v := range in.Params {
			params[k] = v
		}
		data, err = e.code(ctx, CodeInvocation{
			Slug:      tool.Slug,
			Code:      tool.QueryText,
			Params:    params,
			TokenView: in.TokenView,
			TokenID:   in.TokenID,
			ClientIP:  in.ClientIP,
			RowLimit:  tool.RowLimit,
			Timeout:   time.Duration(tool.TimeoutMS) * time.Millisecond,
			Preview:   in.Preview,
		})
	default:
		// ---- Resolve driver (lazy, cached) ----
		var drv drivers.Driver
		drv, err = e.driverFor(ctx, tool.ConnectionID)
		if err != nil {
			e.recordExec(ctx, in, tool, time.Since(start), 0, false, "error", err.Error())
			return nil, err
		}

		// ---- Inject context params (_token_id) ----
		params := map[string]any{}
		for k, v := range in.Params {
			params[k] = v
		}
		params["_token_id"] = in.TokenID.String()

		// Auto-fill optional schema params the caller omitted with nil so
		// SQL idioms like `(:p IS NULL OR col = :p)` work transparently
		// when downstream tools (or external MCP clients) pass only a
		// subset of declared parameters.
		fillOptionalSchemaParams(tool.ParamsSchema, params)

		data, err = drv.Execute(ctx, drivers.ExecRequest{
			Query:    tool.QueryText,
			Params:   params,
			RowLimit: tool.RowLimit,
			Timeout:  time.Duration(tool.TimeoutMS) * time.Millisecond,
		})
	}
	dur := time.Since(start)
	if err != nil {
		status := "error"
		if errors.Is(err, context.DeadlineExceeded) {
			status = "timeout"
		}
		if !in.Preview {
			e.recordExec(ctx, in, tool, dur, 0, false, status, err.Error())
		}
		return nil, err
	}

	// ---- Cache write-through ----
	if cacheKey != "" {
		_ = e.cache.Set(ctx, cacheKey, data, time.Duration(tool.CacheTTLSec)*time.Second)
	}
	if !in.Preview {
		e.recordExec(ctx, in, tool, dur, data.Count, false, "ok", "")
	}
	return &Result{Data: data, CacheHit: false, Duration: dur}, nil
}

// driverFor returns a cached driver, building it from the encrypted config
// on first use.
func (e *Executor) driverFor(ctx context.Context, connID uuid.UUID) (drivers.Driver, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d, ok := e.drivers[connID]; ok {
		return d, nil
	}
	var kind string
	var enc []byte
	err := e.pool.QueryRow(ctx,
		`SELECT type, encrypted_config FROM connections WHERE id = $1`, connID).
		Scan(&kind, &enc)
	if err != nil {
		return nil, err
	}
	plain, err := e.cipher.Decrypt(enc, []byte("connection:"+connID.String()))
	if err != nil {
		return nil, fmt.Errorf("decrypt connection: %w", err)
	}
	d, err := drivers.Build(kind, plain)
	if err != nil {
		return nil, err
	}
	e.drivers[connID] = d
	return d, nil
}

// InvalidateDriver evicts a cached driver (e.g. after a connection edit).
func (e *Executor) InvalidateDriver(connID uuid.UUID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d, ok := e.drivers[connID]; ok {
		_ = d.Close()
		delete(e.drivers, connID)
	}
}

// DriverForConnection exposes the executor's driver cache for callers that
// need to run a one-off query against an existing connection (e.g. the
// codetool runtime's db() global). The returned driver is owned by the
// executor — DO NOT close it.
func (e *Executor) DriverForConnection(ctx context.Context, connID uuid.UUID) (drivers.Driver, error) {
	return e.driverFor(ctx, connID)
}

// RunBySlug re-enters the executor to run another tool on behalf of the same
// caller token. ACL is re-checked against tokenView — there is no privilege
// escalation. Used by code-tools to compose multiple tools. When preview is
// true the inner call inherits the dry-run bypass (no token grant required).
func (e *Executor) RunBySlug(ctx context.Context, tokenView *registry.TokenView, tokenID uuid.UUID,
	slug string, params map[string]any, clientIP string, preview bool) (*drivers.ExecResult, error) {
	res, err := e.Run(ctx, Invocation{
		TokenID:   tokenID,
		TokenView: tokenView,
		ToolSlug:  slug,
		Params:    params,
		ClientIP:  clientIP,
		Preview:   preview,
	})
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

func (e *Executor) recordExec(ctx context.Context, in Invocation, tool *registry.ToolDef,
	dur time.Duration, rowsReturned int, cacheHit bool, status, errMsg string) {
	redacted := redactParams(in.Params)
	rb, _ := json.Marshal(redacted)
	_, _ = e.pool.Exec(ctx, `
INSERT INTO tool_executions
  (token_id, tool_id, tool_slug, params_redacted, duration_ms, rows_returned,
   cache_hit, status, error_message, client_ip)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		in.TokenID, tool.ID, in.ToolSlug, rb,
		int(dur.Milliseconds()), rowsReturned, cacheHit, status, nullableString(errMsg), in.ClientIP)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// fillOptionalSchemaParams inserts a nil entry for every property declared in
// params_schema that is NOT listed in `required` and was NOT supplied by the
// caller. This lets query tools follow the common idiom
// `(:p IS NULL OR col = :p)` without forcing every caller (including
// tools.call() from a code-tool) to explicitly pass null for every optional
// placeholder.
func fillOptionalSchemaParams(raw json.RawMessage, params map[string]any) {
	if len(raw) == 0 {
		return
	}
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return
	}
	req := make(map[string]struct{}, len(s.Required))
	for _, r := range s.Required {
		req[r] = struct{}{}
	}
	for name := range s.Properties {
		if _, ok := params[name]; ok {
			continue
		}
		if _, isRequired := req[name]; isRequired {
			continue
		}
		params[name] = nil
	}
}

// redactParams masks sensitive-looking values.
func redactParams(p map[string]any) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		lk := toLower(k)
		if containsAny(lk, "password", "passwd", "secret", "token", "api_key", "apikey", "authorization") {
			out[k] = "***"
			continue
		}
		out[k] = v
	}
	return out
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
