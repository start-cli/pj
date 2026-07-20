package index

// SchemaVersion is the on-disk index schema version. Any change to the DDL below
// bumps this constant; a mismatch on open triggers a full drop-and-rebuild rather
// than an ALTER/migration (the store is derived from the files, never authority).
const SchemaVersion = 1

// schemaSQL is the complete DDL for a fresh index. The store is rebuilt wholesale
// on any version mismatch, so there are no migrations — this is the single source
// of the shape. The physical unit is the file path: a project row is keyed by its
// absolute path so two files claiming one id (a duplicate_id collision) are two
// rows, and a file moved between the dir root and archive/ is a delete of the old
// path plus an insert of the new. Rows are namespaced by scope so cross-scope
// queries and the FTS corpus are machine-wide.
//
// The FTS corpus is a plain (non-external-content) FTS5 table whose rowid mirrors
// projects.rowid, kept in sync by the write path. edges carries an extra from_path
// column (not part of the logical shape) so a project's edges delete precisely
// even under a duplicate id.
const schemaSQL = `
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE projects (
    path            TEXT PRIMARY KEY,
    scope           TEXT NOT NULL,
    id              TEXT NOT NULL,
    short_id        TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT '',
    order_key       TEXT NOT NULL DEFAULT '',
    title           TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    created         TEXT NOT NULL DEFAULT '',
    tags            TEXT NOT NULL DEFAULT '',
    custom          TEXT NOT NULL DEFAULT '',
    status_conflict TEXT NOT NULL DEFAULT '',
    archived        INTEGER NOT NULL DEFAULT 0,
    parse_error     INTEGER NOT NULL DEFAULT 0,
    parse_msg       TEXT NOT NULL DEFAULT '',
    schema_error    INTEGER NOT NULL DEFAULT 0,
    mtime_ns        INTEGER NOT NULL DEFAULT 0,
    size            INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_projects_scope_id ON projects(scope, id);
CREATE INDEX idx_projects_scope ON projects(scope);

CREATE TABLE edges (
    from_path  TEXT NOT NULL,
    from_id    TEXT NOT NULL,
    from_scope TEXT NOT NULL,
    to_id      TEXT NOT NULL,
    to_scope   TEXT NOT NULL,
    kind       TEXT NOT NULL
);
CREATE INDEX idx_edges_from ON edges(from_id);
CREATE INDEX idx_edges_to ON edges(to_id);
CREATE INDEX idx_edges_from_path ON edges(from_path);

CREATE VIRTUAL TABLE fts USING fts5(title, body, tokenize = 'porter unicode61');

CREATE TABLE scope_meta (
    scope      TEXT PRIMARY KEY,
    last_index INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE config_cache (
    scope        TEXT PRIMARY KEY,
    closure_json TEXT NOT NULL DEFAULT '',
    schema_json  TEXT NOT NULL DEFAULT '',
    config_error TEXT NOT NULL DEFAULT ''
);
`

// SchemaText is the human-facing description pj query --schema prints. It states
// up front that the schema is derived and not a stable API, then lists the tables
// and their columns. Kept in lockstep with schemaSQL by hand — it is documentation,
// not a contract, and pj query is debug/human-only.
const SchemaText = `pj index schema (version 1)

NOT A STABLE API: the index is a derived cache, rebuilt on any schema_version
bump, and may reshape between releases with no migration. Do not script against
it — agents use pj deps / list / search / next / get / meta instead.

projects(path, scope, id, short_id, status, order_key, title, summary, created,
         tags, custom, status_conflict, archived, parse_error, parse_msg,
         schema_error, mtime_ns, size)
    One row per project file, keyed by absolute path. tags and custom are JSON;
    status_conflict is a JSON array; archived/parse_error/schema_error are 0/1.

edges(from_path, from_id, from_scope, to_id, to_scope, kind)
    One row per depends/related frontmatter entry (full ids only). kind is
    'depends' or 'related'. Cross-scope edges have from_scope != to_scope; a row
    whose to_id matches no project is a dangling edge.

fts(title, body)
    FTS5 corpus; rowid mirrors projects.rowid. Query with MATCH and rank by
    bm25(fts).

scope_meta(scope, last_index)
    Per-scope last reconcile timestamp (unix nanoseconds).

config_cache(scope, closure_json, schema_json, config_error)
    Cached pj.cue evaluation. closure_json records the (path, mtime, size) of every
    file in the config import closure; a change to any invalidates the cached schema.
`
