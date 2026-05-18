package firebird

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/marcos14/contextforge/backend/internal/drivers"
	"github.com/marcos14/contextforge/backend/internal/drivers/mysql"
	_ "github.com/nakagami/firebirdsql"
)

// Config is the Firebird 5 connection configuration.
// DSN format: user:password@host:3050/path/to/db.fdb
type Config struct {
	DSN string `json:"dsn"`
}

type driver struct{ db *sql.DB }

func New(cfg []byte) (drivers.Driver, error) {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("firebird config: %w", err)
	}
	if c.DSN == "" {
		return nil, fmt.Errorf("firebird: dsn required")
	}
	db, err := sql.Open("firebirdsql", c.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(3)
	return &driver{db: db}, nil
}

func (d *driver) Kind() drivers.Kind             { return drivers.KindFirebird }
func (d *driver) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }
func (d *driver) Close() error                   { return d.db.Close() }

func (d *driver) Introspect(ctx context.Context) ([]drivers.Table, error) {
	const q = `
SELECT TRIM(r.RDB$RELATION_NAME) AS table_name,
       TRIM(f.RDB$FIELD_NAME)    AS column_name,
       CASE f.RDB$NULL_FLAG WHEN 1 THEN 'N' ELSE 'Y' END AS nullable,
       TRIM(t.RDB$TYPE_NAME)     AS data_type
FROM   RDB$RELATIONS r
JOIN   RDB$RELATION_FIELDS f ON f.RDB$RELATION_NAME = r.RDB$RELATION_NAME
JOIN   RDB$FIELDS fld        ON fld.RDB$FIELD_NAME = f.RDB$FIELD_SOURCE
JOIN   RDB$TYPES t           ON t.RDB$TYPE = fld.RDB$FIELD_TYPE AND t.RDB$FIELD_NAME = 'RDB$FIELD_TYPE'
WHERE  COALESCE(r.RDB$SYSTEM_FLAG,0)=0
ORDER  BY r.RDB$RELATION_NAME, f.RDB$FIELD_POSITION`
	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]int{}
	var out []drivers.Table
	for rows.Next() {
		var table, col, nullable, typ string
		if err := rows.Scan(&table, &col, &nullable, &typ); err != nil {
			return nil, err
		}
		i, ok := idx[table]
		if !ok {
			out = append(out, drivers.Table{Name: table})
			i = len(out) - 1
			idx[table] = i
		}
		out[i].Columns = append(out[i].Columns, drivers.Column{Name: col, Type: typ, Nullable: nullable == "Y"})
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
	return mysql.ExecSQL(ctx, d.db, q, args, req)
}

func init() { drivers.Register(drivers.KindFirebird, New) }
