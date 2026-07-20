// Package reconcile is pj's read-through: it brings the derived SQLite index into
// agreement with the project files before a command reads it, git-free. It stats
// each target scope's dir root and archive/ children, reparses only what changed
// (with a git-style racy-index guard), quarantines unparseable files instead of
// failing, isolates unreachable scopes instead of dropping their rows, prunes
// scopes the registry has forgotten, and caches each scope's pj.cue evaluation
// keyed by its import closure. After reconciling it runs cheap warn-only integrity
// aggregates. It never mutates a project file — detection here, repair elsewhere.
package reconcile

import (
	"fmt"
	"sort"

	"cuelang.org/go/cue"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/status"
	"github.com/start-cli/pj/internal/token"
)

// Reconciler reconciles scopes into an open index using a shared CUE context.
type Reconciler struct {
	db  *index.DB
	ctx *cue.Context
}

// New builds a Reconciler over an open index and the process-wide CUE context.
func New(db *index.DB, ctx *cue.Context) *Reconciler {
	return &Reconciler{db: db, ctx: ctx}
}

// Result carries what a reconcile learned that the verbs and the CLI need: each
// target scope's evaluated schema (nil when its config is unusable), the config
// errors, the set of scopes skipped as unreachable, and the ordered token lines to
// ride stderr (config_unparseable, unreachable_scope, parse_error count, and the
// integrity aggregates).
type Result struct {
	Schemas     map[string]*scopeconfig.Schema
	ConfigErrs  map[string]*scopeconfig.ConfigError
	Unreachable map[string]bool
	Warnings    []string
}

// Schema returns the evaluated schema for a reconciled scope, or nil when the scope
// was not reconciled or its config is unusable. Verbs use it for known-status,
// terminal, and lens rules; nil means fall back to built-ins only.
func (res *Result) Schema(scope string) *scopeconfig.Schema {
	if res == nil {
		return nil
	}
	return res.Schemas[scope]
}

// Merge folds another batch's row-reconcile result into res: schemas, config
// errors, unreachable flags, and warning lines. It is how the cross-scope closure
// walk accumulates its per-batch ReconcileRows results into one before the single
// aggregate warning pass. Each scope appears in exactly one batch, so the per-scope
// maps never collide; warnings concatenate in discovery order.
func (res *Result) Merge(other *Result) {
	if other == nil {
		return
	}
	for k, v := range other.Schemas {
		res.Schemas[k] = v
	}
	for k, v := range other.ConfigErrs {
		res.ConfigErrs[k] = v
	}
	for k, v := range other.Unreachable {
		res.Unreachable[k] = v
	}
	res.Warnings = append(res.Warnings, other.Warnings...)
}

// NewResult builds an empty result with its maps ready to populate. The
// cross-scope closure walk uses it as the accumulator it Merges each batch into.
func NewResult() *Result {
	return &Result{
		Schemas:     map[string]*scopeconfig.Schema{},
		ConfigErrs:  map[string]*scopeconfig.ConfigError{},
		Unreachable: map[string]bool{},
	}
}

// Reconcile brings the given target scopes (name -> dir) into agreement with disk,
// prunes any indexed scope not in registered (P2 forget's deferred index drop),
// runs integrity detection over the reconciled scopes, and returns their schemas
// and the stderr token lines. now is the reconcile timestamp (unix nanoseconds),
// read at the CLI edge so the racy-index rule and last-index are deterministic.
func (r *Reconciler) Reconcile(targets map[string]string, registered map[string]bool, now int64) (*Result, error) {
	res, reconciled, err := r.ReconcileRows(targets, registered, now)
	if err != nil {
		return nil, err
	}
	if err := r.AppendAggregates(reconciled, res); err != nil {
		return nil, err
	}
	return res, nil
}

// ReconcileRows performs the row-and-schema half of a reconcile: prune forgotten
// scopes, bring each target's rows into agreement with disk, evaluate its schema,
// and ride the per-scope config_unparseable / unreachable_scope warnings. It stops
// short of the post-reconcile aggregates (parse_error count and the integrity
// tokens), returning the reconciled (reachable) scope names so a caller can run
// those once over a larger set. Single-scope verbs use Reconcile, which chains this
// with AppendAggregates; the cross-scope closure walk reconciles rows batch by batch
// here and aggregates once at the end, so the closure is scanned exactly once.
func (r *Reconciler) ReconcileRows(targets map[string]string, registered map[string]bool, now int64) (*Result, []string, error) {
	if err := r.pruneForgotten(registered); err != nil {
		return nil, nil, err
	}

	res := NewResult()
	names := sortedKeys(targets)
	var reconciled []string
	for _, name := range names {
		// Reachability is decided first: an unreachable dir makes pj.cue equally
		// unreadable, so evaluating (and warning about) its config would ride a
		// misleading config_unparseable alongside unreachable_scope. Design fixes one
		// token per dir-not-usable mode, so an unreachable scope skips config eval
		// entirely and rides unreachable_scope only.
		reachable, err := r.reconcileScope(name, targets[name], now)
		if err != nil {
			return nil, nil, err
		}
		if !reachable {
			res.Unreachable[name] = true
			res.Warnings = append(res.Warnings, token.Line(token.UnreachableScope, fmt.Sprintf("%s: dir %s is not reachable — rows left in place", name, targets[name])))
			continue
		}

		schema, cfgErr := r.schemaFor(name, targets[name])
		res.Schemas[name] = schema
		if cfgErr != nil {
			res.ConfigErrs[name] = cfgErr
			res.Warnings = append(res.Warnings, token.Line(token.ConfigUnparseable, fmt.Sprintf("%s: %s", name, cfgErr.Reason)))
		}
		reconciled = append(reconciled, name)
	}
	return res, reconciled, nil
}

// AppendAggregates rides the post-reconcile ride-along warnings for an
// already-reconciled scope set onto res: the terse parse_error count and the
// warn-only integrity tokens (duplicate_id, equal_order, archive drift). It only
// reads materialized rows — no re-stat, no re-parse — so a caller can invoke it once
// over the full closure after reconciling the rows in batches.
func (r *Reconciler) AppendAggregates(scopes []string, res *Result) error {
	if err := r.appendParseErrorWarning(scopes, res); err != nil {
		return err
	}
	return r.appendIntegrityWarnings(scopes, res)
}

// pruneForgotten drops every trace of an indexed scope the registry no longer
// knows. It runs on each reconcile so a pj scope forget is realised the next time
// any command touches the index, without a dedicated cleanup pass.
func (r *Reconciler) pruneForgotten(registered map[string]bool) error {
	indexed, err := r.db.IndexedScopes()
	if err != nil {
		return err
	}
	for scope := range indexed {
		if !registered[scope] {
			if err := r.db.DeleteScope(scope); err != nil {
				return err
			}
		}
	}
	return nil
}

// reconcileScope is layer 1 for one scope: stat the dir root and archive children,
// reparse changed/new/racy files, delete rows for vanished files, and stamp the
// last-index timestamp. reachable is false when the dir cannot be listed, and the
// scope's rows are then left untouched.
func (r *Reconciler) reconcileScope(name, dir string, now int64) (reachable bool, err error) {
	files, ok := statScope(name, dir)
	if !ok {
		return false, nil
	}

	lastIndex, err := r.db.LastIndex(name)
	if err != nil {
		return false, err
	}
	existing, err := r.db.ScopeRows(name)
	if err != nil {
		return false, err
	}

	for path, st := range files {
		prev, seen := existing[path]
		// The racy-index rule (mtime >= last-index is dirty) closes the same-tick
		// hole where a file edited in the reconcile tick would look unchanged.
		if seen && prev.MtimeNS == st.MtimeNS && prev.Size == st.Size && st.MtimeNS < lastIndex {
			continue
		}
		p, edges, err := parseFile(path, name, st.FullID, st.Archived, st.MtimeNS, st.Size)
		if err != nil {
			// A read error on a listed file is transient I/O, not bad data: skip it
			// this pass rather than dropping or quarantining a row we could not read.
			continue
		}
		if err := r.db.UpsertProjectWithEdges(p, edges); err != nil {
			return false, err
		}
	}

	for path := range existing {
		if _, stillThere := files[path]; !stillThere {
			if err := r.db.DeleteByPath(path); err != nil {
				return false, err
			}
		}
	}

	if err := r.db.SetLastIndex(name, now); err != nil {
		return false, err
	}
	return true, nil
}

// appendParseErrorWarning rides a terse count of quarantined projects across the
// reconciled scopes. The per-project parse_error: <id>: <message> line is added by
// the id-taking verbs (get/meta); this is the board-verb summary.
func (r *Reconciler) appendParseErrorWarning(scopes []string, res *Result) error {
	if len(scopes) == 0 {
		return nil
	}
	n, err := r.db.ParseErrorCount(scopes)
	if err != nil {
		return err
	}
	if n > 0 {
		res.Warnings = append(res.Warnings, token.Line(token.ParseError, fmt.Sprintf("%d unparseable", n)))
	}
	return nil
}

// appendIntegrityWarnings runs the cheap post-reconcile aggregates (duplicate ids,
// equal order keys, archive layout drift) over the reconciled scopes and rides
// their stable tokens. It never mutates a file — repair is P5/P6.
func (r *Reconciler) appendIntegrityWarnings(scopes []string, res *Result) error {
	if len(scopes) == 0 {
		return nil
	}
	dups, err := r.db.DuplicateIDs(scopes)
	if err != nil {
		return err
	}
	for _, c := range dups {
		res.Warnings = append(res.Warnings, token.Line(token.DuplicateID,
			fmt.Sprintf("%s claimed by %s", c.Key, joinPaths(c.Members))))
	}

	eq, err := r.db.EqualOrders(scopes)
	if err != nil {
		return err
	}
	for _, c := range eq {
		res.Warnings = append(res.Warnings, token.Line(token.EqualOrder,
			fmt.Sprintf("%s order %q shared by %s", c.Scope, c.Key, joinPaths(c.Members))))
	}

	return r.appendArchiveDrift(scopes, res)
}

// appendArchiveDrift flags location-vs-status disagreement: a non-terminal project
// under archive/, or a terminal project still at the dir root. Terminal-ness is per
// scope (a custom done-category counts), so it consults each scope's schema; a scope
// with an unusable config falls back to the built-in terminal set.
func (r *Reconciler) appendArchiveDrift(scopes []string, res *Result) error {
	for _, scope := range scopes {
		rows, err := r.db.ScopeProjects(scope)
		if err != nil {
			return err
		}
		custom := customCategories(res.Schema(scope))
		for _, p := range rows {
			if p.ParseError {
				continue
			}
			terminal := status.IsTerminal(p.Status, custom)
			switch {
			case p.Archived && !terminal:
				res.Warnings = append(res.Warnings, token.Line(token.ArchiveNonTerminal,
					fmt.Sprintf("%s is %s under archive/ (%s)", p.ID, p.Status, p.Path)))
			case !p.Archived && terminal:
				res.Warnings = append(res.Warnings, token.Line(token.ArchiveTerminalAtRoot,
					fmt.Sprintf("%s is %s at dir root (%s)", p.ID, p.Status, p.Path)))
			}
		}
	}
	return nil
}

// customCategories projects a schema's custom statuses into the map the status
// predicates take, or nil for a scope with no usable schema (built-ins only).
func customCategories(s *scopeconfig.Schema) map[string]status.Category {
	if s == nil {
		return nil
	}
	return s.Statuses
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinPaths(paths []string) string {
	s := ""
	for i, p := range paths {
		if i > 0 {
			s += ", "
		}
		s += p
	}
	return s
}
