// Package drivers defines the database driver interface used by the
// executor. Each implementation (pg, mysql, mssql, oracle, mongo, firebird,
// rest) knows how to ping, introspect and execute parameterised queries
// against its target.
package drivers

import (
	"context"
	"errors"
	"time"
)

// ErrUnsupported is returned by capability helpers when a driver does not
// implement an optional capability (e.g. EXPLAIN). Callers should use
// errors.Is(err, ErrUnsupported) to detect it and degrade gracefully (for
// example, disabling the EXPLAIN button in the UI).
var ErrUnsupported = errors.New("capability not supported by driver")

// Kind identifies a driver implementation.
type Kind string

const (
	KindPg       Kind = "pg"
	KindMySQL    Kind = "mysql"
	KindMSSQL    Kind = "mssql"
	KindOracle   Kind = "oracle"
	KindMongo    Kind = "mongo"
	KindFirebird Kind = "firebird"
	KindREST     Kind = "rest"
)

// Column describes one column in an introspected table.
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}

// Table describes one table/collection.
type Table struct {
	Schema  string   `json:"schema,omitempty"`
	Name    string   `json:"name"`
	Columns []Column `json:"columns,omitempty"`
}

// ExecRequest is a parameterised query execution request.
type ExecRequest struct {
	Query    string         // SQL or driver-specific spec (e.g. JSON for Mongo)
	Params   map[string]any // named parameters, :name placeholders in Query
	RowLimit int            // server-enforced max rows
	Timeout  time.Duration  // hard timeout
}

// ExecResult is the normalised result of executing a tool.
type ExecResult struct {
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	Count   int              `json:"count"`
}

// Driver is the contract every backend implementation satisfies.
type Driver interface {
	Kind() Kind
	Ping(ctx context.Context) error
	Introspect(ctx context.Context) ([]Table, error)
	Execute(ctx context.Context, req ExecRequest) (*ExecResult, error)
	Close() error
}

// ExplainResult is the normalised output of an EXPLAIN operation.
type ExplainResult struct {
	Dialect string `json:"dialect"` // driver kind ("pg", "mysql", ...)
	Plan    string `json:"plan"`    // plan text (or serialised JSON)
	Format  string `json:"format"`  // "text" | "json"
	Analyze bool   `json:"analyze"` // true when the query was actually executed (ANALYZE)
}

// Explainer is an optional capability: drivers that can produce a query
// execution plan implement it. Drivers without support simply do not implement
// the interface; use the package-level Explain helper to invoke it safely.
//
// When analyze is true the query is executed for real (EXPLAIN ANALYZE);
// callers must guard this with an explicit opt-in and a short timeout.
type Explainer interface {
	Explain(ctx context.Context, query string, analyze bool) (*ExplainResult, error)
}

// Explain is the single entry point for the EXPLAIN capability. It type-asserts
// the driver to Explainer and delegates, returning ErrUnsupported when the
// driver does not implement the capability. Callers should always go through
// this helper instead of asserting Explainer themselves.
func Explain(ctx context.Context, d Driver, query string, analyze bool) (*ExplainResult, error) {
	ex, ok := d.(Explainer)
	if !ok {
		return nil, ErrUnsupported
	}
	return ex.Explain(ctx, query, analyze)
}

// Factory builds a driver from its decrypted JSON config.
type Factory func(cfg []byte) (Driver, error)
