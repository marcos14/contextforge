package drivers

import (
	"reflect"
	"strings"
	"testing"
)

func TestEnforceReadOnly_StripsTrailingSemicolon(t *testing.T) {
	clean, err := EnforceReadOnly("SELECT 1;")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clean != "SELECT 1" {
		t.Fatalf("trailing ; not stripped: got %q", clean)
	}
}

func TestEnforceReadOnly_StripsMultipleTrailingSemicolons(t *testing.T) {
	clean, err := EnforceReadOnly("SELECT 1 ;\n;  ;")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clean != "SELECT 1" {
		t.Fatalf("trailing ; not stripped: got %q", clean)
	}
}

func TestEnforceReadOnly_RejectsMultipleStatements(t *testing.T) {
	if _, err := EnforceReadOnly("SELECT 1; SELECT 2"); err == nil ||
		!strings.Contains(err.Error(), "multiple statements") {
		t.Fatalf("expected multi-statement rejection, got %v", err)
	}
}

func TestEnforceReadOnly_RejectsDestructive(t *testing.T) {
	if _, err := EnforceReadOnly("DELETE FROM t"); err == nil {
		t.Fatalf("expected destructive rejection")
	}
}

func TestRenderNamed_QMarkDialect_DuplicatesArgsPerOccurrence(t *testing.T) {
	// Regression: Firebird/MySQL use anonymous `?` placeholders that do not
	// support positional reuse. Each occurrence of :id must produce its own
	// arg or the driver returns "Wrong number of parameters".
	got, args, err := RenderNamed("SELECT * FROM t WHERE x = :id OR y = :id", "?", map[string]any{"id": 7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "SELECT * FROM t WHERE x = ? OR y = ?"
	if got != want {
		t.Fatalf("query mismatch:\n got=%q\nwant=%q", got, want)
	}
	if !reflect.DeepEqual(args, []any{7, 7}) {
		t.Fatalf("args should be duplicated for `?` dialect: got %v", args)
	}
}

func TestRenderNamed_DollarDialect_ReusesPositionalIndex(t *testing.T) {
	// Postgres supports reuse: a single arg is referenced by multiple $N.
	got, args, err := RenderNamed("SELECT * FROM t WHERE x = :id OR y = :id", "$", map[string]any{"id": 7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "SELECT * FROM t WHERE x = $1 OR y = $1"
	if got != want {
		t.Fatalf("query mismatch:\n got=%q\nwant=%q", got, want)
	}
	if !reflect.DeepEqual(args, []any{7}) {
		t.Fatalf("args should be deduped for `$` dialect: got %v", args)
	}
}

func TestRenderNamed_AtPDialect_ReusesPositionalIndex(t *testing.T) {
	got, args, err := RenderNamed("SELECT * FROM t WHERE x = :id OR y = :id", "@p", map[string]any{"id": 7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "SELECT * FROM t WHERE x = @p1 OR y = @p1"
	if got != want {
		t.Fatalf("query mismatch:\n got=%q\nwant=%q", got, want)
	}
	if !reflect.DeepEqual(args, []any{7}) {
		t.Fatalf("args should be deduped for `@p` dialect: got %v", args)
	}
}

func TestRenderNamed_QMarkDialect_DistinctParams(t *testing.T) {
	got, args, err := RenderNamed("SELECT :a + :b", "?", map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "SELECT ? + ?" {
		t.Fatalf("query mismatch: %q", got)
	}
	if !reflect.DeepEqual(args, []any{1, 2}) {
		t.Fatalf("args mismatch: %v", args)
	}
}

func TestRenderNamed_MissingParam(t *testing.T) {
	if _, _, err := RenderNamed("SELECT :missing", "?", map[string]any{}); err == nil {
		t.Fatalf("expected missing parameter error")
	}
}
