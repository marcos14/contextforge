// Package codetool runs user-supplied JavaScript snippets in a sandboxed
// goja runtime. A "code-tool" replaces the SQL/REST/Mongo query text of a
// regular tool with a JS function-body that receives the validated params and
// returns a value normalized to drivers.ExecResult (the same shape every
// other tool returns).
//
// The injected runtime exposes:
//
//	db(connectionName, queryOrSpec, paramsObj) -> { rows, columns, count }
//	tools.call(slug, paramsObj)                -> { rows, columns, count }
//	fetch(url, opts)                           -> { status, headers, body, json() }
//	log(...args), console.log(...args)
//	params (object), context (object: { token_id })
//
// Scripts are pure JavaScript (ES5.1 + most ES6). No require, no FS, no
// process — only the globals above. Each execution gets a fresh VM and is
// hard-killed when the tool timeout elapses (vm.Interrupt).
package codetool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/drivers"
	"github.com/marcos14/contextforge/backend/internal/registry"
)

// DriverOpener resolves and opens a driver for an existing connection by ID.
// Production wiring is the Executor's driver cache.
type DriverOpener interface {
	DriverForConnection(ctx context.Context, connID uuid.UUID) (drivers.Driver, error)
}

// ToolInvoker re-enters the executor to run another tool by slug. Production
// wiring is Executor.RunBySlug; grants of the calling token MUST be enforced
// unless preview is true (admin dry-run path).
type ToolInvoker interface {
	RunBySlug(ctx context.Context, tokenView *registry.TokenView, tokenID uuid.UUID,
		slug string, params map[string]any, clientIP string, preview bool) (*drivers.ExecResult, error)
}

// Runtime executes JS code-tools.
type Runtime struct {
	registry *registry.Registry
	opener   DriverOpener
	invoker  ToolInvoker
	log      *slog.Logger
}

// New builds a Runtime. opener/invoker may be nil for tests that don't
// exercise db()/tools.call.
func New(reg *registry.Registry, opener DriverOpener, invoker ToolInvoker, log *slog.Logger) *Runtime {
	if log == nil {
		log = slog.Default()
	}
	return &Runtime{registry: reg, opener: opener, invoker: invoker, log: log}
}

// connectionIDByName resolves a connection name through the registry
// snapshot. Returns an error if no connection matches.
func (rt *Runtime) connectionIDByName(name string) (uuid.UUID, error) {
	if rt.registry == nil {
		return uuid.Nil, fmt.Errorf("registry not configured")
	}
	snap := rt.registry.Get()
	if id, ok := snap.ConnByName[name]; ok {
		return id, nil
	}
	return uuid.Nil, fmt.Errorf("connection %q not found", name)
}

// Invocation is one code-tool execution.
type Invocation struct {
	Slug      string
	Code      string
	Params    map[string]any
	TokenView *registry.TokenView
	TokenID   uuid.UUID
	ClientIP  string
	RowLimit  int
	Timeout   time.Duration
	// Preview indicates this is a dry-run from the admin UI: nested
	// tools.call() bypass ACL/rate-limit/audit so authors can test
	// composition without holding a token grant.
	Preview bool
}

// errTimeout is the sentinel passed to vm.Interrupt() so callers can detect
// it and return context.DeadlineExceeded.
var errTimeout = errors.New("codetool: timeout")

// Compile is a cheap syntax check used by CreateTool/UpdateTool to fail fast
// before persisting an obviously-broken script.
func Compile(code string) error {
	_, err := goja.Compile("", wrapScript(code), false)
	return err
}

// wrapScript wraps the user code into a self-invoking function so that
// `return <value>;` is legal at the top level and the return value is
// captured into the VM result.
func wrapScript(code string) string {
	return "(function(){\n" + code + "\n})();"
}

// Run executes one code-tool. The returned ExecResult is normalized:
//   - {rows:[...], columns?:[...]} -> mapped as-is.
//   - [row1, row2, ...]           -> wrapped as { rows, columns: keys(row1) }.
//   - any other value             -> [{ value: <v> }].
//
// Errors thrown from JS become Go errors. Timeouts return
// context.DeadlineExceeded so executor records them as "timeout".
func (rt *Runtime) Run(ctx context.Context, inv Invocation) (*drivers.ExecResult, error) {
	if inv.Timeout <= 0 {
		inv.Timeout = 15 * time.Second
	}
	if inv.RowLimit <= 0 {
		inv.RowLimit = 1000
	}

	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))

	// ---- Hard timeout via interrupt ----
	timedOut := false
	runCtx, cancel := context.WithTimeout(ctx, inv.Timeout)
	defer cancel()
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-runCtx.Done():
			timedOut = true
			vm.Interrupt(errTimeout)
		case <-stopWatch:
		}
	}()
	defer close(stopWatch)

	// ---- Inject globals ----
	if err := rt.installGlobals(runCtx, vm, &inv); err != nil {
		return nil, fmt.Errorf("install globals: %w", err)
	}

	val, err := vm.RunString(wrapScript(inv.Code))
	if err != nil {
		if timedOut || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("script error: %w", err)
	}

	return normalizeResult(val, inv.RowLimit), nil
}

func (rt *Runtime) installGlobals(ctx context.Context, vm *goja.Runtime, inv *Invocation) error {
	if err := vm.Set("params", inv.Params); err != nil {
		return err
	}
	tokenIDStr := ""
	if inv.TokenID != uuid.Nil {
		tokenIDStr = inv.TokenID.String()
	}
	if err := vm.Set("context", map[string]any{
		"token_id":  tokenIDStr,
		"tool_slug": inv.Slug,
	}); err != nil {
		return err
	}

	// log / console.log
	logFn := func(call goja.FunctionCall) goja.Value {
		args := make([]any, 0, len(call.Arguments))
		for _, a := range call.Arguments {
			args = append(args, a.Export())
		}
		rt.log.Info("codetool.log", "slug", inv.Slug, "args", args)
		return goja.Undefined()
	}
	_ = vm.Set("log", logFn)
	_ = vm.Set("console", map[string]any{"log": logFn, "error": logFn, "warn": logFn, "info": logFn})

	// db(connName, sql, params) -> { rows, columns, count }
	_ = vm.Set("db", func(call goja.FunctionCall) goja.Value {
		if rt.opener == nil {
			panic(vm.NewTypeError("db() not available in this runtime"))
		}
		if len(call.Arguments) < 2 {
			panic(vm.NewTypeError("db(connectionName, query, params?) requires at least 2 args"))
		}
		connName := call.Argument(0).String()
		query := call.Argument(1).String()
		var dbParams map[string]any
		if len(call.Arguments) >= 3 && !goja.IsUndefined(call.Argument(2)) && !goja.IsNull(call.Argument(2)) {
			if m, ok := call.Argument(2).Export().(map[string]any); ok {
				dbParams = m
			}
		}
		if dbParams == nil {
			dbParams = map[string]any{}
		}
		if _, ok := dbParams["_token_id"]; !ok && inv.TokenID != uuid.Nil {
			dbParams["_token_id"] = inv.TokenID.String()
		}

		connID, err := rt.connectionIDByName(connName)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("db(%q): %w", connName, err)))
		}
		drv, err := rt.opener.DriverForConnection(ctx, connID)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("db(%q): %w", connName, err)))
		}
		res, err := drv.Execute(ctx, drivers.ExecRequest{
			Query: query, Params: dbParams,
			RowLimit: inv.RowLimit, Timeout: inv.Timeout,
		})
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("db(%q): %w", connName, err)))
		}
		return vm.ToValue(map[string]any{
			"rows":    res.Rows,
			"columns": res.Columns,
			"count":   res.Count,
		})
	})

	// tools.call(slug, params) -> { rows, columns, count }
	toolsCall := func(call goja.FunctionCall) goja.Value {
		if rt.invoker == nil {
			panic(vm.NewTypeError("tools.call() not available in this runtime"))
		}
		if len(call.Arguments) < 1 {
			panic(vm.NewTypeError("tools.call(slug, params?) requires a slug"))
		}
		slug := call.Argument(0).String()
		var p map[string]any
		if len(call.Arguments) >= 2 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
			if m, ok := call.Argument(1).Export().(map[string]any); ok {
				p = m
			}
		}
		if p == nil {
			p = map[string]any{}
		}
		res, err := rt.invoker.RunBySlug(ctx, inv.TokenView, inv.TokenID, slug, p, inv.ClientIP, inv.Preview)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("tools.call(%q): %w", slug, err)))
		}
		return vm.ToValue(map[string]any{
			"rows":    res.Rows,
			"columns": res.Columns,
			"count":   res.Count,
		})
	}
	_ = vm.Set("tools", map[string]any{"call": toolsCall})

	// fetch(url, opts) -> { status, headers, body, json() }
	_ = vm.Set("fetch", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			panic(vm.NewTypeError("fetch(url, opts?) requires a url"))
		}
		urlStr := call.Argument(0).String()
		method := "GET"
		var headers map[string]any
		var bodyStr string
		var queryParams map[string]any

		if len(call.Arguments) >= 2 && !goja.IsUndefined(call.Argument(1)) && !goja.IsNull(call.Argument(1)) {
			if opts, ok := call.Argument(1).Export().(map[string]any); ok {
				if m, ok := opts["method"].(string); ok && m != "" {
					method = strings.ToUpper(m)
				}
				if h, ok := opts["headers"].(map[string]any); ok {
					headers = h
				}
				if q, ok := opts["query"].(map[string]any); ok {
					queryParams = q
				}
				switch b := opts["body"].(type) {
				case string:
					bodyStr = b
				case nil:
				default:
					raw, jerr := json.Marshal(b)
					if jerr != nil {
						panic(vm.NewGoError(fmt.Errorf("fetch: encode body: %w", jerr)))
					}
					bodyStr = string(raw)
					if headers == nil {
						headers = map[string]any{}
					}
					if _, has := headers["Content-Type"]; !has {
						headers["Content-Type"] = "application/json"
					}
				}
			}
		}

		if len(queryParams) > 0 {
			u, err := url.Parse(urlStr)
			if err != nil {
				panic(vm.NewGoError(fmt.Errorf("fetch: parse url: %w", err)))
			}
			q := u.Query()
			for k, v := range queryParams {
				q.Set(k, fmt.Sprint(v))
			}
			u.RawQuery = q.Encode()
			urlStr = u.String()
		}

		req, err := http.NewRequestWithContext(ctx, method, urlStr, strings.NewReader(bodyStr))
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("fetch: %w", err)))
		}
		for k, v := range headers {
			req.Header.Set(k, fmt.Sprint(v))
		}
		client := &http.Client{Timeout: inv.Timeout}
		resp, err := client.Do(req)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("fetch: %w", err)))
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)

		hdrMap := map[string]any{}
		for k := range resp.Header {
			hdrMap[k] = resp.Header.Get(k)
		}

		obj := vm.NewObject()
		_ = obj.Set("status", resp.StatusCode)
		_ = obj.Set("headers", hdrMap)
		_ = obj.Set("body", string(respBody))
		_ = obj.Set("json", func(goja.FunctionCall) goja.Value {
			var v any
			if err := json.Unmarshal(respBody, &v); err != nil {
				panic(vm.NewGoError(fmt.Errorf("fetch.json: %w", err)))
			}
			return vm.ToValue(v)
		})
		return obj
	})

	return nil
}

// normalizeResult converts the JS return value into an ExecResult.
func normalizeResult(v goja.Value, rowLimit int) *drivers.ExecResult {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return &drivers.ExecResult{Columns: []string{}, Rows: []map[string]any{}, Count: 0}
	}
	exp := v.Export()

	// Case 1: object with {rows, columns?}
	if obj, ok := exp.(map[string]any); ok {
		if rowsAny, has := obj["rows"]; has {
			rows := toRowList(rowsAny)
			if len(rows) > rowLimit {
				rows = rows[:rowLimit]
			}
			cols := columnsFromMaybe(obj["columns"], rows)
			return &drivers.ExecResult{Columns: cols, Rows: rows, Count: len(rows)}
		}
		// Plain object -> single row.
		return &drivers.ExecResult{
			Columns: keysOf(obj),
			Rows:    []map[string]any{obj},
			Count:   1,
		}
	}

	// Case 2: array -> rows
	if arr, ok := exp.([]any); ok {
		rows := toRowList(arr)
		if len(rows) > rowLimit {
			rows = rows[:rowLimit]
		}
		cols := []string{}
		if len(rows) > 0 {
			cols = keysOf(rows[0])
		}
		return &drivers.ExecResult{Columns: cols, Rows: rows, Count: len(rows)}
	}

	// Case 3: scalar -> single {value:..} row
	return &drivers.ExecResult{
		Columns: []string{"value"},
		Rows:    []map[string]any{{"value": exp}},
		Count:   1,
	}
}

func toRowList(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
			continue
		}
		out = append(out, map[string]any{"value": item})
	}
	return out
}

func columnsFromMaybe(v any, rows []map[string]any) []string {
	if arr, ok := v.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, x := range arr {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	if len(rows) > 0 {
		return keysOf(rows[0])
	}
	return []string{}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
