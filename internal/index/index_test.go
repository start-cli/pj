package index

import (
	"errors"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func proj(scope, id, status, order string) *Project {
	return &Project{
		Path: filepath.Join("/tmp", scope, id+".md"), Scope: scope, ID: scope + "-" + id,
		ShortID: id, Status: status, OrderKey: order, Title: id + " title",
		Body: []byte("body of " + id),
	}
}

func TestOpenStampsVersionAndRebuildsOnMismatch(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ver, ok, err := db.readSchemaVersion()
	if err != nil || !ok || ver != SchemaVersion {
		t.Fatalf("version = %d ok=%v err=%v, want %d", ver, ok, err, SchemaVersion)
	}
	if err := db.UpsertProject(proj("wc", "ab2c", "todo", "a0")); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	// Reopen: same version, row survives (no rebuild).
	db2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	rows, err := db2.ScopeProjects("wc")
	if err != nil || len(rows) != 1 {
		t.Fatalf("after reopen rows = %d err=%v, want 1", len(rows), err)
	}

	// Force a version mismatch → next open rebuilds, dropping rows.
	if _, err := db2.sql.Exec(`UPDATE meta SET value = 999 WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := db2.ensureSchema(); err != nil {
		t.Fatal(err)
	}
	rows, _ = db2.ScopeProjects("wc")
	if len(rows) != 0 {
		t.Fatalf("rebuild should drop rows, got %d", len(rows))
	}
}

func TestUpsertReplacesRowAndEdges(t *testing.T) {
	db := openTemp(t)
	p := proj("wc", "ab2c", "todo", "a0")
	edges := []Edge{{FromPath: p.Path, FromID: "wc-ab2c", FromScope: "wc", ToID: "wc-de34", ToScope: "wc", Kind: EdgeDepends}}
	if err := db.UpsertProjectWithEdges(p, edges); err != nil {
		t.Fatal(err)
	}
	got, _ := db.ProjectsByID("wc", "wc-ab2c")
	if len(got) != 1 || got[0].Status != "todo" {
		t.Fatalf("row = %+v", got)
	}
	all, _ := db.AllEdges()
	if len(all) != 1 || all[0].ToID != "wc-de34" {
		t.Fatalf("edges = %+v", all)
	}

	// Re-upsert with a different status and no edges: row updated, edge gone.
	p.Status = "in-progress"
	if err := db.UpsertProjectWithEdges(p, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = db.ProjectsByID("wc", "wc-ab2c")
	if got[0].Status != "in-progress" {
		t.Fatalf("status not updated: %v", got[0].Status)
	}
	if all, _ := db.AllEdges(); len(all) != 0 {
		t.Fatalf("edges should be replaced to empty, got %+v", all)
	}
}

func TestSearchBM25AndScopeBound(t *testing.T) {
	db := openTemp(t)
	a := proj("wc", "ab2c", "todo", "a0")
	a.Title = "Network redesign"
	a.Body = []byte("sockets and network buffers")
	b := proj("wc", "de34", "todo", "a1")
	b.Title = "Unrelated"
	b.Body = []byte("mentions network once")
	c := proj("ui", "gh56", "todo", "a0")
	c.Title = "Network in ui"
	c.Body = []byte("network network")
	for _, p := range []*Project{a, b, c} {
		if err := db.UpsertProject(p); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := db.Search("", "network")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("machine-wide hits = %d, want 3", len(hits))
	}
	// Scope bound.
	hits, _ = db.Search("ui", "network")
	if len(hits) != 1 || hits[0].Project.Scope != "ui" {
		t.Fatalf("scope-bound search = %+v", hits)
	}
}

func TestSearchMalformedQueryIsTyped(t *testing.T) {
	db := openTemp(t)
	// An unbalanced quote is a malformed FTS5 query, not an infrastructure fault.
	_, err := db.Search("", `foo"`)
	if err == nil {
		t.Fatal("malformed query should error")
	}
	if !errors.Is(err, ErrSearchQuery) {
		t.Fatalf("malformed query should be ErrSearchQuery, got %v", err)
	}
	// A well-formed query over an empty index is not an error.
	if _, err := db.Search("", "network"); err != nil {
		t.Fatalf("valid query should not error: %v", err)
	}
}

func TestDuplicateAndEqualOrderAggregates(t *testing.T) {
	db := openTemp(t)
	dupA := proj("wc", "ab2c", "todo", "a0")
	dupB := proj("wc", "ab2c", "done", "a1")
	dupB.Path = "/tmp/wc/archive/ab2c.md" // same id, different file
	same1 := proj("wc", "de34", "todo", "b0")
	same2 := proj("wc", "gh56", "todo", "b0") // equal order key
	for _, p := range []*Project{dupA, dupB, same1, same2} {
		if err := db.UpsertProject(p); err != nil {
			t.Fatal(err)
		}
	}
	dups, _ := db.DuplicateIDs([]string{"wc"})
	if len(dups) != 1 || dups[0].Key != "wc-ab2c" || len(dups[0].Members) != 2 {
		t.Fatalf("duplicate ids = %+v", dups)
	}
	eq, _ := db.EqualOrders([]string{"wc"})
	if len(eq) != 1 || eq[0].Key != "b0" || len(eq[0].Members) != 2 {
		t.Fatalf("equal orders = %+v", eq)
	}
	set, _ := db.DuplicateIDSet([]string{"wc"})
	if !set["wc\x00wc-ab2c"] {
		t.Fatalf("duplicate set missing collision: %+v", set)
	}
}

func TestDeleteScopePrunes(t *testing.T) {
	db := openTemp(t)
	_ = db.UpsertProject(proj("wc", "ab2c", "todo", "a0"))
	_ = db.UpsertProject(proj("ui", "de34", "todo", "a0"))
	_ = db.SetLastIndex("wc", 123)
	if err := db.DeleteScope("wc"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.ScopeProjects("wc"); len(rows) != 0 {
		t.Fatalf("wc rows survived delete: %d", len(rows))
	}
	if rows, _ := db.ScopeProjects("ui"); len(rows) != 1 {
		t.Fatalf("ui rows should be untouched: %d", len(rows))
	}
	if ns, _ := db.LastIndex("wc"); ns != 0 {
		t.Fatalf("scope_meta not pruned: %d", ns)
	}
}

func TestReadOnlyQueryGuard(t *testing.T) {
	db := openTemp(t)
	_ = db.UpsertProject(proj("wc", "ab2c", "todo", "a0"))

	res, err := db.RunReadOnlyQuery(`SELECT id, status FROM projects ORDER BY id`)
	if err != nil {
		t.Fatalf("select rejected: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] != "wc-ab2c" {
		t.Fatalf("select result = %+v", res)
	}

	for _, bad := range []string{
		`INSERT INTO projects(path,scope,id) VALUES('x','wc','wc-zz22')`,
		`UPDATE projects SET status='done'`,
		`DELETE FROM projects`,
		`DROP TABLE projects`,
		`SELECT 1; DELETE FROM projects`,
		`PRAGMA journal_mode = DELETE`,
		`replace into projects(path,scope,id) values('x','wc','wc-zz22')`,
		// A write smuggled behind a CTE: the static classifier sees a leading WITH,
		// so only the runtime PRAGMA query_only guard catches it.
		`WITH t AS (SELECT 1) DELETE FROM projects`,
		`WITH t AS (SELECT 1) UPDATE projects SET status='done'`,
	} {
		if _, err := db.RunReadOnlyQuery(bad); err == nil {
			t.Errorf("read-only guard admitted a write: %q", bad)
		}
	}
	// A legitimate CTE-prefixed SELECT is still allowed.
	if _, err := db.RunReadOnlyQuery(`WITH t AS (SELECT id FROM projects) SELECT * FROM t`); err != nil {
		t.Errorf("read-only CTE select rejected: %v", err)
	}
	// A read-only PRAGMA and EXPLAIN are allowed.
	if _, err := db.RunReadOnlyQuery(`PRAGMA table_info(projects)`); err != nil {
		t.Errorf("read-only pragma rejected: %v", err)
	}
	if _, err := db.RunReadOnlyQuery(`EXPLAIN QUERY PLAN SELECT * FROM projects`); err != nil {
		t.Errorf("explain rejected: %v", err)
	}
	// A ';' inside a string literal or quoted identifier is not a batch separator, so
	// a legitimate read-only query that merely contains one is not spuriously refused.
	for _, ok := range []string{
		`SELECT id FROM projects WHERE id LIKE '%;%'`,
		`SELECT ';' AS sep FROM projects`,
		`SELECT id AS "a;b" FROM projects`,
	} {
		if _, err := db.RunReadOnlyQuery(ok); err != nil {
			t.Errorf("read-only query with a quoted ';' rejected: %q: %v", ok, err)
		}
	}
	// A real write after a genuine separator is still caught even when an earlier
	// statement carries a quoted ';'.
	if _, err := db.RunReadOnlyQuery(`SELECT ';' FROM projects; DELETE FROM projects`); err == nil {
		t.Error("read-only guard admitted a write following a quoted-';' select")
	}
	// The write must not have landed.
	if rows, _ := db.ScopeProjects("wc"); len(rows) != 1 {
		t.Fatalf("a rejected write still mutated the store: %d rows", len(rows))
	}
}

func TestConfigCache(t *testing.T) {
	db := openTemp(t)
	if _, ok, _ := db.ConfigCacheGet("wc"); ok {
		t.Fatal("empty cache should miss")
	}
	e := ConfigCacheEntry{ClosureJSON: "k1", SchemaJSON: `{"name":"wc"}`}
	if err := db.ConfigCacheSet("wc", e); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.ConfigCacheGet("wc")
	if err != nil || !ok || got.ClosureJSON != "k1" || got.SchemaJSON != `{"name":"wc"}` {
		t.Fatalf("cache get = %+v ok=%v err=%v", got, ok, err)
	}
}
