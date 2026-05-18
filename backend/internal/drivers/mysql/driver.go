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

func (d *driver) Execute(ctx context.Context, req drivers.ExecRequest) (*drivers.ExecResult, error) {
	if err := drivers.EnforceReadOnly(req.Query); err != nil {
		return nil, err
	}
	q, args, err := drivers.RenderNamed(req.Query, "?", req.Params)
	if err != nil {
		return nil, err
	}
	return execSQL(ctx, d.db, q, args, req)
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
