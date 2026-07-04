//go:build integration

package pg

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marcos14/contextforge/backend/internal/drivers"
)

// TestExplain_Integration validates the real Postgres EXPLAIN behaviour. It is
// gated by the `integration` build tag AND self-skips when the
// QUERY_STUDIO_TEST_PG_DSN env var is absent, so the integration gate stays
// green even without a live database.
//
// Run against a real Postgres with:
//
//	QUERY_STUDIO_TEST_PG_DSN="postgres://user:pass@host:5432/db?sslmode=disable" \
//	  go test -tags=integration ./internal/drivers/pg/ -run TestExplain_Integration -v
func TestExplain_Integration(t *testing.T) {
	dsn := os.Getenv("QUERY_STUDIO_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("QUERY_STUDIO_TEST_PG_DSN not set; skipping Postgres integration test")
	}

	cfg, err := json.Marshal(Config{DSN: dsn})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Plain EXPLAIN — must yield a JSON plan and NOT be flagged as analyze.
	res, err := drivers.Explain(ctx, d, "SELECT 1", false)
	if err != nil {
		t.Fatalf("Explain(analyze=false): %v", err)
	}
	if res.Format != "json" || res.Dialect != "pg" || res.Analyze {
		t.Fatalf("unexpected result: %+v", res)
	}
	var plan any
	if err := json.Unmarshal([]byte(res.Plan), &plan); err != nil {
		t.Fatalf("plan is not valid JSON: %v\nplan=%s", err, res.Plan)
	}

	// EXPLAIN ANALYZE — executes for real; plan should report actual timings.
	resA, err := drivers.Explain(ctx, d, "SELECT 1", true)
	if err != nil {
		t.Fatalf("Explain(analyze=true): %v", err)
	}
	if !resA.Analyze {
		t.Fatalf("analyze result not flagged: %+v", resA)
	}
	if !strings.Contains(resA.Plan, "Actual") {
		t.Fatalf("EXPLAIN ANALYZE plan lacks actual timings: %s", resA.Plan)
	}

	// SELECT-only enforcement: a DROP must be rejected before reaching the DB.
	if _, err := drivers.Explain(ctx, d, "DROP TABLE nope", false); err == nil {
		t.Fatalf("expected DROP to be rejected by EnforceSelectOnly")
	}
}

// TestIntrospectRich_Integration validates the real Postgres rich introspection
// (FKs + indexes) against ad-hoc tables. Gated by the `integration` tag and
// self-skips without QUERY_STUDIO_TEST_PG_DSN.
func TestIntrospectRich_Integration(t *testing.T) {
	dsn := os.Getenv("QUERY_STUDIO_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("QUERY_STUDIO_TEST_PG_DSN not set; skipping Postgres integration test")
	}

	cfg, err := json.Marshal(Config{DSN: dsn})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Setup fixture schema. Use a dedicated schema to avoid clobbering data.
	setup := []string{
		`CREATE SCHEMA IF NOT EXISTS qs_rich_test`,
		`DROP TABLE IF EXISTS qs_rich_test.child`,
		`DROP TABLE IF EXISTS qs_rich_test.parent`,
		`CREATE TABLE qs_rich_test.parent (id int PRIMARY KEY, code text)`,
		`CREATE TABLE qs_rich_test.child (id int PRIMARY KEY, parent_id int REFERENCES qs_rich_test.parent(id))`,
		`CREATE INDEX idx_child_parent ON qs_rich_test.child (parent_id)`,
	}
	for _, stmt := range setup {
		if err := runDDL(ctx, dsn, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	defer func() {
		_ = runDDL(context.Background(), dsn, `DROP TABLE IF EXISTS qs_rich_test.child`)
		_ = runDDL(context.Background(), dsn, `DROP TABLE IF EXISTS qs_rich_test.parent`)
		_ = runDDL(context.Background(), dsn, `DROP SCHEMA IF EXISTS qs_rich_test`)
	}()

	graph, err := drivers.IntrospectRich(ctx, d)
	if err != nil {
		t.Fatalf("IntrospectRich: %v", err)
	}

	var foundFK bool
	for _, rel := range graph.Relations {
		if rel.FromTable == "child" && rel.ToTable == "parent" &&
			len(rel.FromColumns) == 1 && rel.FromColumns[0] == "parent_id" &&
			len(rel.ToColumns) == 1 && rel.ToColumns[0] == "id" {
			foundFK = true
		}
	}
	if !foundFK {
		t.Fatalf("expected child->parent FK in relations: %+v", graph.Relations)
	}

	var foundIdx bool
	for _, ix := range graph.Indexes {
		if ix.Name == "idx_child_parent" && ix.Table == "child" &&
			len(ix.Columns) == 1 && ix.Columns[0] == "parent_id" {
			foundIdx = true
		}
	}
	if !foundIdx {
		t.Fatalf("expected idx_child_parent in indexes: %+v", graph.Indexes)
	}
}

// runDDL opens a short-lived pgx pool and executes one statement. Used only by
// the integration test to provision and tear down fixtures.
func runDDL(ctx context.Context, dsn, stmt string) error {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, stmt)
	return err
}
