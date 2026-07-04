package pg

import "testing"

func TestBuildExplainSQL(t *testing.T) {
	const q = "SELECT id FROM users"

	if got, want := buildExplainSQL(q, false), "EXPLAIN (FORMAT JSON) "+q; got != want {
		t.Fatalf("buildExplainSQL(analyze=false):\n got=%q\nwant=%q", got, want)
	}

	if got, want := buildExplainSQL(q, true), "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+q; got != want {
		t.Fatalf("buildExplainSQL(analyze=true):\n got=%q\nwant=%q", got, want)
	}
}
