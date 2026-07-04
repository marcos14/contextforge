package drivers

import (
	"context"
	"reflect"
	"testing"
)

func TestBuildRelations_SingleColumn(t *testing.T) {
	got := BuildRelations([]FKColumn{
		{ConstraintName: "fk_order_customer", FromSchema: "public", FromTable: "orders", FromColumn: "customer_id",
			ToSchema: "public", ToTable: "customers", ToColumn: "id"},
	})
	want := []Relation{{
		ConstraintName: "fk_order_customer",
		FromSchema:     "public", FromTable: "orders", FromColumns: []string{"customer_id"},
		ToSchema: "public", ToTable: "customers", ToColumns: []string{"id"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestBuildRelations_CompositePreservesOrder(t *testing.T) {
	// Two-column composite FK arriving in ordinal order.
	got := BuildRelations([]FKColumn{
		{ConstraintName: "fk_item", FromSchema: "s", FromTable: "line_items", FromColumn: "order_id",
			ToSchema: "s", ToTable: "orders", ToColumn: "id"},
		{ConstraintName: "fk_item", FromSchema: "s", FromTable: "line_items", FromColumn: "tenant_id",
			ToSchema: "s", ToTable: "orders", ToColumn: "tenant_id"},
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 grouped relation, got %d", len(got))
	}
	if !reflect.DeepEqual(got[0].FromColumns, []string{"order_id", "tenant_id"}) {
		t.Fatalf("from columns out of order: %v", got[0].FromColumns)
	}
	if !reflect.DeepEqual(got[0].ToColumns, []string{"id", "tenant_id"}) {
		t.Fatalf("to columns out of order: %v", got[0].ToColumns)
	}
}

func TestBuildRelations_SameConstraintNameDistinctTables(t *testing.T) {
	// A constraint name is not globally unique across tables; grouping keys on
	// (from schema, from table, constraint) so these stay separate.
	got := BuildRelations([]FKColumn{
		{ConstraintName: "fk_parent", FromTable: "a", FromColumn: "pid", ToTable: "p", ToColumn: "id"},
		{ConstraintName: "fk_parent", FromTable: "b", FromColumn: "pid", ToTable: "p", ToColumn: "id"},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 relations for distinct tables, got %d: %+v", len(got), got)
	}
}

func TestBuildIndexes_UniqueAndComposite(t *testing.T) {
	got := BuildIndexes([]IndexColumn{
		{Name: "pk_users", Schema: "public", Table: "users", Column: "id", Unique: true},
		{Name: "idx_name_email", Schema: "public", Table: "users", Column: "name", Unique: false},
		{Name: "idx_name_email", Schema: "public", Table: "users", Column: "email", Unique: false},
	})
	want := []IndexInfo{
		{Name: "pk_users", Schema: "public", Table: "users", Columns: []string{"id"}, Unique: true},
		{Name: "idx_name_email", Schema: "public", Table: "users", Columns: []string{"name", "email"}, Unique: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestBuildIndexes_Empty(t *testing.T) {
	if got := BuildIndexes(nil); len(got) != 0 {
		t.Fatalf("expected empty slice, got %+v", got)
	}
}

// richDriver embeds baseDriver (defined in explain_test.go) and implements
// RichIntrospector.
type richDriver struct {
	baseDriver
	called bool
}

func (r *richDriver) IntrospectRich(context.Context) (*SchemaGraph, error) {
	r.called = true
	return &SchemaGraph{
		Tables:    []Table{{Name: "t"}},
		Relations: []Relation{{ConstraintName: "fk", FromTable: "t", FromColumns: []string{"c"}, ToTable: "u", ToColumns: []string{"id"}}},
		Indexes:   []IndexInfo{{Name: "i", Table: "t", Columns: []string{"c"}}},
	}, nil
}

// introspectableDriver reports one table via the plain Introspect path but does
// NOT implement RichIntrospector.
type introspectableDriver struct{ baseDriver }

func (introspectableDriver) Introspect(context.Context) ([]Table, error) {
	return []Table{{Schema: "public", Name: "orders"}}, nil
}

func TestIntrospectRich_Delegates(t *testing.T) {
	d := &richDriver{}
	g, err := IntrospectRich(context.Background(), d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.called {
		t.Fatalf("expected IntrospectRich to be delegated to the driver")
	}
	if len(g.Relations) != 1 || len(g.Indexes) != 1 {
		t.Fatalf("rich graph not returned: %+v", g)
	}
}

func TestIntrospectRich_FallsBackToIntrospect(t *testing.T) {
	g, err := IntrospectRich(context.Background(), introspectableDriver{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Tables) != 1 || g.Tables[0].Name != "orders" {
		t.Fatalf("fallback did not return plain tables: %+v", g.Tables)
	}
	if len(g.Relations) != 0 || len(g.Indexes) != 0 {
		t.Fatalf("fallback should leave relations/indexes empty: %+v", g)
	}
}
