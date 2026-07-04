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
// Returns the sanitized SQL (trailing semicolons and whitespace stripped) when
// the input is safe to execute under a read-only contract. Callers should pass
// the returned string to the driver-specific renderer (RenderNamed) so the
// cleaned form reaches the database — many drivers (Firebird in particular)
// reject a trailing ";".
func EnforceReadOnly(sql string) (string, error) {
	clean := blockComment.ReplaceAllString(sql, " ")
	clean = lineComment.ReplaceAllString(clean, " ")
	clean = strings.TrimSpace(clean)
	// Strip any trailing ";" plus surrounding whitespace. LLMs frequently
	// emit a closing semicolon by habit; on `?`-dialect drivers (Firebird,
	// MySQL) that trailing token would otherwise be sent to the DB and
	// rejected as a syntax error.
	for strings.HasSuffix(clean, ";") {
		clean = strings.TrimSpace(strings.TrimSuffix(clean, ";"))
	}
	if clean == "" {
		return "", fmt.Errorf("empty query")
	}
	if multiStmt.MatchString(clean) {
		return "", fmt.Errorf("multiple statements are not allowed")
	}
	upper := strings.ToUpper(clean)
	// Word-boundary keyword scan.
	for _, kw := range destructiveKeywords {
		re := regexp.MustCompile(`\b` + kw + `\b`)
		if re.MatchString(upper) {
			return "", fmt.Errorf("forbidden keyword %q in query", kw)
		}
	}
	// First token must be SELECT or WITH (CTEs).
	first := strings.Fields(upper)[0]
	if first != "SELECT" && first != "WITH" && first != "SHOW" && first != "EXPLAIN" {
		return "", fmt.Errorf("only SELECT/WITH/SHOW/EXPLAIN queries are allowed, got %q", first)
	}
	return clean, nil
}

// EnforceSelectOnly is a stricter variant of EnforceReadOnly used by the Query
// Studio module. It accepts ONLY plain SELECT/WITH queries and rejects
// everything else — including SHOW and EXPLAIN — because in that module the
// backend is the one that wraps the query in EXPLAIN. Allowing a raw EXPLAIN or
// SHOW from the caller would defeat that contract and could smuggle in
// DDL/DML behind an EXPLAIN wrapper on some dialects.
//
// It reuses EnforceReadOnly for comment stripping, trailing ";" removal and
// multi-statement / destructive-keyword rejection, then requires the first
// token to be SELECT or WITH. Returns the sanitized SQL when safe.
func EnforceSelectOnly(sql string) (string, error) {
	clean, err := EnforceReadOnly(sql)
	if err != nil {
		return "", err
	}
	first := strings.Fields(strings.ToUpper(clean))[0]
	if first != "SELECT" && first != "WITH" {
		return "", fmt.Errorf("only SELECT/WITH queries are allowed, got %q", first)
	}
	return clean, nil
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
		switch dialect {
		case "$":
			// Postgres supports positional reuse ($1 referenced N times),
			// so a single arg covers every occurrence of :name.
			pos, exists := seen[name]
			if !exists {
				args = append(args, val)
				idx++
				pos = idx
				seen[name] = pos
			}
			fmt.Fprintf(&out, "$%d", pos)
		case "@p":
			// MSSQL @pN also supports reuse.
			pos, exists := seen[name]
			if !exists {
				args = append(args, val)
				idx++
				pos = idx
				seen[name] = pos
			}
			fmt.Fprintf(&out, "@p%d", pos)
		case "?":
			// MySQL/Firebird use anonymous `?` placeholders, which DO NOT
			// support reuse — each `?` consumes the next arg in order. So
			// every occurrence of :name must push its value onto args, even
			// when the name repeats (e.g. "WHERE x = :id OR y = :id").
			args = append(args, val)
			out.WriteByte('?')
		default:
			return "", nil, fmt.Errorf("unsupported dialect %q", dialect)
		}
		last = m[5]
	}
	out.WriteString(query[last:])
	return out.String(), args, nil
}
