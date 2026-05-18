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

func (d *driver) Execute(ctx context.Context, req drivers.ExecRequest) (*drivers.ExecResult, error) {
	if err := drivers.EnforceReadOnly(req.Query); err != nil {
		return nil, err
	}
	q, args, err := drivers.RenderNamed(req.Query, "$", req.Params)
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

func init() {
	drivers.Register(drivers.KindPg, New)
}
