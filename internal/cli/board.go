package cli

import (
	"sort"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/reconcile"
	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/status"
	"github.com/start-cli/pj/internal/token"
)

// gate is the depends-gating view the board reads on: the machine-wide project
// index (so a cross-scope target's terminal-ness is one lookup), each project's
// depends edges, per-scope schemas for terminal decisions, and the duplicate-id set
// for the home scopes. It is the single place list's waiting-on and next's
// eligibility are computed, so P4's next --claim can reuse the same rules.
type gate struct {
	e       *engine
	byID    map[string][]*index.Project
	depends map[string][]string
	schemas map[string]*scopeconfig.Schema
	dupSet  map[string]bool
}

// buildGate assembles the gate over the reconciled state. homeScopes are the scopes
// whose projects are being listed/selected; the duplicate-id set is scoped to them.
func (e *engine) buildGate(res *reconcile.Result, homeScopes []string) (*gate, error) {
	all, err := e.db.AllProjects()
	if err != nil {
		return nil, err
	}
	edges, err := e.db.AllEdges()
	if err != nil {
		return nil, err
	}
	dup, err := e.db.DuplicateIDSet(homeScopes)
	if err != nil {
		return nil, err
	}

	g := &gate{
		e:       e,
		byID:    map[string][]*index.Project{},
		depends: map[string][]string{},
		schemas: map[string]*scopeconfig.Schema{},
		dupSet:  dup,
	}
	for _, p := range all {
		g.byID[p.ID] = append(g.byID[p.ID], p)
	}
	for _, ed := range edges {
		if ed.Kind == index.EdgeDepends {
			g.depends[ed.FromPath] = append(g.depends[ed.FromPath], ed.ToID)
		}
	}
	for name, s := range res.Schemas {
		g.schemas[name] = s
	}
	return g, nil
}

// depStatus is the outcome of evaluating a project's depends gate.
type depStatus struct {
	// WaitingOn is the sorted set of unmet direct depends full ids — non-terminal,
	// dangling, or unresolvable targets. Malformed entries are not full ids and are
	// excluded (the schema_error token is their signal).
	WaitingOn []string
	// Tokens are the stderr diagnostic lines this project's edges produced.
	Tokens []string
	// SchemaError is true when the project carries a malformed depends/related entry.
	SchemaError bool
}

// Held reports whether the project is held out of next by its dependencies: any
// unmet depend, or a malformed edge.
func (d depStatus) Held() bool { return len(d.WaitingOn) > 0 || d.SchemaError }

// evalDepends computes a project's gate: its unmet depends (waiting-on) and the
// tokens they ride. A same-scope missing target is a hard dangle
// (depends_dangling); a cross-scope one an informational hold
// (depends_unresolvable); a non-terminal target is a plain wait; a malformed edge
// on the project rides schema_error and holds it.
func (g *gate) evalDepends(p *index.Project) depStatus {
	var ds depStatus
	if p.SchemaError {
		ds.SchemaError = true
		ds.Tokens = append(ds.Tokens, token.Line(token.SchemaError,
			p.ID+": a depends/related entry is not a legal full project id"))
	}

	seen := map[string]bool{}
	for _, target := range g.depends[p.Path] {
		if seen[target] {
			continue
		}
		seen[target] = true

		rows := g.byID[target]
		if len(rows) == 0 {
			if scopeOfFullID(target) == p.Scope {
				ds.Tokens = append(ds.Tokens, token.Line(token.DependsDangling,
					p.ID+" depends on "+target+" which has no project in this scope"))
			} else {
				ds.Tokens = append(ds.Tokens, token.Line(token.DependsUnresolvable,
					p.ID+" depends on "+target+" which cannot be resolved here"))
			}
			ds.WaitingOn = append(ds.WaitingOn, target)
			continue
		}
		if !g.allTerminal(rows) {
			ds.WaitingOn = append(ds.WaitingOn, target)
		}
	}
	sort.Strings(ds.WaitingOn)
	return ds
}

// allTerminal reports whether every row for a depends target is terminal. Held-not-
// surfaced: a prerequisite that cannot be confirmed done (ambiguous under a
// duplicate id, or any non-terminal member) holds rather than falsely satisfying.
func (g *gate) allTerminal(rows []*index.Project) bool {
	for _, r := range rows {
		if !status.IsTerminal(r.Status, schemaCustom(g.schema(r.Scope))) {
			return false
		}
	}
	return len(rows) > 0
}

// nextEligible reports whether a project can be returned by next: built-in todo, at
// the dir root (never under archive/), not a duplicate-id collision, no malformed
// edge, and every depend terminal. The lens is applied by the caller.
func (g *gate) nextEligible(p *index.Project, ds depStatus) bool {
	if !status.IsNextEligible(p.Status) || p.Archived || p.ParseError {
		return false
	}
	if g.isDuplicate(p) || ds.Held() {
		return false
	}
	return true
}

// isDuplicate reports whether a project's id is in a duplicate_id collision set.
func (g *gate) isDuplicate(p *index.Project) bool {
	return g.dupSet[p.Scope+"\x00"+p.ID]
}

// schema returns a scope's schema, resolving lazily via the cache for a scope not
// in the reconciled set (a cross-scope depends target's scope), and caching the
// result — including a nil for an unusable or unregistered scope.
func (g *gate) schema(scope string) *scopeconfig.Schema {
	if s, ok := g.schemas[scope]; ok {
		return s
	}
	var s *scopeconfig.Schema
	if entry, ok := g.e.reg.Scopes[scope]; ok {
		s = g.e.rec.SchemaCached(scope, entry.Dir)
	}
	g.schemas[scope] = s
	return s
}

func schemaCustom(s *scopeconfig.Schema) map[string]status.Category {
	if s == nil {
		return nil
	}
	return s.Statuses
}
