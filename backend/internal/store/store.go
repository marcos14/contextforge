package store

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	pgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending migrations against the database in connURL.
func Migrate(ctx context.Context, connURL string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrations source: %w", err)
	}
	// The pgx/v5 migrate driver registers under the "pgx5" scheme. Accept
	// the standard "postgres://" or "postgresql://" URLs by rewriting the
	// scheme transparently.
	migrateURL := connURL
	if s := strings.SplitN(connURL, "://", 2); len(s) == 2 {
		switch s[0] {
		case "postgres", "postgresql":
			migrateURL = "pgx5://" + s[1]
		}
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL)
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Connect opens a pgx connection pool.
func Connect(ctx context.Context, connURL string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, connURL)
}

// Ensure pgx/v5 driver is registered for golang-migrate.
var _ = pgx.Postgres{}
