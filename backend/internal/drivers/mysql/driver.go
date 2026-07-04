package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	"github.com/marcos14/contextforge/backend/internal/drivers"
)

type Config struct {
	DSN string `json:"dsn"` // user:pass@tcp(host:3306)/db?parseTime=true
}

type driver struct{ db *sql.DB }

func New(cfg []byte) (drivers.Driver, error) {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("mysql config: %w", err)
	}
	if c.DSN == "" {
		return nil, fmt.Errorf("mysql: dsn required")
	}
	db, err := sql.Open("mysql", c.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	return &driver{db: db}, nil
}

func (d *driver) Kind() drivers.Kind             { return drivers.KindMySQL }
func (d *driver) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }
func (d *driver) Close() error                   { return d.db.Close() }

func (d *driver) Introspect(ctx context.Context) ([]drivers.Table, error) {
	const q = `
SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE
FROM   INFORMATION_SCHEMA.COLUMNS
WHERE  TABLE_SCHEMA NOT IN ('information_schema','mysql','performance_schema','sys')
ORDER  BY TABLE_SCHEMA, TABLE_NAME, ORDINAL_POSITION`
	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ s, t string }
	idx := map[key]int{}
	var out []drivers.Table
	for rows.Next() {
		var schema, table, col, typ, nullable string
		if err := rows.Scan(&schema, &table, &col, &typ, &nullable); err != nil {
			return nil, err
		}
		k := key{schema, table}
		i, ok := idx[k]
		if !ok {
			out = append(out, drivers.Table{Schema: schema, Name: table})
			i = len(out) - 1
			idx[k] = i
		}
		out[i].Columns = append(out[i].Columns, drivers.Column{Name: col, Type: typ, Nullable: nullable == "YES"})
	}
	return out, rows.Err()
}

// ensure mysql implements the optional rich-introspection capability.
var _ drivers.RichIntrospector = (*driver)(nil)

// IntrospectRich implements drivers.RichIntrospector for MySQL. It reuses
// Introspect for tables and enriches it with foreign keys
// (INFORMATION_SCHEMA.KEY_COLUMN_USAGE) and existing indexes
// (INFORMATION_SCHEMA.STATISTICS).
func (d *driver) IntrospectRich(ctx context.Context) (*drivers.SchemaGraph, error) {
	tables, err := d.Introspect(ctx)
	if err != nil {
		return nil, err
	}
	relations, err := d.introspectRelations(ctx)
	if err != nil {
		return nil, err
	}
	indexes, err := d.introspectIndexes(ctx)
	if err != nil {
		return nil, err
	}
	return &drivers.SchemaGraph{Tables: tables, Relations: relations, Indexes: indexes}, nil
}

func (d *driver) introspectRelations(ctx context.Context) ([]drivers.Relation, error) {
	// REFERENCED_TABLE_NAME is non-null only for FK columns. ORDINAL_POSITION
	// orders composite FK columns; CONSTRAINT_NAME is unique only within a
	// table, so we order (and BuildRelations groups) by table + constraint.
	const q = `
SELECT CONSTRAINT_NAME, TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME,
       REFERENCED_TABLE_SCHEMA, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
FROM   INFORMATION_SCHEMA.KEY_COLUMN_USAGE
WHERE  REFERENCED_TABLE_NAME IS NOT NULL
  AND  TABLE_SCHEMA NOT IN ('information_schema','mysql','performance_schema','sys')
ORDER  BY TABLE_SCHEMA, TABLE_NAME, CONSTRAINT_NAME, ORDINAL_POSITION`
	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var acc []drivers.FKColumn
	for rows.Next() {
		var c drivers.FKColumn
		if err := rows.Scan(&c.ConstraintName, &c.FromSchema, &c.FromTable, &c.FromColumn,
			&c.ToSchema, &c.ToTable, &c.ToColumn); err != nil {
			return nil, err
		}
		acc = append(acc, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return drivers.BuildRelations(acc), nil
}

func (d *driver) introspectIndexes(ctx context.Context) ([]drivers.IndexInfo, error) {
	// NON_UNIQUE=0 means the index is unique. SEQ_IN_INDEX orders composite
	// index columns.
	const q = `
SELECT INDEX_SCHEMA, TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME
FROM   INFORMATION_SCHEMA.STATISTICS
WHERE  TABLE_SCHEMA NOT IN ('information_schema','mysql','performance_schema','sys')
  AND  COLUMN_NAME IS NOT NULL
ORDER  BY INDEX_SCHEMA, TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`
	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var acc []drivers.IndexColumn
	for rows.Next() {
		var (
			c         drivers.IndexColumn
			nonUnique int
		)
		if err := rows.Scan(&c.Schema, &c.Table, &c.Name, &nonUnique, &c.Column); err != nil {
			return nil, err
		}
		c.Unique = nonUnique == 0
		acc = append(acc, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return drivers.BuildIndexes(acc), nil
}

func (d *driver) Execute(ctx context.Context, req drivers.ExecRequest) (*drivers.ExecResult, error) {
	clean, err := drivers.EnforceReadOnly(req.Query)
	if err != nil {
		return nil, err
	}
	q, args, err := drivers.RenderNamed(clean, "?", req.Params)
	if err != nil {
		return nil, err
	}
	return execSQL(ctx, d.db, q, args, req)
}

// buildExplainSQL wraps a SELECT in a MySQL EXPLAIN command. With analyze=false
// it returns the estimated plan as JSON (EXPLAIN FORMAT=JSON, does not execute);
// with analyze=true it runs the query for real and returns the TREE-format
// textual plan (EXPLAIN ANALYZE, MySQL 8.0.18+). Kept as a pure function so the
// generated command can be unit-tested without a live database.
func buildExplainSQL(query string, analyze bool) string {
	if analyze {
		return "EXPLAIN ANALYZE " + query
	}
	return "EXPLAIN FORMAT=JSON " + query
}

// ensure mysql implements the optional EXPLAIN capability.
var _ drivers.Explainer = (*driver)(nil)

// Explain implements drivers.Explainer for MySQL. It validates that the input is
// a plain SELECT (via EnforceSelectOnly), wraps it in EXPLAIN and returns the
// plan: JSON when analyze is false, TREE-format text when analyze is true (which
// executes the query). The caller is responsible for imposing a short context
// timeout, especially when analyze=true.
func (d *driver) Explain(ctx context.Context, query string, analyze bool) (*drivers.ExplainResult, error) {
	clean, err := drivers.EnforceSelectOnly(query)
	if err != nil {
		return nil, err
	}
	// Both EXPLAIN FORMAT=JSON and EXPLAIN ANALYZE return a single row with a
	// single column holding the plan, so scanning into a string works.
	var plan string
	if err := d.db.QueryRowContext(ctx, buildExplainSQL(clean, analyze)).Scan(&plan); err != nil {
		return nil, err
	}
	format := "json"
	if analyze {
		format = "text"
	}
	return &drivers.ExplainResult{
		Dialect: string(drivers.KindMySQL),
		Plan:    plan,
		Format:  format,
		Analyze: analyze,
	}, nil
}

// execSQL is shared by SQL-based drivers (mysql, mssql, firebird).
func execSQL(ctx context.Context, db *sql.DB, q string, args []any, req drivers.ExecRequest) (*drivers.ExecResult, error) {
	cctx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	rows, err := db.QueryContext(cctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &drivers.ExecResult{Columns: cols, Rows: []map[string]any{}}
	for rows.Next() {
		if req.RowLimit > 0 && res.Count >= req.RowLimit {
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[c] = v
		}
		res.Rows = append(res.Rows, row)
		res.Count++
	}
	return res, rows.Err()
}

// ExecSQL is exposed so other SQL drivers can reuse the row-mapping helper.
func ExecSQL(ctx context.Context, db *sql.DB, q string, args []any, req drivers.ExecRequest) (*drivers.ExecResult, error) {
	return execSQL(ctx, db, q, args, req)
}

func init() { drivers.Register(drivers.KindMySQL, New) }
