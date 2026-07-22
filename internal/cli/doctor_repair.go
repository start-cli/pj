package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/git"
	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/reconcile"
	"github.com/start-cli/pj/internal/repair"
	"github.com/start-cli/pj/internal/rewrite"
	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/selfcommit"
	"github.com/start-cli/pj/internal/status"
	"github.com/start-cli/pj/internal/token"
)

// repairTarget is one scope cleared for repair: the schema-derived autoCommit and the
// derived git-root, resolved once under the scope flock and threaded through every batch
// so each agrees on the same values for the whole run.
type repairTarget struct {
	scope      string
	dir        string
	schema     *scopeconfig.Schema
	autoCommit bool
	root       string
	hasRoot    bool
}

// runRepairs applies the mutating doctor procedures over the selected scopes. --repair
// runs the id-collision, equal-order, and archive-layout repairs; --re-space-order runs
// only the over-long band re-space.
func (e *engine) runRepairs(c *cobra.Command, scopes []string, f doctorFlags) error {
	for _, scope := range scopes {
		entry, ok := e.reg.Scopes[scope]
		if !ok {
			continue
		}
		if err := e.repairScope(c, scope, entry.Dir, f); err != nil {
			return err
		}
	}
	return nil
}

// repairScope holds the scope flock across the reconcile, the preflight, and every repair
// batch for one scope, so the whole plan runs under the U22 durability contract.
//
// The reconcile is inside the lock, not before it: the repair procedures choose which
// files to rewrite from these rows, so the read that decides and the write that acts must
// sit in one lock span — the same span every complete-state write verb holds. A reconcile
// taken outside it could be invalidated by a concurrent pj status before the first byte
// is written.
//
// The layout repair runs before the collision repair because an interrupted archive move
// leaves two same-id copies that the index reports as a collision; completing the move
// first collapses them, and repairCollisions independently refuses such a pair so the
// ordering is not the only thing standing between a crash and a forked id.
func (e *engine) repairScope(c *cobra.Command, scope, dir string, f doctorFlags) error {
	// Reachability is checked before the flock, because taking the flock creates a file in
	// the dir and so fails outright on an unmounted one. Under --all that would stop every
	// remaining scope over one unplugged drive; the scope skips instead, and the report
	// still rides unreachable_scope for it.
	if _, err := os.Stat(dir); err != nil {
		stderrln(c, fmt.Sprintf("skipping %s: dir unreachable", scope))
		return nil
	}
	lock, err := acquireScopeLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	res, err := e.reconcileResult(single(scope, dir))
	if err != nil {
		return err
	}
	t, err := e.repairPreflight(c, scope, dir, res, f)
	if err != nil || t == nil {
		return err
	}

	if f.repair {
		if err := e.repairArchive(c, t, false); err != nil {
			return err
		}
		if err := e.repairCollisions(c, t); err != nil {
			return err
		}
		// The collision repair gave every loser a distinct id and so a distinct basename,
		// which frees the layout moves the first pass deferred as unsafe. This second pass
		// lands them, and reports whatever is still deferred because its collision could
		// not be repaired (a quarantined member).
		if err := e.repairArchive(c, t, true); err != nil {
			return err
		}
		if err := e.repairEqualOrder(c, t); err != nil {
			return err
		}
	}
	if f.reSpaceOrder {
		if err := e.repairLongOrder(c, t); err != nil {
			return err
		}
	}
	return nil
}

// repairPreflight decides whether a scope may be repaired at all, under its flock. A nil
// target with a nil error is a skip the caller reports nothing further about: an
// unreachable dir, a drifted registration, or an unusable pj.cue all make a write unsafe
// under the wrong name binding or schema, and each rides its own note here. A mid-rebase
// auto-commit git-root refuses hard for a single ambient scope and is skipped with a note
// under --all, where one frozen repo must not block every other scope.
func (e *engine) repairPreflight(c *cobra.Command, scope, dir string, res *reconcile.Result, f doctorFlags) (*repairTarget, error) {
	if res.Unreachable[scope] {
		stderrln(c, fmt.Sprintf("skipping %s: dir unreachable", scope))
		return nil, nil
	}
	if pjName, err := scopeconfig.ReadName(e.app.Ctx, dir); err == nil && pjName != scope {
		stderrln(c, token.Line(token.NameDrift, fmt.Sprintf("skipping %s: registry key %q but pj.cue name is %q — recover with pj scope forget/import", scope, scope, pjName)))
		return nil, nil
	}
	if _, bad := res.ConfigErrs[scope]; bad {
		stderrln(c, token.Line(token.ConfigUnparseable, fmt.Sprintf("skipping %s: fix pj.cue before repairing", scope)))
		return nil, nil
	}

	schema := res.Schema(scope)
	t := &repairTarget{scope: scope, dir: dir, schema: schema, autoCommit: schemaAutoCommit(schema)}
	t.root, t.hasRoot = gitRootFor(dir)
	if t.autoCommit && t.hasRoot && git.MidRebase(c.Context(), t.root) {
		if !f.all {
			return nil, midRebaseRefusal(c, scope, t.root)
		}
		stderrln(c, fmt.Sprintf("skipping %s: git-root %s is mid-rebase — resolve then re-run", scope, t.root))
		return nil, nil
	}
	return t, nil
}

// repairCollisions resolves every duplicate-id collision in the scope by the
// deterministic loser pick + short-id extension, keeping both files and leaving edges
// untouched. It reports each rename and, for each repaired collision, every inbound edge
// to the collided id as edge_verify (operation-time only, read from the live edges table).
//
// The inbound edges are read after every collision in the scope is repaired, not beside
// each one. A referrer may itself be a member of a later collision, so a pre-repair read
// names identities the operation then invalidates — two byte-identical lines for two
// distinct referrers sharing a collided id, one of which no longer exists by the end of
// the run. Each repair write-throughs its touched paths, so a read at the end resolves
// every referrer to its post-repair id. The kept side keeps the collided id, so inbound
// edges still resolve to it and none are lost by reading late.
func (e *engine) repairCollisions(c *cobra.Command, t *repairTarget) error {
	dups, err := e.db.DuplicateIDs([]string{t.scope})
	if err != nil {
		return err
	}
	if len(dups) == 0 {
		return nil
	}
	rows, err := e.db.ScopeProjects(t.scope)
	if err != nil {
		return err
	}
	occupied := shortIDPaths(rows)
	byPath := map[string]*index.Project{}
	for _, p := range rows {
		byPath[p.Path] = p
	}

	var repaired []string
	for _, col := range dups {
		members := rowsForPaths(byPath, col.Members)
		mid, err := repair.InterruptedMove(t.dir, toRepairRows(members))
		if err != nil {
			return err
		}
		if mid {
			stderrln(c, fmt.Sprintf("skipping %s: unfinished archive-layout move, not a collision — re-run pj doctor --repair to complete it", col.Key))
			continue
		}
		if anyParseError(members) {
			stderrln(c, token.Line(token.ParseError, fmt.Sprintf("%s: collision includes a quarantined file — fix its frontmatter before repair", col.Key)))
			continue
		}
		ops, renames, err := repair.DuplicateID(t.scope, toRepairRows(members), occupied)
		if err != nil {
			return err
		}
		if err := e.applyRepairBatch(c, t, ops, collisionMessage(renames)); err != nil {
			return err
		}
		for _, r := range renames {
			stdoutln(c, fmt.Sprintf("repaired duplicate id: %s -> %s (%s)", r.OldID, r.NewID, r.NewPath))
		}
		repaired = append(repaired, col.Key)
	}
	return e.reportEdgeVerify(c, repaired)
}

// reportEdgeVerify surfaces every inbound edge to each repaired collided id, converting a
// silent mispoint into a check: the kept side keeps the id, so a reference meaning the
// kept side is still right while one meaning the renamed side now resolves elsewhere.
// Skipped collisions contribute nothing — only ids actually repaired carry the hazard.
func (e *engine) reportEdgeVerify(c *cobra.Command, collidedIDs []string) error {
	for _, collidedID := range collidedIDs {
		inbound, err := e.db.EdgesByTarget(collidedID)
		if err != nil {
			return err
		}
		for _, ed := range inbound {
			stdoutln(c, token.Line(token.EdgeVerify, fmt.Sprintf("%s %s %s — target was collision-repaired, verify this reference", ed.FromID, ed.Kind, collidedID)))
		}
	}
	return nil
}

func (e *engine) repairEqualOrder(c *cobra.Command, t *repairTarget) error {
	rows, err := e.db.ScopeProjects(t.scope)
	if err != nil {
		return err
	}
	ops, err := repair.EqualOrder(toRepairRows(rows))
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	}
	if err := e.applyRepairBatch(c, t, ops, "pj: repair equal order"); err != nil {
		return err
	}
	stdoutln(c, fmt.Sprintf("re-spaced %d equal order key(s) in %s", len(ops), t.scope))
	return nil
}

// repairArchive moves every project whose layout disagrees with its terminal-ness across
// the archive boundary, both directions.
//
// A project whose id a genuine collision claims is deferred, not moved: its members share
// a basename, so moving one across the boundary would land it on the other and the source
// removal would leave no copy of what it overwrote. The collision repair de-conflicts them
// first; reportDeferred is set on the pass that runs after it, where anything still
// deferred is a layout the run cannot fix and the operator must hear about.
func (e *engine) repairArchive(c *cobra.Command, t *repairTarget, reportDeferred bool) error {
	rows, err := e.db.ScopeProjects(t.scope)
	if err != nil {
		return err
	}
	collided, err := e.genuineCollisionIDs(t.scope, t.dir)
	if err != nil {
		return err
	}
	deferred := map[string]bool{}
	custom := schemaCustom(t.schema)
	for _, p := range rows {
		if p.ParseError {
			continue
		}
		if collided[p.ID] {
			if reportDeferred && !deferred[p.ID] {
				deferred[p.ID] = true
				stderrln(c, fmt.Sprintf("archive layout for %s left as is: its id is still duplicated — repair the collision first", p.ID))
			}
			continue
		}
		terminal := status.IsTerminal(p.Status, custom)
		if p.Archived == terminal {
			continue
		}
		op, err := repair.ArchiveMove(t.dir, toRepairRow(p), terminal)
		if err != nil {
			return err
		}
		msg := fmt.Sprintf("pj: repair archive layout %s", p.ID)
		if err := e.applyRepairBatch(c, t, []rewrite.Op{op}, msg); err != nil {
			return err
		}
		stdoutln(c, fmt.Sprintf("moved archive layout: %s -> %s", p.ID, op.NewPath))
	}
	return nil
}

func (e *engine) repairLongOrder(c *cobra.Command, t *repairTarget) error {
	rows, err := e.db.ScopeProjects(t.scope)
	if err != nil {
		return err
	}
	ops, err := repair.LongOrder(toRepairRows(rows))
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	}
	if err := e.applyRepairBatch(c, t, ops, "pj: re-space order"); err != nil {
		return err
	}
	stdoutln(c, fmt.Sprintf("re-spaced %d over-long order key(s) in %s", len(ops), t.scope))
	return nil
}

// applyRepairBatch is the durability tail every repair batch shares: apply the ops
// (write-new-then-remove-old), index-sync the touched paths, and — on an auto-commit
// scope — self-commit them after every write succeeds (or ride sync_disabled without a
// git-root). Non-auto-commit writes files only; the host or plain-files sync owns
// durability.
func (e *engine) applyRepairBatch(c *cobra.Command, t *repairTarget, ops []rewrite.Op, message string) error {
	if len(ops) == 0 {
		return nil
	}
	touched, err := rewrite.Apply(ops)
	if err != nil {
		return err
	}
	if err := e.rec.SyncPaths(t.scope, touched); err != nil {
		return err
	}
	if !t.autoCommit {
		return nil
	}
	if !t.hasRoot {
		stderrln(c, token.Line(token.SyncDisabled, fmt.Sprintf("%s: no git repository — repaired files written but not committed", t.scope)))
		return nil
	}
	return selfcommit.CommitPaths(c.Context(), selfcommit.BatchRequest{
		StateDir: e.app.StateDir, GitRoot: t.root, Message: message, Paths: touched,
	})
}

// midRebaseRefusal builds the hard mid-rebase refusal for a single-scope mutating
// doctor run — the same class as the complete-state verbs.
func midRebaseRefusal(c *cobra.Command, scope, root string) error {
	where := "the conflicted file"
	if files := git.UnmergedFiles(c.Context(), root); len(files) > 0 {
		where = strings.Join(files, ", ")
	}
	return fmt.Errorf("%s is mid-sync-conflict in shared repo %s — resolve %s then run pj sync before repairing", scope, root, where)
}

// collisionMessage builds the fixed self-commit message for one collision batch. Every
// loser shares the collided id, so the message names it once and lists every new id in
// the order repair.DuplicateID produced them — deterministic, and identical to the
// design's single-loser example when the collision is two-way.
func collisionMessage(renames []repair.Rename) string {
	newIDs := make([]string, len(renames))
	for i, r := range renames {
		newIDs[i] = r.NewID
	}
	return fmt.Sprintf("pj: repair duplicate id %s -> %s", renames[0].OldID, strings.Join(newIDs, ", "))
}

func toRepairRows(rows []*index.Project) []repair.Row {
	out := make([]repair.Row, len(rows))
	for i, p := range rows {
		out[i] = toRepairRow(p)
	}
	return out
}

func toRepairRow(p *index.Project) repair.Row {
	return repair.Row{Path: p.Path, FullID: p.ID, ShortID: p.ShortID, OrderKey: p.OrderKey, ParseError: p.ParseError}
}

// shortIDPaths maps every short-id present in a scope's rows to the file holding it —
// the occupied set the deterministic short-id extension must avoid, carrying the paths
// re-entry needs to spot an already-extended loser. A collided short-id resolves to its
// lexicographically smallest path so dirent order never reaches a repair decision.
func shortIDPaths(rows []*index.Project) map[string]string {
	out := make(map[string]string, len(rows))
	for _, p := range rows {
		if p.ShortID == "" {
			continue
		}
		if prev, ok := out[p.ShortID]; !ok || p.Path < prev {
			out[p.ShortID] = p.Path
		}
	}
	return out
}

// genuineCollisionIDs returns the ids in scope that a real duplicate-id collision claims.
// The both-present window of an interrupted archive move is excluded: its two copies are
// byte-identical, so completing the move is exactly the right repair and nothing can be
// overwritten by it. The layout repair reads this to know which ids it must leave alone.
func (e *engine) genuineCollisionIDs(scope, dir string) (map[string]bool, error) {
	dups, err := e.db.DuplicateIDs([]string{scope})
	if err != nil || len(dups) == 0 {
		return nil, err
	}
	rows, err := e.db.ScopeProjects(scope)
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]*index.Project, len(rows))
	for _, p := range rows {
		byPath[p.Path] = p
	}
	out := map[string]bool{}
	for _, col := range dups {
		mid, err := repair.InterruptedMove(dir, toRepairRows(rowsForPaths(byPath, col.Members)))
		if err != nil {
			return nil, err
		}
		if !mid {
			out[col.Key] = true
		}
	}
	return out, nil
}

func rowsForPaths(byPath map[string]*index.Project, paths []string) []*index.Project {
	var out []*index.Project
	for _, p := range paths {
		if row, ok := byPath[p]; ok {
			out = append(out, row)
		}
	}
	return out
}

func anyParseError(rows []*index.Project) bool {
	for _, p := range rows {
		if p.ParseError {
			return true
		}
	}
	return false
}
