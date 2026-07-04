package pg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marcos14/contextforge/backend/internal/drivers"
)

// Config is the PostgreSQL connection configuration (stored encrypted).
type Config struct {
	DSN string `json:"dsn"`
}

type driver struct {
	pool *pgxpool.Pool
}

// New creates a PostgreSQL driver from a JSON config.
func New(cfg []byte) (drivers.Driver, error) {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("pg config: %w", err)
	}
	if c.DSN == "" {
		return nil, fmt.Errorf("pg: dsn required")
	}
	pcfg, err := pgxpool.ParseConfig(c.DSN)
	if err != nil {
		return nil, fmt.Errorf("pg parse dsn: %w", err)
	}
	pcfg.MaxConns = 5
	// Use the simple query protocol: pgx safely interpolates the args into the
	// SQL text. This sidesteps Postgres' type inference for parameters, which
	// fails (SQLSTATE 42P08) on common NULL-tolerant patterns such as
	// "WHERE ($1 IS NULL OR col = $1)". pgx still escapes values properly, so
	// this is safe.
	pcfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	pool, err := pgxpool.NewWithConfig(context.Background(), pcfg)
	if err != nil {
		return nil, err
	}
	return &driver{pool: pool}, nil
}

func (d *driver) Kind() drivers.Kind { return drivers.KindPg }

func (d *driver) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

func (d *driver) Close() error { d.pool.Close(); return nil }

func (d *driver) Introspect(ctx context.Context) ([]drivers.Table, error) {
	const q = `
SELECT n.nspname  AS schema,
       c.relname  AS table_name,
       a.attname  AS column_name,
       format_type(a.atttypid, a.atttypmod) AS data_type,
       NOT a.attnotnull AS is_nullable
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
WHERE  c.relkind IN ('r','v','m','p')
  AND  n.nspname NOT IN ('pg_catalog','information_schema')
ORDER  BY n.nspname, c.relname, a.attnum`
	rows, err := d.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type key struct{ s, t string }
	idx := map[key]int{}
	out := []drivers.Table{}
	for rows.Next() {
		var schema, table, col, typ string
		var nullable bool
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
		out[i].Columns = append(out[i].Columns, drivers.Column{Name: col, Type: typ, Nullable: nullable})
	}
	return out, rows.Err()
}

// ensure pg implements the optional rich-introspection capability.
var _ drivers.RichIntrospector = (*driver)(nil)

// IntrospectRich implements drivers.RichIntrospector for PostgreSQL. It reuses
// Introspect for the tables and enriches it with foreign keys (pg_constraint)
// and existing indexes (pg_index). Expression index columns (attnum 0) are
// skipped — only plain column indexes are reported.
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
	// unnest(conkey, confkey) WITH ORDINALITY expands composite FKs while
	// preserving column order (ord), which BuildRelations relies on.
	const q = `
SELECT con.conname                        AS constraint_name,
       ns.nspname                         AS from_schema,
       cl.relname                         AS from_table,
       att.attname                        AS from_column,
       fns.nspname                        AS to_schema,
       fcl.relname                        AS to_table,
       fatt.attname                       AS to_column
FROM   pg_constraint con
JOIN   pg_class cl        ON cl.oid = con.conrelid
JOIN   pg_namespace ns    ON ns.oid = cl.relnamespace
JOIN   pg_class fcl       ON fcl.oid = con.confrelid
JOIN   pg_namespace fns   ON fns.oid = fcl.relnamespace
JOIN   LATERAL unnest(con.conkey, con.confkey) WITH ORDINALITY AS k(conkey, confkey, ord) ON true
JOIN   pg_attribute att   ON att.attrelid = con.conrelid  AND att.attnum = k.conkey
JOIN   pg_attribute fatt  ON fatt.attrelid = con.confrelid AND fatt.attnum = k.confkey
WHERE  con.contype = 'f'
  AND  ns.nspname NOT IN ('pg_catalog','information_schema')
ORDER  BY ns.nspname, cl.relname, con.conname, k.ord`
	rows, err := d.pool.Query(ctx, q)
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
	// unnest(indkey) WITH ORDINALITY preserves index column order. attnum > 0
	// filters out expression-index entries (attnum 0), which have no column.
	const q = `
SELECT ns.nspname     AS schema,
       t.relname      AS table_name,
       i.relname      AS index_name,
       ix.indisunique AS is_unique,
       a.attname      AS column_name
FROM   pg_index ix
JOIN   pg_class i      ON i.oid = ix.indexrelid
JOIN   pg_class t      ON t.oid = ix.indrelid
JOIN   pg_namespace ns ON ns.oid = t.relnamespace
JOIN   LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
JOIN   pg_attribute a  ON a.attrelid = t.oid AND a.attnum = k.attnum
WHERE  ns.nspname NOT IN ('pg_catalog','information_schema')
  AND  t.relkind IN ('r','p')
  AND  k.attnum > 0
ORDER  BY ns.nspname, t.relname, i.relname, k.ord`
	rows, err := d.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var acc []drivers.IndexColumn
	for rows.Next() {
		var c drivers.IndexColumn
		if err := rows.Scan(&c.Schema, &c.Table, &c.Name, &c.Unique, &c.Column); err != nil {
			return nil, err
		}
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
	q, args, err := drivers.RenderNamed(clean, "$", req.Params)
	if err != nil {
		return nil, err
	}
	cctx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	rows, err := d.pool.Query(cctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = string(f.Name)
	}

	res := &drivers.ExecResult{Columns: cols, Rows: []map[string]any{}}
	for rows.Next() {
		if req.RowLimit > 0 && res.Count >= req.RowLimit {
			break
		}
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = vals[i]
		}
		res.Rows = append(res.Rows, row)
		res.Count++
	}
	return res, rows.Err()
}

// buildExplainSQL wraps a SELECT query in a Postgres EXPLAIN command. With
// analyze=false it produces the safe, non-executing estimated plan; with
// analyze=true it runs the query for real and includes buffer statistics.
// Kept as a pure function so the generated command can be unit-tested without a
// live database.
func buildExplainSQL(query string, analyze bool) string {
	if analyze {
		return "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + query
	}
	return "EXPLAIN (FORMAT JSON) " + query
}

// Explain implements drivers.Explainer for PostgreSQL. It validates that the
// input is a plain SELECT (via EnforceSelectOnly), wraps it in EXPLAIN and
// returns the JSON plan. The caller is responsible for imposing a short
// context timeout, especially when analyze=true (which executes the query).
func (d *driver) Explain(ctx context.Context, query string, analyze bool) (*drivers.ExplainResult, error) {
	clean, err := drivers.EnforceSelectOnly(query)
	if err != nil {
		return nil, err
	}
	// EXPLAIN (FORMAT JSON) returns a single row with a single json column
	// holding the plan array. The simple query protocol delivers it as text,
	// so scanning into a string works.
	var plan string
	if err := d.pool.QueryRow(ctx, buildExplainSQL(clean, analyze)).Scan(&plan); err != nil {
		return nil, err
	}
	return &drivers.ExplainResult{
		Dialect: string(drivers.KindPg),
		Plan:    plan,
		Format:  "json",
		Analyze: analyze,
	}, nil
}

func init() {
	drivers.Register(drivers.KindPg, New)
}
