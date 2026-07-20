// Package index is pj's machine-wide SQLite read model: one derived, rebuildable
// store under XDG state (never inside a scope, never synced) that materializes
// every registered scope's projects, their depends/related edges, and a single
// FTS5 corpus. It is opened with WAL mode and a fixed busy_timeout on every
// connection, namespaces rows by a scope column so cross-scope queries and search
// are one statement, and treats any schema-version mismatch or corruption as a
// full drop-and-rebuild rather than a migration.
//
// Authority stays in the files: callers write the file first, then upsert the row
// (write-through), and reconcile catches direct edits via mtime. This package owns
// the schema, the open/rebuild lifecycle, the write-through mutators, the read
// queries the verbs run, and the read-only guard for pj query. It performs SQLite
// I/O only; deciding what to index from a file is the reconcile package's job.
package index

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver, FTS5 compiled in; no cgo
)

// DBName is the fixed index filename under the XDG state dir.
const DBName = "index.db"

// busyTimeoutMS is the connection busy_timeout, locked by design (not a user
// knob): a contending CLI reconcile+write waits this long on the single-writer
// lock before failing with a database-busy error rather than hanging an agent.
const busyTimeoutMS = 5000

// DB is an open handle to the machine-wide index.
type DB struct {
	sql  *sql.DB
	path string
	// LocalDiskWarning is a non-empty human message when the DB's parent directory
	// was detected as a non-local (network/synced) filesystem, where WAL is unsafe.
	// It is a hard warning the CLI surfaces once; the store still opens.
	LocalDiskWarning string
}

// Open opens (creating if absent) the index at <stateDir>/index.db, applying WAL
// and busy_timeout on the connection, and ensures the schema is current: a fresh
// DB is created, and a version mismatch or unreadable/corrupt store is rebuilt
// wholesale. It also runs the local-disk guard on the parent directory and records
// any warning on the returned handle.
func Open(stateDir string) (*DB, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create XDG state directory %s: %w", stateDir, err)
	}
	path := filepath.Join(stateDir, DBName)

	db, err := openAt(path)
	if err != nil {
		return nil, err
	}
	db.LocalDiskWarning = localDiskWarning(stateDir)

	if err := db.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// openAt opens the SQLite file with the pragmas that must hold on every
// connection. MaxOpenConns is pinned to 1: a CLI is a single process with one
// intentional writer, so serializing on the pool sidesteps in-process lock
// contention entirely while WAL still serves any external reader.
func openAt(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(off)",
		path, busyTimeoutMS)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open index %s: %w", path, err)
	}
	sqldb.SetMaxOpenConns(1)
	if err := sqldb.Ping(); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("open index %s: %w", path, err)
	}
	return &DB{sql: sqldb, path: path}, nil
}

// Close closes the underlying handle.
func (d *DB) Close() error {
	if d == nil || d.sql == nil {
		return nil
	}
	err := d.sql.Close()
	d.sql = nil
	return err
}

// ensureSchema brings a just-opened DB to the current schema. A fresh or
// version-mismatched or unreadable store is rebuilt from scratch; a current one is
// left as is. Rebuild is always safe because the store is derived.
func (d *DB) ensureSchema() error {
	ver, ok, err := d.readSchemaVersion()
	if err != nil {
		// The meta table is unreadable or the DB is corrupt: rebuild rather than
		// fail — the files remain authoritative and reconcile repopulates.
		return d.rebuildSchema()
	}
	if !ok || ver != SchemaVersion {
		return d.rebuildSchema()
	}
	return nil
}

// readSchemaVersion reads meta.schema_version. ok is false when the DB is fresh
// (no meta table yet). A malformed value is an error, driving a rebuild.
func (d *DB) readSchemaVersion() (int, bool, error) {
	var exists string
	err := d.sql.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var ver int
	err = d.sql.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&ver)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return ver, true, nil
}

// rebuildSchema drops every known object and recreates the schema at the current
// version. It is the full-rebuild trigger's mechanism; reconcile repopulates rows
// afterwards from the files.
func (d *DB) rebuildSchema() error {
	drop := `
DROP TABLE IF EXISTS fts;
DROP TABLE IF EXISTS edges;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS scope_meta;
DROP TABLE IF EXISTS config_cache;
DROP TABLE IF EXISTS meta;
`
	if _, err := d.sql.Exec(drop); err != nil {
		return fmt.Errorf("reset index schema: %w", err)
	}
	if _, err := d.sql.Exec(schemaSQL); err != nil {
		return fmt.Errorf("create index schema: %w", err)
	}
	if _, err := d.sql.Exec(`INSERT INTO meta(key, value) VALUES ('schema_version', ?)`, SchemaVersion); err != nil {
		return fmt.Errorf("stamp schema version: %w", err)
	}
	return nil
}

// Rebuild drops and recreates the schema, discarding every row. Callers follow it
// with a full reconcile to repopulate. It is the doctor --reindex / corruption
// path, exposed so the reconcile layer can force a clean slate.
func (d *DB) Rebuild() error {
	return d.rebuildSchema()
}
