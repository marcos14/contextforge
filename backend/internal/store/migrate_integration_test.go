//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestQuerySessionsMigration_Rollback validates that migration 0005 rolls back
// and reapplies cleanly (its down.sql drops query_sessions, the up.sql recreates
// it). Gated by the `integration` build tag AND self-skips without
// QUERY_STUDIO_TEST_PG_DSN, so the integration gate stays green without a DB.
//
// Run against a real Postgres with:
//
//	QUERY_STUDIO_TEST_PG_DSN="postgres://user:pass@host:5432/db?sslmode=disable" \
//	  go test -tags=integration ./internal/store/ -run TestQuerySessionsMigration_Rollback -v
func TestQuerySessionsMigration_Rollback(t *testing.T) {
	dsn := os.Getenv("QUERY_STUDIO_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("QUERY_STUDIO_TEST_PG_DSN not set; skipping Postgres integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Bring the schema fully up first.
	if err := Migrate(ctx, dsn); err != nil {
		t.Fatalf("initial migrate up: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	tableExists := func() bool {
		var reg *string
		if err := pool.QueryRow(ctx,
			`SELECT to_regclass('public.query_sessions')::text`).Scan(&reg); err != nil {
			t.Fatalf("to_regclass: %v", err)
		}
		return reg != nil
	}

	if !tableExists() {
		t.Fatalf("query_sessions should exist after migrate up")
	}

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("migrations source: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, toMigrateURL(dsn))
	if err != nil {
		t.Fatalf("migrate init: %v", err)
	}
	defer m.Close()

	// Roll back exactly one step (0005 down) and confirm the table is gone.
	if err := m.Steps(-1); err != nil {
		t.Fatalf("rollback step: %v", err)
	}
	if tableExists() {
		t.Fatalf("query_sessions should be dropped after rollback")
	}

	// Reapply and confirm the table is back, leaving the DB at the latest version.
	if err := m.Steps(1); err != nil {
		t.Fatalf("reapply step: %v", err)
	}
	if !tableExists() {
		t.Fatalf("query_sessions should exist again after reapply")
	}
}
