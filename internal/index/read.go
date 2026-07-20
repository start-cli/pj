package index

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sqlite "modernc.org/sqlite"
)

// ErrSearchQuery marks a malformed full-text search query: the MATCH expression
// could not be parsed by FTS5 (an unbalanced quote, a bare operator). It is the one
// user-input error Search can produce — its SQL is static and its only variable
// input is the bound MATCH term — so callers map it to a clean message while every
// other failure stays infrastructure.
var ErrSearchQuery = errors.New("malformed full-text search query")

// sqliteError is SQLite's generic SQLITE_ERROR result code (a frozen public ABI
// value). Within Search — static SQL, one bound MATCH param — it can only mean the
// query expression was malformed; real faults (BUSY, CORRUPT, IOERR) use other codes.
const sqliteError = 1

// projectColumns is the fixed column list every project scan reads, in the order
// scanProject expects. Body is never selected — it is not stored as a column.
const projectColumns = `path, scope, id, short_id, status, order_key, title, summary, created,
    tags, custom, status_conflict, archived, parse_error, parse_msg, schema_error, mtime_ns, size`

// scanProject reads one project row from a *sql.Rows / *sql.Row positioned on a
// projectColumns select.
func scanProject(sc interface{ Scan(...any) error }) (*Project, error) {
	var (
		p                      Project
		tags, custom, conflict string
		archived, perr, serr   int
	)
	if err := sc.Scan(&p.Path, &p.Scope, &p.ID, &p.ShortID, &p.Status, &p.OrderKey, &p.Title, &p.Summary,
		&p.Created, &tags, &custom, &conflict, &archived, &perr, &p.ParseMsg, &serr, &p.MtimeNS, &p.Size); err != nil {
		return nil, err
	}
	p.Archived = archived != 0
	p.ParseError = perr != 0
	p.SchemaError = serr != 0
	if err := unmarshalStrings(tags, &p.Tags); err != nil {
		return nil, err
	}
	if err := unmarshalStrings(conflict, &p.StatusConflict); err != nil {
		return nil, err
	}
	if custom != "" {
		if err := json.Unmarshal([]byte(custom), &p.Custom); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

// AllProjects returns every project row machine-wide. Callers that need a cross-
// scope id map (the depends gate, deps resolution) build it from this.
func (d *DB) AllProjects() ([]*Project, error) {
	return d.queryProjects(`SELECT ` + projectColumns + ` FROM projects`)
}

// ScopeProjects returns every project row in one scope.
func (d *DB) ScopeProjects(scope string) ([]*Project, error) {
	return d.queryProjects(`SELECT `+projectColumns+` FROM projects WHERE scope = ?`, scope)
}

// ProjectsByID returns every row in a scope whose full id matches — usually one,
// but two or more under a duplicate_id collision (the caller refuses those).
func (d *DB) ProjectsByID(scope, id string) ([]*Project, error) {
	return d.queryProjects(`SELECT `+projectColumns+` FROM projects WHERE scope = ? AND id = ?`, scope, id)
}

// ProjectsByShortID returns every row in a scope whose short id matches.
func (d *DB) ProjectsByShortID(scope, shortID string) ([]*Project, error) {
	return d.queryProjects(`SELECT `+projectColumns+` FROM projects WHERE scope = ? AND short_id = ?`, scope, shortID)
}

// ProjectsByFullID returns every row machine-wide whose full id matches, across
// any scope (a full id addresses any registered scope).
func (d *DB) ProjectsByFullID(id string) ([]*Project, error) {
	return d.queryProjects(`SELECT `+projectColumns+` FROM projects WHERE id = ?`, id)
}

func (d *DB) queryProjects(q string, args ...any) ([]*Project, error) {
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SearchHit is one FTS result: the project row plus its corpus-relative bm25 score
// (smaller is a better match, SQLite's convention).
type SearchHit struct {
	Project *Project
	Score   float64
}

// Search runs an FTS5 MATCH over titles and bodies, ranked bm25 best-first and
// tie-broken by full id ascending. An empty scope searches machine-wide; a
// non-empty scope bounds to that scope. It returns the matched project rows,
// including archived terminals and parse_error quarantine rows.
func (d *DB) Search(scope, match string) ([]SearchHit, error) {
	q := `SELECT ` + prefixed("p.", projectColumns) + `, bm25(fts) AS score
          FROM fts JOIN projects p ON p.rowid = fts.rowid
          WHERE fts MATCH ?`
	args := []any{match}
	if scope != "" {
		q += ` AND p.scope = ?`
		args = append(args, scope)
	}
	q += ` ORDER BY score ASC, p.id ASC`

	rows, err := d.sql.Query(q, args...)
	if err != nil {
		if isQuerySyntaxErr(err) {
			return nil, fmt.Errorf("%w: %w", ErrSearchQuery, err)
		}
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SearchHit
	for rows.Next() {
		hit, err := scanSearchHit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

// isQuerySyntaxErr reports whether err is a malformed-query error from FTS5. Search's
// SQL is static and its only variable input is the bound MATCH term, so a generic
// SQLITE_ERROR here is a query-parse failure, not an infrastructure fault.
func isQuerySyntaxErr(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqliteError
}

func scanSearchHit(rows *sql.Rows) (SearchHit, error) {
	var (
		p                      Project
		tags, custom, conflict string
		archived, perr, serr   int
		score                  float64
	)
	if err := rows.Scan(&p.Path, &p.Scope, &p.ID, &p.ShortID, &p.Status, &p.OrderKey, &p.Title, &p.Summary,
		&p.Created, &tags, &custom, &conflict, &archived, &perr, &p.ParseMsg, &serr, &p.MtimeNS, &p.Size, &score); err != nil {
		return SearchHit{}, err
	}
	p.Archived = archived != 0
	p.ParseError = perr != 0
	p.SchemaError = serr != 0
	if err := unmarshalStrings(tags, &p.Tags); err != nil {
		return SearchHit{}, err
	}
	if err := unmarshalStrings(conflict, &p.StatusConflict); err != nil {
		return SearchHit{}, err
	}
	if custom != "" {
		if err := json.Unmarshal([]byte(custom), &p.Custom); err != nil {
			return SearchHit{}, err
		}
	}
	return SearchHit{Project: &p, Score: score}, nil
}

// AllEdges returns every edge machine-wide. deps and traversal build their
// adjacency from this.
func (d *DB) AllEdges() ([]Edge, error) {
	return d.queryEdges(`SELECT from_path, from_id, from_scope, to_id, to_scope, kind FROM edges`)
}

func (d *DB) queryEdges(q string, args ...any) ([]Edge, error) {
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.FromPath, &e.FromID, &e.FromScope, &e.ToID, &e.ToScope, &e.Kind); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func unmarshalStrings(s string, dst *[]string) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), dst)
}

// prefixed rewrites a comma-separated column list to prefix each bare column with
// alias (e.g. "p."), leaving the multi-line formatting intact. It keeps the one
// projectColumns definition authoritative for joined selects.
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		trimmed := strings.TrimSpace(c)
		lead := c[:len(c)-len(strings.TrimLeft(c, " \t\n"))]
		parts[i] = lead + alias + trimmed
	}
	return strings.Join(parts, ",")
}
