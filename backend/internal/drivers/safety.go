package drivers

import (
	"fmt"
	"regexp"
	"strings"
)

// destructiveKeywords are top-level statements that are forbidden by default.
// Tools may opt into write semantics later (out of MVP scope).
var destructiveKeywords = []string{
	"DROP", "DELETE", "UPDATE", "TRUNCATE", "ALTER",
	"GRANT", "REVOKE", "CREATE", "INSERT", "MERGE",
	"REPLACE", "RENAME", "VACUUM", "ATTACH", "DETACH",
	"CALL", "EXEC", "EXECUTE",
}

// commentStripper removes /* ... */ and -- ... line comments.
var (
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineComment  = regexp.MustCompile(`--[^\n]*`)
	multiStmt    = regexp.MustCompile(`;\s*\S`)
)

// EnforceReadOnly validates that the SQL contains no destructive top-level
// keywords and no multiple statements. It performs lightweight lexical
// analysis (comments stripped, then keyword scan); proper parsers should be
// preferred per dialect when available.
//
// Returns nil when the SQL is safe to execute under a read-only contract.
func EnforceReadOnly(sql string) error {
	clean := blockComment.ReplaceAllString(sql, " ")
	clean = lineComment.ReplaceAllString(clean, " ")
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return fmt.Errorf("empty query")
	}
	if multiStmt.MatchString(clean) {
		return fmt.Errorf("multiple statements are not allowed")
	}
	upper := strings.ToUpper(clean)
	// Word-boundary keyword scan.
	for _, kw := range destructiveKeywords {
		re := regexp.MustCompile(`\b` + kw + `\b`)
		if re.MatchString(upper) {
			return fmt.Errorf("forbidden keyword %q in query", kw)
		}
	}
	// First token must be SELECT or WITH (CTEs).
	first := strings.Fields(upper)[0]
	if first != "SELECT" && first != "WITH" && first != "SHOW" && first != "EXPLAIN" {
		return fmt.Errorf("only SELECT/WITH/SHOW/EXPLAIN queries are allowed, got %q", first)
	}
	return nil
}

// namedParamRe matches :ident placeholders, ignoring ::cast tokens and
// occurrences inside single-quoted strings (handled by the caller via
// SplitQuoted).
var namedParamRe = regexp.MustCompile(`(^|[^:]):([a-zA-Z_][a-zA-Z0-9_]*)`)

// RenderNamed converts ":name" placeholders to dialect-specific positional
// markers and returns the rewritten query and the ordered args array.
//
//   dialect:
//     "$"   -> $1, $2... (PostgreSQL)
//     "?"   -> ?, ?      (MySQL, MSSQL via go-mssqldb supports @p1 too)
//     "@p"  -> @p1, @p2  (MSSQL named)
//
// Unknown params return an error. Missing params in the map return an error.
func RenderNamed(query, dialect string, params map[string]any) (string, []any, error) {
	var (
		out  strings.Builder
		args []any
		seen = map[string]int{} // name -> 1-based index for reuse
	)
	idx := 0
	last := 0
	matches := namedParamRe.FindAllStringSubmatchIndex(query, -1)
	for _, m := range matches {
		// m[0..1] = whole match, m[2..3] = lead char, m[4..5] = name
		name := query[m[4]:m[5]]
		val, ok := params[name]
		if !ok {
			return "", nil, fmt.Errorf("missing parameter %q", name)
		}
		out.WriteString(query[last : m[2]+1]) // include the lead char
		pos, exists := seen[name]
		if !exists {
			args = append(args, val)
			idx++
			pos = idx
			seen[name] = pos
		}
		switch dialect {
		case "$":
			fmt.Fprintf(&out, "$%d", pos)
		case "?":
			out.WriteByte('?')
		case "@p":
			fmt.Fprintf(&out, "@p%d", pos)
		default:
			return "", nil, fmt.Errorf("unsupported dialect %q", dialect)
		}
		last = m[5]
	}
	out.WriteString(query[last:])
	return out.String(), args, nil
}
