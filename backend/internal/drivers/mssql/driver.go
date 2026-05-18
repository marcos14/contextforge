package mssql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/marcos14/contextforge/backend/internal/drivers"
	"github.com/marcos14/contextforge/backend/internal/drivers/mysql"
	_ "github.com/microsoft/go-mssqldb"
)

type Config struct {
	DSN string `json:"dsn"` // sqlserver://user:pass@host:1433?database=...
}

type driver struct{ db *sql.DB }

func New(cfg []byte) (drivers.Driver, error) {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("mssql config: %w", err)
	}
	if c.DSN == "" {
		return nil, fmt.Errorf("mssql: dsn required")
	}
	db, err := sql.Open("sqlserver", c.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(5)
	return &driver{db: db}, nil
}

func (d *driver) Kind() drivers.Kind             { return drivers.KindMSSQL }
func (d *driver) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }
func (d *driver) Close() error                   { return d.db.Close() }

func (d *driver) Introspect(ctx context.Context) ([]drivers.Table, error) {
	const q = `
SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, DATA_TYPE, IS_NULLABLE
FROM   INFORMATION_SCHEMA.COLUMNS
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
	q, args, err := drivers.RenderNamed(req.Query, "@p", req.Params)
	if err != nil {
		return nil, err
	}
	return mysql.ExecSQL(ctx, d.db, q, args, req)
}

func init() { drivers.Register(drivers.KindMSSQL, New) }
