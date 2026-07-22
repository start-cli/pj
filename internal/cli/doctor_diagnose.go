package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/frontmatter"
	"github.com/start-cli/pj/internal/git"
	"github.com/start-cli/pj/internal/gitroot"
	"github.com/start-cli/pj/internal/gitstate"
	"github.com/start-cli/pj/internal/id"
	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/order"
	"github.com/start-cli/pj/internal/reconcile"
	"github.com/start-cli/pj/internal/repair"
	"github.com/start-cli/pj/internal/resolve"
	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/status"
	"github.com/start-cli/pj/internal/token"
)

// staleInProgress is the wall-clock age past which a built-in in-progress project is
// flagged as a possibly-abandoned claim (design: Status and dependencies).
const staleInProgress = 72 * time.Hour

// diagnose builds the full doctor report over the given scopes: every integrity class
// with its stable token where one is defined, plus the two token-less structural
// classes. It reads the refreshed index and re-reads each project file for the
// frontmatter-shape checks; it never mutates a file. res supplies each scope's schema,
// unreachable flag, and config error.
func (e *engine) diagnose(c *cobra.Command, scopes []string, res *reconcile.Result) ([]string, error) {
	d, err := e.newDiagnoser(c, res)
	if err != nil {
		return nil, err
	}
	for _, scope := range scopes {
		if err := d.scope(scope); err != nil {
			return nil, err
		}
	}
	return d.lines, nil
}

// diagnoser holds the machine-wide state a doctor run reads once (project rows and
// the edge graph, so cross-scope edge targets resolve) plus per-run dedup sets.
type diagnoser struct {
	e         *engine
	c         *cobra.Command
	res       *reconcile.Result
	now       time.Time
	hasRow    map[string]bool
	rowByID   map[string]*index.Project
	edges     []index.Edge
	schemaFor map[string]*scopeconfig.Schema
	seenRoot  map[string]bool
	// registered is the registry's scope set, constant for the run — a cross-scope edge
	// target is unresolvable when its scope is not in it.
	registered map[string]bool
	// cfgReported keeps config_unparseable to one line per scope across the two paths
	// that can reach it: the diagnosed scope's reconcile result and the sibling preflight.
	cfgReported map[string]bool
	lines       []string
}

func (e *engine) newDiagnoser(c *cobra.Command, res *reconcile.Result) (*diagnoser, error) {
	all, err := e.db.AllProjects()
	if err != nil {
		return nil, err
	}
	edges, err := e.db.AllEdges()
	if err != nil {
		return nil, err
	}
	d := &diagnoser{
		e: e, c: c, res: res, now: time.Now(),
		hasRow: map[string]bool{}, rowByID: map[string]*index.Project{},
		edges: edges, schemaFor: map[string]*scopeconfig.Schema{}, seenRoot: map[string]bool{},
		registered: e.registeredSet(), cfgReported: map[string]bool{},
	}
	for _, p := range all {
		d.hasRow[p.ID] = true
		if _, ok := d.rowByID[p.ID]; !ok {
			d.rowByID[p.ID] = p
		}
	}
	return d, nil
}

func (d *diagnoser) add(line string) { d.lines = append(d.lines, line) }

// scope runs every class for one scope. An unreachable dir or a drifted registration
// short-circuits the project-level checks (the scope is not a usable namespace), but
// each still rides its own token.
func (d *diagnoser) scope(scope string) error {
	entry, ok := d.e.reg.Scopes[scope]
	if !ok {
		return nil
	}
	dir := entry.Dir

	if d.res.Unreachable[scope] {
		d.add(token.Line(token.UnreachableScope, fmt.Sprintf("%s: dir %s could not be read — rows left in place", scope, dir)))
		return nil
	}
	if pjName, err := scopeconfig.ReadName(d.e.app.Ctx, dir); err == nil && pjName != scope {
		d.add(resolve.DriftLine(scope, pjName, dir, resolve.SuggestCodeRoot(dir, entry.Root)))
		return nil
	}

	schema := d.res.Schema(scope)
	if cfgErr, ok := d.res.ConfigErrs[scope]; ok {
		d.configUnparseable(scope, cfgErr)
	}

	rows, err := d.e.db.ScopeProjects(scope)
	if err != nil {
		return err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })

	if err := d.collisions(scope); err != nil {
		return err
	}
	autoCommit, autoCommitKnown := d.autoCommitFor(scope, schema)
	d.perRow(dir, rows, schema, autoCommit, autoCommitKnown)
	d.edgeClasses(scope)
	if err := d.repoHealth(scope, dir, schema); err != nil {
		return err
	}
	d.residue(scope, dir)
	return nil
}

// collisions rides the cross-file aggregates from the index: duplicate ids and equal
// order keys within the scope.
func (d *diagnoser) collisions(scope string) error {
	dups, err := d.e.db.DuplicateIDs([]string{scope})
	if err != nil {
		return err
	}
	for _, col := range dups {
		d.add(token.Line(token.DuplicateID, fmt.Sprintf("%s claimed by %s — run pj doctor --repair", col.Key, strings.Join(col.Members, ", "))))
	}
	eq, err := d.e.db.EqualOrders([]string{scope})
	if err != nil {
		return err
	}
	for _, col := range eq {
		d.add(token.Line(token.EqualOrder, fmt.Sprintf("%s share order %q: %s — run pj doctor --repair", scope, col.Key, strings.Join(col.Members, ", "))))
	}
	return nil
}

// perRow runs every per-project class: parse_error, order_long, archive layout,
// status_conflict, stale_in_progress, schema_error, the frontmatter schema_warn
// family, and the two token-less structural classes (filename/id shape and created).
func (d *diagnoser) perRow(dir string, rows []*index.Project, schema *scopeconfig.Schema, autoCommit, autoCommitKnown bool) {
	custom := schemaCustom(schema)
	for _, p := range rows {
		if p.ParseError {
			d.add(token.Line(token.ParseError, fmt.Sprintf("%s: %s (%s)", p.ID, p.ParseMsg, p.Path)))
			continue
		}
		// order is the board's rank space, so an invalid key does not fail — it sorts
		// somewhere nobody intended. The design makes this hard and every mutating write
		// already rejects it; without this the only order class is order_long, which is
		// itself gated on validity, so hand-edited garbage passed clean.
		//
		// The two arms are exclusive: an invalid key cannot also be meaningfully
		// over-long, and reporting both would give one key two lines with different fixes.
		switch {
		case p.OrderKey == "":
			// A missing key and an explicit "" are indistinguishable by here — the
			// frontmatter model erases the difference — so the message claims neither.
			d.add(token.Line(token.SchemaError, fmt.Sprintf("%s has a missing or empty order key (%s) — set a quoted order key, or run pj reorder", p.ID, p.Path)))
		case !order.Valid(p.OrderKey):
			d.add(token.Line(token.SchemaError, fmt.Sprintf("%s has an invalid order key %q (%s) — outside the closed order grammar; set a quoted valid key, or run pj reorder", p.ID, p.OrderKey, p.Path)))
		case len(p.OrderKey) > repair.OrderLongThreshold:
			d.add(token.Line(token.OrderLong, fmt.Sprintf("%s order key is %d chars (%s) — run pj doctor --re-space-order", p.ID, len(p.OrderKey), p.Path)))
		}
		terminal := status.IsTerminal(p.Status, custom)
		switch {
		case p.Archived && !terminal:
			d.add(token.Line(token.ArchiveNonTerminal, fmt.Sprintf("%s is %s under archive/ (%s)", p.ID, p.Status, p.Path)))
		case !p.Archived && terminal:
			d.add(token.Line(token.ArchiveTerminalAtRoot, fmt.Sprintf("%s is %s at dir root (%s)", p.ID, p.Status, p.Path)))
		}
		if len(p.StatusConflict) > 0 {
			d.add(d.statusConflictLine(p, dir, autoCommit, autoCommitKnown))
		}
		if p.Status == status.InProgress && p.MtimeNS > 0 && d.now.Sub(time.Unix(0, p.MtimeNS)) > staleInProgress {
			age := d.now.Sub(time.Unix(0, p.MtimeNS)).Round(time.Hour)
			d.add(token.Line(token.StaleInProgress, fmt.Sprintf("%s has been in-progress for %s (%s) — inspect; maybe reopen to todo", p.ID, age, p.Path)))
		}
		if p.SchemaError {
			d.add(token.Line(token.SchemaError, fmt.Sprintf("%s has a depends/related entry that is not a legal full project id (%s)", p.ID, p.Path)))
		}
		d.frontmatterChecks(p, schema)
	}
}

// statusConflictLine reports a status_conflict in one of three modes, selected by what
// is known about the scope. Every mode instructs the same physical resolution — set
// status in the file, drop the key — and differs only in the tail.
//
// The mid-rebase tail is gated on autoCommit, not on repo topology: pj sync runs only
// for an auto-commit scope, so the host repo being mid-rebase says nothing about a
// repo-driven or plain-files scope. Such a scope gets the standalone residue tail even
// mid-rebase, because pj never wrote that key as part of a sync it manages.
//
// When autoCommit is unknown (unparseable pj.cue) neither tail is derivable: "then pj
// sync" may name a command that never runs here, and "stale residue" asserts a staleness
// there is no basis for. That mode states the dispute and its resolution only.
func (d *diagnoser) statusConflictLine(p *index.Project, dir string, autoCommit bool, autoCommitKnown bool) string {
	disputed := strings.Join(p.StatusConflict, " vs ")
	if !autoCommitKnown {
		return token.Line(token.StatusConflict, fmt.Sprintf("%s disputes %s (%s) — set status and clear status_conflict", p.ID, disputed, p.Path))
	}
	// autoCommit gates the git lookup, not merely the outcome: gitRootFor shells out, and
	// on a non-auto-commit scope its answer cannot change the line.
	if autoCommit {
		if root, ok := gitRootFor(dir); ok && git.MidRebase(d.c.Context(), root) {
			return token.Line(token.StatusConflict, fmt.Sprintf("%s disputes %s (%s) — resolve in file, then pj sync", p.ID, disputed, p.Path))
		}
	}
	return token.Line(token.StatusConflict, fmt.Sprintf("%s disputes %s (%s) — stale residue: set status and clear status_conflict", p.ID, disputed, p.Path))
}

// frontmatterChecks re-reads a healthy project file and rides the classes that need
// the raw frontmatter model: the token-less created and filename/id structural checks,
// and the schema_warn family (self-related, duplicate list entries, id-shaped links,
// undeclared keys, knownTags typos). self-depends is hard (depends_self).
func (d *diagnoser) frontmatterChecks(p *index.Project, schema *scopeconfig.Schema) {
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return
	}
	interior, _, present := frontmatter.Split(data)
	if !present {
		return
	}
	m, err := frontmatter.Parse(interior)
	if err != nil {
		return
	}

	// Token-less structural classes (human-priority prose, no agent token). The design
	// makes this the union of two failures: the filename does not begin with the
	// frontmatter id, or the id/slug shape is not a project file shape.
	//
	// The slug tail needs its own check because the indexer and the allowlist disagree
	// about it on purpose. reconcile's projectID validates only the short-id, so a
	// malformed tail still yields a locatable row rather than an invisible project;
	// looksLikeProjectFile requires slug.Valid, so that file never reaches a commit.
	// Doctor is what reconciles the two — without this the file is a live project that
	// only ever rides non_allowlist, telling the operator to remove real work.
	base := filepath.Base(p.Path)
	switch {
	case m.ID == "" || !strings.HasPrefix(base, m.ID+"-") && strings.TrimSuffix(base, ".md") != m.ID:
		d.add(fmt.Sprintf("filename/id mismatch: %s does not begin with its frontmatter id %q", base, m.ID))
	case !id.IsFullProjectID(m.ID):
		d.add(fmt.Sprintf("filename/id mismatch: %s has a non-project-shaped id %q", base, m.ID))
	case !looksLikeProjectFile(base):
		d.add(fmt.Sprintf("filename/id mismatch: %s is not a project file shape (<id>-<slug>.md) — rename it to a valid slug", base))
	}
	// Prose, not a key: this class carries no token by design, so the line must not open
	// with a lowercase word and a colon — that is the shape of every closed token, and an
	// agent scanning for one would read this as a token the catalogue does not contain.
	if !validRFC3339(m.Created) {
		d.add(fmt.Sprintf("created value missing or not RFC3339 in %s: %q (%s) — fix so the id-collision repair order stays strong", p.ID, m.Created, p.Path))
	}

	// depends_self is hard; self-related is a soft schema_warn (there is no
	// related_self token).
	if contains(m.Depends, m.ID) {
		d.add(token.Line(token.DependsSelf, fmt.Sprintf("%s lists its own id in depends — remove the self-edge (%s)", p.ID, p.Path)))
	}
	if contains(m.Related, m.ID) {
		d.add(token.Line(token.SchemaWarn, fmt.Sprintf("%s lists its own id in related (%s)", p.ID, p.Path)))
	}
	for _, kind := range []struct {
		name string
		list []string
	}{{"depends", m.Depends}, {"related", m.Related}, {"tags", m.Tags}, {"links", m.Links}} {
		if dup := firstDuplicate(kind.list); dup != "" {
			d.add(token.Line(token.SchemaWarn, fmt.Sprintf("%s has a duplicate %s entry %q (%s)", p.ID, kind.name, dup, p.Path)))
		}
	}
	for _, link := range m.Links {
		if id.IsFullProjectID(link) {
			d.add(token.Line(token.SchemaWarn, fmt.Sprintf("%s links entry %q is project-id-shaped — use related/depends for project ids (%s)", p.ID, link, p.Path)))
		}
	}
	if schema != nil {
		if m.Status != "" && !schema.StatusKnown(m.Status) {
			d.add(token.Line(token.SchemaError, fmt.Sprintf("%s has unknown status %q (%s)", p.ID, m.Status, p.Path)))
		}
		for _, f := range m.Custom {
			field, declared := schema.Fields[f.Key]
			if !declared {
				d.add(token.Line(token.SchemaWarn, fmt.Sprintf("%s has undeclared frontmatter key %q (%s)", p.ID, f.Key, p.Path)))
				continue
			}
			if reason := fieldTypeError(field, f.Value); reason != "" {
				d.add(token.Line(token.SchemaError, fmt.Sprintf("%s field %q %s (%s)", p.ID, f.Key, reason, p.Path)))
				continue
			}
			// A strings field carries the same 3-way set merge as the built-in lists, so
			// the same duplicate noise reaches the same merge path. Checked only after
			// the type check passes, so a field that is not a list reports its type
			// error rather than a spurious duplicate warning.
			if field.Type == scopeconfig.FieldStrings {
				if dup := firstDuplicate(stringElems(f.Value)); dup != "" {
					d.add(token.Line(token.SchemaWarn, fmt.Sprintf("%s has a duplicate %s entry %q (%s)", p.ID, f.Key, dup, p.Path)))
				}
			}
		}
		if len(schema.KnownTags) > 0 {
			known := sliceSet(schema.KnownTags)
			for _, tag := range m.Tags {
				if !known[tag] {
					d.add(token.Line(token.SchemaWarn, fmt.Sprintf("%s tag %q is not in knownTags — likely a typo (%s)", p.ID, tag, p.Path)))
				}
			}
		}
	}
}

// fieldTypeError reports why a declared custom field's value violates its schema — a
// scalar/list shape mismatch or a value outside a declared enum — or "" when it is
// valid. It flags only clear violations, erring toward silence on YAML-decode
// ambiguity so a doctor run never invents a schema_error.
func fieldTypeError(field scopeconfig.Field, value any) string {
	switch field.Type {
	case scopeconfig.FieldStrings:
		list, ok := value.([]any)
		if !ok {
			return "should be a list of strings"
		}
		// A strings field is a sequence of strings, not a free-form mixed sequence
		// (design.md:1267–1276) — it carries the same set merge as tags/depends, so a
		// non-string element would reach that merge as an untyped scalar. Checked before
		// the enum so a mixed list reports its type violation rather than an enum
		// complaint about a value that was never a string.
		for _, e := range list {
			if _, ok := e.(string); !ok {
				return fmt.Sprintf("has a non-string entry (%v)", e)
			}
		}
		if len(field.Values) > 0 {
			allowed := sliceSet(field.Values)
			for _, e := range list {
				if s, ok := e.(string); ok && !allowed[s] {
					return fmt.Sprintf("has value %q outside its declared values", s)
				}
			}
		}
	case scopeconfig.FieldString:
		s, ok := value.(string)
		if !ok {
			return "should be a string"
		}
		if len(field.Values) > 0 && !sliceSet(field.Values)[s] {
			return fmt.Sprintf("value %q is outside its declared values", s)
		}
	case scopeconfig.FieldInt:
		if !isIntKind(value) {
			return "should be an integer"
		}
	case scopeconfig.FieldBool:
		if _, ok := value.(bool); !ok {
			return "should be a bool"
		}
	}
	return ""
}

// isIntKind reports whether a YAML-decoded value is an integer scalar (any signed or
// unsigned width). A float or string is not, so a mistyped int field is caught.
func isIntKind(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}

// edgeClasses rides the depends/related graph classes for edges originating in scope:
// same-scope dangling (hard), cross-scope unresolvable (informational), depends on a
// cancelled/abandoned target, unresolvable related, and depends cycles.
func (d *diagnoser) edgeClasses(scope string) {
	for _, id := range d.cycleNodes(scope) {
		d.add(token.Line(token.DependsCycle, fmt.Sprintf("%s is in a depends cycle — fix the edges", id)))
	}
	for _, ed := range d.edges {
		if ed.FromScope != scope || ed.FromID == ed.ToID {
			continue // self-edges are reported from the frontmatter model
		}
		if ed.Kind == index.EdgeRelated {
			if !d.hasRow[ed.ToID] {
				d.add(token.Line(token.RelatedUnresolvable, fmt.Sprintf("%s related target %s is not resolvable here", ed.FromID, ed.ToID)))
			}
			continue
		}
		if ed.ToScope == scope {
			if !d.hasRow[ed.ToID] {
				d.add(token.Line(token.DependsDangling, fmt.Sprintf("%s depends on %s which has no project in this scope", ed.FromID, ed.ToID)))
			}
		} else if !d.registered[ed.ToScope] || !d.hasRow[ed.ToID] {
			d.add(token.Line(token.DependsUnresolvable, fmt.Sprintf("%s depends on cross-scope %s which is not resolvable here", ed.FromID, ed.ToID)))
		}
		if d.dependsOnAbandoned(ed.ToID, ed.ToScope) {
			d.add(token.Line(token.DependsOnCancelled, fmt.Sprintf("%s depends on %s which is cancelled/abandoned — decide if it still applies", ed.FromID, ed.ToID)))
		}
	}
}

// dependsOnAbandoned reports whether a depends target resolves to a cancelled project
// or a custom done-category status (an abandonment-shaped terminal). A built-in done
// is normal completion and is not flagged.
func (d *diagnoser) dependsOnAbandoned(toID, toScope string) bool {
	row, ok := d.rowByID[toID]
	if !ok {
		return false
	}
	if row.Status == status.Cancelled {
		return true
	}
	if status.IsBuiltin(row.Status) {
		return false
	}
	schema := d.targetSchema(toScope)
	return schema != nil && schema.StatusTerminal(row.Status)
}

// targetSchema returns and caches a scope's schema for a cross-scope terminal check.
func (d *diagnoser) targetSchema(scope string) *scopeconfig.Schema {
	if s, ok := d.schemaFor[scope]; ok {
		return s
	}
	var s *scopeconfig.Schema
	if entry, ok := d.e.reg.Scopes[scope]; ok {
		s = d.e.rec.SchemaCached(scope, entry.Dir)
	}
	d.schemaFor[scope] = s
	return s
}

// cycleNodes returns the sorted ids of projects in scope that participate in a depends
// cycle among two or more distinct projects. It walks the machine-wide depends graph so a
// cross-scope cycle is caught too.
//
// Self-edges are excluded from the graph: a pure self-depends is owned by depends_self,
// the dedicated hard class (design.md:2438–2441), and reporting it as a cycle as well
// gives one edge two hard lines pointing at different fixes. This is the same exclusion
// edgeClasses already makes for the same reason. A project in a real cycle that also has
// a self-edge still reports, because its other edges remain.
func (d *diagnoser) cycleNodes(scope string) []string {
	adj := map[string][]string{}
	for _, ed := range d.edges {
		if ed.Kind == index.EdgeDepends && ed.FromID != ed.ToID {
			adj[ed.FromID] = append(adj[ed.FromID], ed.ToID)
		}
	}
	var out []string
	for _, p := range d.scopeIDs(scope) {
		if reaches(p, p, adj, map[string]bool{}) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// scopeIDs returns the distinct project ids whose prefix is scope, from the machine
// row map.
func (d *diagnoser) scopeIDs(scope string) []string {
	var out []string
	for id, p := range d.rowByID {
		if p.Scope == scope {
			out = append(out, id)
		}
	}
	return out
}

// autoCommitFor reports a scope's autoCommit and whether it is knowable at all. It is
// the read-side twin of the write verbs' refuseUnusableScope: autoCommit lives only in
// pj.cue, so an unparseable config makes the value unknown, not false. Doctor reports
// where a write refuses, but neither may guess — a guessed false would tell an
// auto-commit scope to commit by hand and suppress its sync classes (design.md:1323–1332).
func (d *diagnoser) autoCommitFor(scope string, schema *scopeconfig.Schema) (value bool, known bool) {
	if _, unusable := d.res.ConfigErrs[scope]; unusable {
		return false, false
	}
	return schemaAutoCommit(schema), true
}

// repoHealth rides the sync/config/repo classes derived off-sync: auto_commit_mismatch
// (per-git-root, deduped), sync_disabled, last_push_error, and the repo-driven
// uncommitted signal. It never runs a mutating git command.
func (d *diagnoser) repoHealth(scope, dir string, schema *scopeconfig.Schema) error {
	root, hasRoot := gitRootFor(dir)

	if hasRoot && !d.seenRoot[root] {
		d.seenRoot[root] = true
		d.rootPreflight(root)
	}

	// Unknown autoCommit makes none of the classes below derivable. The scope already
	// rides config_unparseable; every class outside this function still runs, because an
	// unusable config gates writes only.
	autoCommit, known := d.autoCommitFor(scope, schema)
	if !known {
		return nil
	}

	switch {
	case autoCommit:
		if !hasRoot || !git.HasUpstream(d.c.Context(), root) {
			d.add(token.Line(token.SyncDisabled, fmt.Sprintf("%s: no git repository with an upstream — set one up, then pj sync", scope)))
		}
		if hasRoot {
			if detail, ok := gitstate.ReadLastPushError(d.e.app.StateDir, root); ok {
				d.add(token.Line(token.LastPushError, fmt.Sprintf("%s: last push failed (%s) — fix the remote/auth, then pj sync", scope, detail)))
			}
		}
	case hasRoot: // repo-driven: autoCommit false inside git
		dirty, err := git.DirtyPaths(d.c.Context(), root, dir)
		if err == nil {
			n := 0
			for _, p := range dirty {
				if isAllowlistedScopeFile(p, dir) {
					n++
				}
			}
			if n > 0 {
				d.add(token.Line(token.Uncommitted, fmt.Sprintf("%s: %d allowlisted path(s) under %s uncommitted — commit with the host repo", scope, n, dir)))
			}
		}
	}
	return nil
}

// rootPreflight is the per-git-root preflight doctor runs off-sync — the same shared-repo
// proof pj sync demands before it pushes, reported rather than refused. It walks every
// registered scope sharing root once and rides both repo-granular classes:
// config_unparseable for a sibling whose pj.cue will not load, and auto_commit_mismatch
// when the loadable siblings disagree on autoCommit.
//
// The sibling scan is why this cannot ride the diagnosed scopes alone: with an ambient
// scope resolved, a broken sibling sharing the root is never reconciled, yet it is what
// makes the whole git-root unsyncable. An unreadable autoCommit is not a disagreement —
// there is no value to compare — so such a scope rides config_unparseable and is left out
// of the mismatch verdict.
func (d *diagnoser) rootPreflight(root string) {
	seenTrue, seenFalse := false, false
	for _, name := range d.siblingScopes(root) {
		schema, cfgErr := d.e.rec.SchemaOrError(name, d.e.reg.Scopes[name].Dir)
		if cfgErr != nil {
			d.configUnparseable(name, cfgErr)
			continue
		}
		if schema == nil {
			continue
		}
		if schema.AutoCommit {
			seenTrue = true
		} else {
			seenFalse = true
		}
	}
	if seenTrue && seenFalse {
		d.add(token.Line(token.AutoCommitMismatch, fmt.Sprintf("scopes sharing git-root %s disagree on autoCommit — split the divergent scope into its own repo", root)))
	}
}

// siblingScopes returns the registered scopes whose dir derives root, sorted so the
// preflight's output does not depend on registry map order.
func (d *diagnoser) siblingScopes(root string) []string {
	var out []string
	for name, entry := range d.e.reg.Scopes {
		if sgr, ok := gitroot.RepoRoot(entry.Dir); ok && sgr == root {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// configUnparseable rides the class at most once per scope per run. A scope can be
// reached twice — once as a diagnosed scope from the reconcile result, once as a
// git-root sibling — and the second report would add no information.
func (d *diagnoser) configUnparseable(scope string, cfgErr *scopeconfig.ConfigError) {
	if d.cfgReported[scope] {
		return
	}
	d.cfgReported[scope] = true
	d.add(token.Line(token.ConfigUnparseable, fmt.Sprintf("%s (%s): %s — fix pj.cue", scope, cfgErr.Dir, cfgErr.Reason)))
}

// residue walks the scope dir and rides non_allowlist for every path outside the
// closed allowlist (nested archive trees, vendor conflict copies, AGENTS.md, …) for
// human cleanup. It never deletes or commits.
func (d *diagnoser) residue(scope, dir string) {
	_ = filepath.WalkDir(dir, func(path string, ent os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ent.IsDir() {
			if ent.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		base := ent.Name()
		if base == scopeLockName {
			return nil
		}
		if isAllowlistedScopeFile(path, dir) {
			return nil
		}
		d.add(token.Line(token.NonAllowlist, fmt.Sprintf("%s: %s is under the scope dir but outside the allowlist — move or remove it", scope, path)))
		return nil
	})
}

func validRFC3339(s string) bool {
	if s == "" {
		return false
	}
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}

func contains(list []string, v string) bool {
	for _, e := range list {
		if e == v {
			return true
		}
	}
	return false
}

// stringElems returns the string elements of a YAML-decoded sequence, skipping any
// non-string. It feeds the shared duplicate predicate from an any-typed custom field;
// a non-string element is the type check's business, not this one's.
func stringElems(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func firstDuplicate(list []string) string {
	seen := map[string]bool{}
	for _, e := range list {
		if seen[e] {
			return e
		}
		seen[e] = true
	}
	return ""
}

func sliceSet(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, e := range list {
		out[e] = true
	}
	return out
}

// reaches reports whether target is reachable from node by following adj. The start is
// not marked visited, so a walk that returns to it is detected as the cycle it is; adj
// carries no self-edges, so that return is always via at least one other project.
func reaches(node, target string, adj map[string][]string, visited map[string]bool) bool {
	for _, next := range adj[node] {
		if next == target {
			return true
		}
		if visited[next] {
			continue
		}
		visited[next] = true
		if reaches(next, target, adj, visited) {
			return true
		}
	}
	return false
}
