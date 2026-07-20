package index

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// RowStat is the minimal per-file record reconcile needs to decide whether a file
// changed since it was last indexed, without materializing the whole row.
type RowStat struct {
	ID      string
	MtimeNS int64
	Size    int64
}

// ScopeRows returns the (mtime, size, id) of every indexed file in a scope, keyed
// by absolute path. Reconcile diffs this against the current on-disk stat set to
// find changed, new, and vanished files.
func (d *DB) ScopeRows(scope string) (map[string]RowStat, error) {
	rows, err := d.sql.Query(`SELECT path, id, mtime_ns, size FROM projects WHERE scope = ?`, scope)
	if err != nil {
		return nil, fmt.Errorf("read scope rows for %q: %w", scope, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]RowStat{}
	for rows.Next() {
		var path string
		var rs RowStat
		if err := rows.Scan(&path, &rs.ID, &rs.MtimeNS, &rs.Size); err != nil {
			return nil, err
		}
		out[path] = rs
	}
	return out, rows.Err()
}

// UpsertProject writes one project row (keyed by path) and refreshes its FTS
// entry with NO edges — it is the edge-less form of the write-through unit,
// equivalent to UpsertProjectWithEdges(p, nil). A caller that has depends/related
// edges to record MUST use UpsertProjectWithEdges instead: this form clears the
// file's existing edges and writes none back, so routing an edge-bearing project
// through here would silently drop its edges until the next reconcile.
func (d *DB) UpsertProject(p *Project) error {
	return d.UpsertProjectWithEdges(p, nil)
}

// upsertProjectTx performs the project/edges/fts write inside an existing
// transaction, so one file's row, edges, and search text never disagree. That
// per-file agreement is the whole atomicity unit: reconcile calls one
// UpsertProject* per changed file, each its own transaction, and does not wrap a
// scope's files together — the index is derived and rebuildable, so a crash mid-
// scope self-heals on the next reconcile rather than needing scope-wide atomicity.
func upsertProjectTx(tx *sql.Tx, p *Project) error {
	tagsJSON, err := marshalStrings(p.Tags)
	if err != nil {
		return err
	}
	conflictJSON, err := marshalStrings(p.StatusConflict)
	if err != nil {
		return err
	}
	customJSON, err := marshalCustom(p.Custom)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
INSERT INTO projects (path, scope, id, short_id, status, order_key, title, summary, created,
                      tags, custom, status_conflict, archived, parse_error, parse_msg, schema_error, mtime_ns, size)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
    scope=excluded.scope, id=excluded.id, short_id=excluded.short_id, status=excluded.status,
    order_key=excluded.order_key, title=excluded.title, summary=excluded.summary, created=excluded.created,
    tags=excluded.tags, custom=excluded.custom, status_conflict=excluded.status_conflict,
    archived=excluded.archived, parse_error=excluded.parse_error, parse_msg=excluded.parse_msg,
    schema_error=excluded.schema_error, mtime_ns=excluded.mtime_ns, size=excluded.size`,
		p.Path, p.Scope, p.ID, p.ShortID, p.Status, p.OrderKey, p.Title, p.Summary, p.Created,
		tagsJSON, customJSON, conflictJSON, boolToInt(p.Archived), boolToInt(p.ParseError),
		p.ParseMsg, boolToInt(p.SchemaError), p.MtimeNS, p.Size)
	if err != nil {
		return fmt.Errorf("upsert project %s: %w", p.Path, err)
	}

	var rowid int64
	if err := tx.QueryRow(`SELECT rowid FROM projects WHERE path = ?`, p.Path).Scan(&rowid); err != nil {
		return fmt.Errorf("resolve rowid for %s: %w", p.Path, err)
	}

	if _, err := tx.Exec(`DELETE FROM fts WHERE rowid = ?`, rowid); err != nil {
		return fmt.Errorf("clear fts for %s: %w", p.Path, err)
	}
	if _, err := tx.Exec(`INSERT INTO fts(rowid, title, body) VALUES (?, ?, ?)`, rowid, p.Title, string(p.Body)); err != nil {
		return fmt.Errorf("index fts for %s: %w", p.Path, err)
	}

	if _, err := tx.Exec(`DELETE FROM edges WHERE from_path = ?`, p.Path); err != nil {
		return fmt.Errorf("clear edges for %s: %w", p.Path, err)
	}
	return nil
}

// UpsertProjectWithEdges upserts a project and its full edge list in one
// transaction. Reconcile computes edges alongside the row, so they land together.
func (d *DB) UpsertProjectWithEdges(p *Project, edges []Edge) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := upsertProjectTx(tx, p); err != nil {
		return err
	}
	for _, e := range edges {
		if _, err := tx.Exec(`INSERT INTO edges(from_path, from_id, from_scope, to_id, to_scope, kind) VALUES (?, ?, ?, ?, ?, ?)`,
			e.FromPath, e.FromID, e.FromScope, e.ToID, e.ToScope, e.Kind); err != nil {
			return fmt.Errorf("insert edge %s->%s: %w", e.FromID, e.ToID, err)
		}
	}
	return tx.Commit()
}

// DeleteByPath removes the row, its edges, and its FTS entry for a file that has
// vanished from disk.
func (d *DB) DeleteByPath(path string) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var rowid int64
	err = tx.QueryRow(`SELECT rowid FROM projects WHERE path = ?`, path).Scan(&rowid)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM fts WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM edges WHERE from_path = ?`, path); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE path = ?`, path); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteScope drops every trace of a scope: its project rows (and their FTS/edges),
// its reconcile timestamp, and its cached config. It is how a forgotten scope's
// rows are pruned on the next reconcile.
func (d *DB) DeleteScope(scope string) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM fts WHERE rowid IN (SELECT rowid FROM projects WHERE scope = ?)`, scope); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM edges WHERE from_scope = ?`, scope); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE scope = ?`, scope); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scope_meta WHERE scope = ?`, scope); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM config_cache WHERE scope = ?`, scope); err != nil {
		return err
	}
	return tx.Commit()
}

// IndexedScopes returns the set of scopes that currently have rows, so reconcile
// can prune any that are no longer registered.
func (d *DB) IndexedScopes() (map[string]bool, error) {
	rows, err := d.sql.Query(`SELECT DISTINCT scope FROM projects
                              UNION SELECT scope FROM scope_meta
                              UNION SELECT scope FROM config_cache`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out[s] = true
	}
	return out, rows.Err()
}

// LastIndex returns the scope's last reconcile timestamp (unix nanoseconds), or 0
// when the scope was never reconciled.
func (d *DB) LastIndex(scope string) (int64, error) {
	var ns int64
	err := d.sql.QueryRow(`SELECT last_index FROM scope_meta WHERE scope = ?`, scope).Scan(&ns)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return ns, err
}

// SetLastIndex records the scope's reconcile timestamp.
func (d *DB) SetLastIndex(scope string, ns int64) error {
	_, err := d.sql.Exec(`INSERT INTO scope_meta(scope, last_index) VALUES (?, ?)
                          ON CONFLICT(scope) DO UPDATE SET last_index = excluded.last_index`, scope, ns)
	return err
}

// ConfigCacheEntry is a cached pj.cue evaluation: the serialized closure stats it
// is valid for, the serialized schema (empty when the config was unusable), and the
// config error reason (empty when usable). Caching the negative avoids re-evaluating
// a config that is still broken and unchanged.
type ConfigCacheEntry struct {
	ClosureJSON string
	SchemaJSON  string
	ConfigError string
}

// ConfigCacheGet returns the cached config evaluation for a scope, if any.
func (d *DB) ConfigCacheGet(scope string) (ConfigCacheEntry, bool, error) {
	var e ConfigCacheEntry
	err := d.sql.QueryRow(`SELECT closure_json, schema_json, config_error FROM config_cache WHERE scope = ?`, scope).
		Scan(&e.ClosureJSON, &e.SchemaJSON, &e.ConfigError)
	if errors.Is(err, sql.ErrNoRows) {
		return ConfigCacheEntry{}, false, nil
	}
	if err != nil {
		return ConfigCacheEntry{}, false, err
	}
	return e, true, nil
}

// ConfigCacheSet stores a scope's config evaluation keyed by its closure.
func (d *DB) ConfigCacheSet(scope string, e ConfigCacheEntry) error {
	_, err := d.sql.Exec(`INSERT INTO config_cache(scope, closure_json, schema_json, config_error) VALUES (?, ?, ?, ?)
                          ON CONFLICT(scope) DO UPDATE SET closure_json=excluded.closure_json,
                              schema_json=excluded.schema_json, config_error=excluded.config_error`,
		scope, e.ClosureJSON, e.SchemaJSON, e.ConfigError)
	return err
}

func marshalStrings(v []string) (string, error) {
	if len(v) == 0 {
		return "", nil
	}
	b, err := json.Marshal(v)
	return string(b), err
}

func marshalCustom(v map[string]any) (string, error) {
	if len(v) == 0 {
		return "", nil
	}
	b, err := json.Marshal(v)
	return string(b), err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
