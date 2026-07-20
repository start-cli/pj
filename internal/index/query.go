package index

import (
	"context"
	"fmt"
	"strings"
)

// QueryResult is the tabular result of a read-only pj query: the column names and
// the rows, each cell rendered to a string for TSV-style printing.
type QueryResult struct {
	Columns []string
	Rows    [][]string
}

// RunReadOnlyQuery executes an ad-hoc SQL statement after proving it is read-only.
// A write, DDL, or a multi-statement batch containing any of those is rejected
// before execution — the index is derived and mutating it cannot change the files,
// so durable change is the file path / pj doctor --repair, not this DB.
//
// The static leadingKeyword classifier is the friendly first line of defence, but
// it cannot see a write smuggled behind a CTE (WITH … DELETE) or a function-form
// pragma, so the statement runs on a connection pinned to PRAGMA query_only = ON:
// SQLite then refuses any write regardless of syntax. That runtime guard is the
// authority; the classifier only shapes the error message.
func (d *DB) RunReadOnlyQuery(sqlText string) (*QueryResult, error) {
	if err := ensureReadOnly(sqlText); err != nil {
		return nil, err
	}

	ctx := context.Background()
	conn, err := d.sql.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		return nil, fmt.Errorf("arm read-only guard: %w", err)
	}
	// Reset before the connection returns to the pool so later writers are not
	// silently frozen out.
	defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA query_only = OFF`) }()

	rows, err := conn.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &QueryResult{Columns: cols}
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out := make([]string, len(cols))
		for i, c := range cells {
			out[i] = renderCell(c)
		}
		res.Rows = append(res.Rows, out)
	}
	return res, rows.Err()
}

// writeVerbs are the leading keywords that mutate the store or its schema. A
// statement (or any statement in a batch) beginning with one is refused.
var writeVerbs = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true,
	"DROP": true, "ALTER": true, "CREATE": true, "TRUNCATE": true,
	"ATTACH": true, "DETACH": true, "REINDEX": true, "VACUUM": true,
	"BEGIN": true, "COMMIT": true, "SAVEPOINT": true, "RELEASE": true,
}

// ensureReadOnly refuses anything that is not a plain SELECT / read-only EXPLAIN /
// read-only PRAGMA. It splits on ';' so a batch smuggling a write after a SELECT is
// caught, and treats a bare PRAGMA assignment (PRAGMA x = y) as a write.
func ensureReadOnly(sqlText string) error {
	statements := splitStatements(sqlText)
	if len(statements) == 0 {
		return fmt.Errorf("empty query")
	}
	for _, stmt := range statements {
		lead := leadingKeyword(stmt)
		switch lead {
		case "SELECT", "WITH", "VALUES":
			// read-only
		case "EXPLAIN":
			// EXPLAIN [QUERY PLAN] <read-only stmt> — inspect only, never mutates.
		case "PRAGMA":
			if strings.ContainsRune(stmt, '=') {
				return readOnlyRefusal("a PRAGMA that sets a value")
			}
		default:
			if writeVerbs[lead] {
				return readOnlyRefusal(strings.ToLower(lead))
			}
			return readOnlyRefusal("a non-read-only statement")
		}
	}
	return nil
}

func readOnlyRefusal(what string) error {
	return fmt.Errorf("pj query is read-only: refusing %s — the index is a derived cache; durable change is the project files or pj doctor --repair, not the DB", what)
}

// splitStatements breaks a batch into statements on ';', dropping empty ones. It
// is literal-aware: a ';' inside a single-quoted string ('a;b') or a double-quoted
// identifier ("a;b") is not a separator, so a legitimate read-only query that
// merely contains ';' (WHERE title LIKE '%;%') is one statement, not a spurious
// batch that fails the read-only check. SQLite's doubled-quote escape (a quote
// written twice inside a quoted region) is handled by the toggle: two adjacent
// quotes flip the state off then on, netting out to still-inside, so an escaped
// quote never opens a phantom separator window.
func splitStatements(sqlText string) []string {
	var out []string
	var b strings.Builder
	var inSingle, inDouble bool
	flush := func() {
		if strings.TrimSpace(b.String()) != "" {
			out = append(out, b.String())
		}
		b.Reset()
	}
	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == ';' && !inSingle && !inDouble:
			flush()
			continue
		}
		b.WriteByte(c)
	}
	flush()
	return out
}

// leadingKeyword returns the uppercased first SQL keyword of a statement, skipping
// leading whitespace and SQL line/block comments.
func leadingKeyword(stmt string) string {
	s := stripLeading(stmt)
	i := 0
	for i < len(s) && (isWordByte(s[i])) {
		i++
	}
	return strings.ToUpper(s[:i])
}

// stripLeading removes leading whitespace and comments so the first real token can
// be read.
func stripLeading(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			if nl := strings.IndexByte(s, '\n'); nl >= 0 {
				s = s[nl+1:]
			} else {
				return ""
			}
		case strings.HasPrefix(s, "/*"):
			if end := strings.Index(s, "*/"); end >= 0 {
				s = s[end+2:]
			} else {
				return ""
			}
		default:
			return s
		}
	}
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func renderCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}
