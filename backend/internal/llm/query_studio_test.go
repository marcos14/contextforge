package llm

import (
	"strings"
	"testing"

	"github.com/marcos14/contextforge/backend/internal/drivers"
)

func TestParseQueryStudioOutput_FullProposal(t *testing.T) {
	raw := `{
		"reply": "Aqui está a consulta de faturamento por cliente.",
		"query": "SELECT c.id, c.name, SUM(o.total) AS revenue FROM orders o JOIN customers c ON c.id = o.customer_id WHERE o.created_at >= :from AND o.created_at < :to GROUP BY c.id, c.name",
		"explanation": "Soma o total dos pedidos por cliente no período informado.",
		"suggested_indexes": ["CREATE INDEX idx_orders_customer_created ON orders (customer_id, created_at)"],
		"performance_notes": ["Evita SELECT *", "Filtro por range em created_at preserva o uso do índice"],
		"assumptions": ["orders.total já está no total do pedido"]
	}`

	out, err := parseQueryStudioOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Reply == "" {
		t.Fatal("reply should be populated")
	}
	if !strings.Contains(out.Query, "SELECT c.id") {
		t.Fatalf("query not parsed: %q", out.Query)
	}
	if out.Explanation == "" {
		t.Fatal("explanation should be populated")
	}
	if len(out.SuggestedIndexes) != 1 || !strings.HasPrefix(out.SuggestedIndexes[0], "CREATE INDEX") {
		t.Fatalf("suggested_indexes not parsed: %v", out.SuggestedIndexes)
	}
	if len(out.PerformanceNotes) != 2 {
		t.Fatalf("performance_notes not parsed: %v", out.PerformanceNotes)
	}
	if len(out.Assumptions) != 1 {
		t.Fatalf("assumptions not parsed: %v", out.Assumptions)
	}
	if out.Raw != raw {
		t.Fatal("raw should preserve the original response")
	}
}

func TestParseQueryStudioOutput_ReplyOnly(t *testing.T) {
	// While gathering information the model may reply without a query.
	raw := `{"reply": "Qual período você quer analisar?"}`
	out, err := parseQueryStudioOutput(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Query != "" || out.Explanation != "" {
		t.Fatalf("expected empty query/explanation, got %q / %q", out.Query, out.Explanation)
	}
	if len(out.SuggestedIndexes) != 0 || len(out.PerformanceNotes) != 0 || len(out.Assumptions) != 0 {
		t.Fatal("expected empty performance fields")
	}
}

func TestParseQueryStudioOutput_MalformedJSON(t *testing.T) {
	for _, raw := range []string{
		`{"reply": "oops"`,       // truncated
		`not json at all`,        // garbage
		`{"reply": 123}`,         // wrong type
	} {
		if _, err := parseQueryStudioOutput(raw); err == nil {
			t.Fatalf("expected error for malformed input %q", raw)
		}
	}
}

func TestParseQueryStudioOutput_EmptyReplyRejected(t *testing.T) {
	if _, err := parseQueryStudioOutput(`{"reply": "", "query": "SELECT 1"}`); err == nil {
		t.Fatal("expected error when reply is empty")
	}
}

func TestBuildQueryStudioContext_RelationsAndIndexes(t *testing.T) {
	ctx := buildQueryStudioContext(QueryStudioInput{
		ConnectionKind: "pg",
		Tables: []drivers.Table{
			{Schema: "public", Name: "orders", Columns: []drivers.Column{{Name: "id", Type: "int8"}}},
		},
		Relations: []drivers.Relation{
			{ConstraintName: "fk_oc", FromSchema: "public", FromTable: "orders", FromColumns: []string{"customer_id"},
				ToSchema: "public", ToTable: "customers", ToColumns: []string{"id"}},
		},
		Indexes: []drivers.IndexInfo{
			{Name: "idx_orders_customer", Schema: "public", Table: "orders", Columns: []string{"customer_id"}},
		},
	})
	if !strings.Contains(ctx, "Foreign keys") {
		t.Fatalf("context missing FK section:\n%s", ctx)
	}
	if !strings.Contains(ctx, "public.orders(customer_id) -> public.customers(id)") {
		t.Fatalf("FK not rendered as expected:\n%s", ctx)
	}
	if !strings.Contains(ctx, "Existing indexes") || !strings.Contains(ctx, "idx_orders_customer on public.orders (customer_id)") {
		t.Fatalf("index not rendered as expected:\n%s", ctx)
	}
}

func TestBuildQueryStudioContext_FirebirdOmitsSchema(t *testing.T) {
	// Firebird has no schemas: FK/index references must be bare table names.
	ctx := buildQueryStudioContext(QueryStudioInput{
		ConnectionKind: "firebird",
		Relations: []drivers.Relation{
			{ConstraintName: "fk", FromSchema: "PUBLIC", FromTable: "CONTAS", FromColumns: []string{"CLIENTE_ID"},
				ToSchema: "PUBLIC", ToTable: "CLIENTES", ToColumns: []string{"ID"}},
		},
	})
	if strings.Contains(ctx, "PUBLIC.CONTAS") || strings.Contains(ctx, "PUBLIC.CLIENTES") {
		t.Fatalf("firebird context must not qualify tables with a schema:\n%s", ctx)
	}
	if !strings.Contains(ctx, "CONTAS(CLIENTE_ID) -> CLIENTES(ID)") {
		t.Fatalf("firebird FK not rendered bare:\n%s", ctx)
	}
}

func TestBuildQueryStudioContext_OmitsEmptySections(t *testing.T) {
	ctx := buildQueryStudioContext(QueryStudioInput{ConnectionKind: "pg"})
	if strings.Contains(ctx, "Foreign keys") || strings.Contains(ctx, "Existing indexes") {
		t.Fatalf("empty relations/indexes should not render a section:\n%s", ctx)
	}
}

func TestQueryStudioDialectGuidance(t *testing.T) {
	// Every supported SQL dialect must get tailored guidance mentioning EXPLAIN.
	for _, kind := range []string{"pg", "mysql", "mssql", "oracle", "firebird"} {
		g := queryStudioDialectGuidance(kind)
		if g == "" {
			t.Fatalf("expected guidance for dialect %q", kind)
		}
		up := strings.ToUpper(g)
		if !strings.Contains(up, "EXPLAIN") &&
			!strings.Contains(up, "SET PLAN") &&
			!strings.Contains(up, "DBMS_XPLAN") &&
			!strings.Contains(up, "SHOWPLAN") {
			t.Fatalf("dialect %q guidance should mention plan inspection: %s", kind, g)
		}
	}
	// Non-SQL / unknown dialects get no special guidance.
	if g := queryStudioDialectGuidance("mongo"); g != "" {
		t.Fatalf("expected no guidance for mongo, got %q", g)
	}
}
