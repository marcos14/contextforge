package drivers

import "context"

// Relation describes one foreign-key relationship between two tables. A single
// FK constraint may span several columns (composite key), so FromColumns and
// ToColumns are positionally-aligned slices.
type Relation struct {
	ConstraintName string   `json:"constraint_name,omitempty"`
	FromSchema     string   `json:"from_schema,omitempty"`
	FromTable      string   `json:"from_table"`
	FromColumns    []string `json:"from_columns"`
	ToSchema       string   `json:"to_schema,omitempty"`
	ToTable        string   `json:"to_table"`
	ToColumns      []string `json:"to_columns"`
}

// IndexInfo describes one existing index. Columns preserves the index column
// order (leading columns matter for index selectivity).
type IndexInfo struct {
	Name    string   `json:"name"`
	Schema  string   `json:"schema,omitempty"`
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// SchemaGraph is the enriched schema: tables plus foreign keys and existing
// indexes. It feeds the Query Studio LLM so it can propose correct JOINs and
// avoid suggesting indexes that already exist.
type SchemaGraph struct {
	Tables    []Table     `json:"tables"`
	Relations []Relation  `json:"relations"`
	Indexes   []IndexInfo `json:"indexes"`
}

// RichIntrospector is an optional capability: drivers that can enumerate FK
// relations and existing indexes implement it. Drivers without support simply
// do not implement the interface; use the package-level IntrospectRich helper
// to invoke it safely (it falls back to a plain Introspect).
type RichIntrospector interface {
	IntrospectRich(ctx context.Context) (*SchemaGraph, error)
}

// IntrospectRich is the single entry point for the rich-introspection
// capability. Drivers that implement RichIntrospector return the full graph;
// drivers that do not fall back to a plain Introspect (Relations/Indexes left
// empty). Callers should always go through this helper.
func IntrospectRich(ctx context.Context, d Driver) (*SchemaGraph, error) {
	if ri, ok := d.(RichIntrospector); ok {
		return ri.IntrospectRich(ctx)
	}
	tables, err := d.Introspect(ctx)
	if err != nil {
		return nil, err
	}
	return &SchemaGraph{Tables: tables}, nil
}

// FKColumn is one column pair of a foreign key. Drivers scan their catalog rows
// into these (already ordered by constraint + ordinal position) and hand them
// to BuildRelations, which groups them into composite Relation entries. Kept
// separate from Relation so the grouping logic is shared and unit-testable.
type FKColumn struct {
	ConstraintName string
	FromSchema     string
	FromTable      string
	FromColumn     string
	ToSchema       string
	ToTable        string
	ToColumn       string
}

// BuildRelations groups ordered FK column rows into composite Relation entries.
// Rows must already be sorted by (from table, constraint, ordinal) so composite
// columns land in the right order. A constraint name is not globally unique
// across tables, so grouping keys on (from schema, from table, constraint).
func BuildRelations(rows []FKColumn) []Relation {
	type key struct{ s, t, c string }
	idx := map[key]int{}
	out := []Relation{}
	for _, row := range rows {
		k := key{row.FromSchema, row.FromTable, row.ConstraintName}
		i, ok := idx[k]
		if !ok {
			out = append(out, Relation{
				ConstraintName: row.ConstraintName,
				FromSchema:     row.FromSchema,
				FromTable:      row.FromTable,
				ToSchema:       row.ToSchema,
				ToTable:        row.ToTable,
			})
			i = len(out) - 1
			idx[k] = i
		}
		out[i].FromColumns = append(out[i].FromColumns, row.FromColumn)
		out[i].ToColumns = append(out[i].ToColumns, row.ToColumn)
	}
	return out
}

// IndexColumn is one column of an index. Drivers scan their catalog rows into
// these (ordered by index + column position) and hand them to BuildIndexes.
type IndexColumn struct {
	Name   string
	Schema string
	Table  string
	Column string
	Unique bool
}

// BuildIndexes groups ordered index column rows into composite IndexInfo
// entries. Rows must already be sorted by (schema, table, index, seq) so index
// columns keep their order. Grouping keys on (schema, table, index name).
func BuildIndexes(rows []IndexColumn) []IndexInfo {
	type key struct{ s, t, n string }
	idx := map[key]int{}
	out := []IndexInfo{}
	for _, row := range rows {
		k := key{row.Schema, row.Table, row.Name}
		i, ok := idx[k]
		if !ok {
			out = append(out, IndexInfo{
				Name:   row.Name,
				Schema: row.Schema,
				Table:  row.Table,
				Unique: row.Unique,
			})
			i = len(out) - 1
			idx[k] = i
		}
		out[i].Columns = append(out[i].Columns, row.Column)
	}
	return out
}
