package oracle

import (
	"context"
	"errors"
	"testing"
)

func TestBuildExplainPlanSQL(t *testing.T) {
	const q = "SELECT id FROM users"
	if got, want := buildExplainPlanSQL(q), "EXPLAIN PLAN FOR "+q; got != want {
		t.Fatalf("buildExplainPlanSQL:\n got=%q\nwant=%q", got, want)
	}
	if got, want := displayPlanSQL(), "SELECT plan_table_output FROM TABLE(DBMS_XPLAN.DISPLAY())"; got != want {
		t.Fatalf("displayPlanSQL:\n got=%q\nwant=%q", got, want)
	}
}

func TestExplainStubNotEnabled(t *testing.T) {
	_, err := stub{}.Explain(context.Background(), "SELECT 1", false)
	if !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("expected ErrNotEnabled, got %v", err)
	}
}
