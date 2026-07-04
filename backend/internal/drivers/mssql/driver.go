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
	clean, err := drivers.EnforceReadOnly(req.Query)
	if err != nil {
		return nil, err
	}
	q, args, err := drivers.RenderNamed(clean, "@p", req.Params)
	if err != nil {
		return nil, err
	}
	return mysql.ExecSQL(ctx, d.db, q, args, req)
}

// explainSettings returns the SET statement pair used to capture the query plan.
// With analyze=false, SHOWPLAN_XML returns the ESTIMATED plan WITHOUT executing
// the query; with analyze=true, STATISTICS XML executes the query and returns
// the ACTUAL plan alongside the results. Kept as a pure function so the generated
// commands can be unit-tested without a live database.
func explainSettings(analyze bool) (on, off string) {
	if analyze {
		return "SET STATISTICS XML ON", "SET STATISTICS XML OFF"
	}
	return "SET SHOWPLAN_XML ON", "SET SHOWPLAN_XML OFF"
}

// ensure mssql implements the optional EXPLAIN capability.
var _ drivers.Explainer = (*driver)(nil)

// Explain implements drivers.Explainer for SQL Server. It validates SELECT-only,
// then uses SHOWPLAN_XML (estimated, no execution) or STATISTICS XML (actual,
// executes the query) to obtain the XML execution plan. Both are session-scoped
// SET options, so the work runs on a pinned connection that is always reset. The
// caller must impose a short context timeout, especially when analyze=true.
func (d *driver) Explain(ctx context.Context, query string, analyze bool) (*drivers.ExplainResult, error) {
	clean, err := drivers.EnforceSelectOnly(query)
	if err != nil {
		return nil, err
	}
	on, off := explainSettings(analyze)
	// SHOWPLAN/STATISTICS XML must run on the same physical connection as the
	// query, so pin one and always reset it before returning it to the pool.
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, on); err != nil {
		return nil, err
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), off) }()

	rows, err := conn.QueryContext(ctx, clean)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plan, err := scanShowplanXML(rows)
	if err != nil {
		return nil, err
	}
	return &drivers.ExplainResult{
		Dialect: string(drivers.KindMSSQL),
		Plan:    plan,
		Format:  "xml",
		Analyze: analyze,
	}, nil
}

// scanShowplanXML extracts the Showplan XML from the result sets returned while
// SHOWPLAN_XML/STATISTICS XML is on. SHOWPLAN_XML yields a single single-column
// result set with the plan; STATISTICS XML interleaves the query's own result
// sets with a trailing single-column plan set. Non-plan (multi-column) result
// sets are drained, and the last single-column value wins — which is the plan.
func scanShowplanXML(rows *sql.Rows) (string, error) {
	var plan string
	found := false
	for {
		cols, err := rows.Columns()
		if err != nil {
			return "", err
		}
		if len(cols) == 1 {
			for rows.Next() {
				var s sql.NullString
				if err := rows.Scan(&s); err != nil {
					return "", err
				}
				if s.Valid {
					plan = s.String
					found = true
				}
			}
		} else {
			for rows.Next() { // drain the actual query rows (STATISTICS XML)
			}
		}
		if err := rows.Err(); err != nil {
			return "", err
		}
		if !rows.NextResultSet() {
			break
		}
	}
	if !found {
		return "", fmt.Errorf("mssql: no showplan XML returned")
	}
	return plan, nil
}

func init() { drivers.Register(drivers.KindMSSQL, New) }
