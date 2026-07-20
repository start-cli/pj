package index

import (
	"fmt"
	"sort"
	"strings"
)

// Collision is a group of project rows in one scope that share a key that must be
// unique — a full id (duplicate_id) or an order key (equal_order). Members lists
// the colliding paths, sorted, so the warning is stable and repair can find them.
type Collision struct {
	Scope   string
	Key     string
	Members []string
}

// DuplicateIDs returns, for the given scopes, every full id claimed by two or more
// project files — the cheap post-reconcile aggregate behind the duplicate_id
// warning. It reads only materialized rows; it never re-stats or re-parses.
func (d *DB) DuplicateIDs(scopes []string) ([]Collision, error) {
	return d.collisions(scopes, "id", `1`)
}

// EqualOrders returns, for the given scopes, every order key shared by two or more
// projects (empty keys excluded) — the aggregate behind the equal_order warning.
func (d *DB) EqualOrders(scopes []string) ([]Collision, error) {
	return d.collisions(scopes, "order_key", `order_key <> ''`)
}

// collisions groups a scope's rows by keyCol, keeping only groups of size > 1, and
// returns each group's sorted member paths. extraPred narrows the rows considered.
func (d *DB) collisions(scopes []string, keyCol, extraPred string) ([]Collision, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	placeholders, args := inClause(scopes)
	q := fmt.Sprintf(`SELECT scope, %s AS k, path FROM projects
                      WHERE scope IN (%s) AND %s
                      ORDER BY scope, k, path`, keyCol, placeholders, extraPred)
	rows, err := d.sql.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("collision aggregate: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type key struct{ scope, k string }
	grouped := map[key][]string{}
	var order []key
	for rows.Next() {
		var scope, k, path string
		if err := rows.Scan(&scope, &k, &path); err != nil {
			return nil, err
		}
		kk := key{scope, k}
		if _, seen := grouped[kk]; !seen {
			order = append(order, kk)
		}
		grouped[kk] = append(grouped[kk], path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Collision
	for _, kk := range order {
		members := grouped[kk]
		if len(members) < 2 {
			continue
		}
		sort.Strings(members)
		out = append(out, Collision{Scope: kk.scope, Key: kk.k, Members: members})
	}
	return out, nil
}

// ParseErrorCount returns how many quarantined (parse_error) project rows exist
// across the given scopes — the number behind the terse "N unparseable" warning.
func (d *DB) ParseErrorCount(scopes []string) (int, error) {
	if len(scopes) == 0 {
		return 0, nil
	}
	placeholders, args := inClause(scopes)
	var n int
	err := d.sql.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM projects WHERE parse_error = 1 AND scope IN (%s)`, placeholders), args...).Scan(&n)
	return n, err
}

// DuplicateIDSet returns the full ids (scope-qualified as "<scope>\x00<id>") that
// are in a duplicate_id collision for the given scopes, so next can skip any
// candidate whose id collides. The key is scope+id because a bare id is not
// machine-unique.
func (d *DB) DuplicateIDSet(scopes []string) (map[string]bool, error) {
	cols, err := d.DuplicateIDs(scopes)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, c := range cols {
		out[c.Scope+"\x00"+c.Key] = true
	}
	return out, nil
}

// inClause builds a "?, ?, ?" placeholder run and the matching args slice for a
// stable, sorted set of values.
func inClause(values []string) (string, []any) {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	marks := make([]string, len(sorted))
	args := make([]any, len(sorted))
	for i, v := range sorted {
		marks[i] = "?"
		args[i] = v
	}
	return strings.Join(marks, ", "), args
}
