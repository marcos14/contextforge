//go:build integration

package pg

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

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
