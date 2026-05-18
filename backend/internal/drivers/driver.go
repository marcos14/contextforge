// Package drivers defines the database driver interface used by the
// executor. Each implementation (pg, mysql, mssql, oracle, mongo, firebird,
// rest) knows how to ping, introspect and execute parameterised queries
// against its target.
package drivers

import (
	"context"
	"time"
)

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

// Factory builds a driver from its decrypted JSON config.
type Factory func(cfg []byte) (Driver, error)
