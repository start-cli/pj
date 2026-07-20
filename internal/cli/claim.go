package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/status"
)

// runClaim is pj next --claim: the complete-state write that reuses next's selection
// and eligibility to start work. Under the scope flock for the whole span it
// reconciles the ambient scope plus its transitive depends-closure, walks the same
// candidates in the same order, and claims the first that still validates as eligible
// from trusted on-disk state — skipping any that lost the race, collides on a
// duplicate id, or is in parse_error quarantine. It writes status: in-progress only
// (no archive move), self-commits on an auto-commit git-root, and prints the path.
func runClaim(app *App, c *cobra.Command, scopeFlag string, noLens bool) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return err
	}
	scope := resolved.Name
	dir := resolved.Entry.Dir

	lock, err := acquireScopeLock(dir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	ctx := c.Context()
	res, targets, err := e.reconcileClosureResult(scope, dir)
	if err != nil {
		return err
	}
	// The ambient scope's own config must be usable to validate and write. A
	// depended-on sibling with a bad config only holds its gates and rides its warning
	// below; it does not refuse the claim.
	if err := refuseUnusableScope(res, scope, dir); err != nil {
		return err
	}
	schema := res.Schema(scope)
	autoCommit := schemaAutoCommit(schema)
	root, hasRoot := gitRootFor(dir)
	if err := checkMidRebase(ctx, scope, autoCommit, root, hasRoot); err != nil {
		return err
	}
	e.printWarnings(c, res.Warnings)

	gate, err := e.buildGate(res, targets)
	if err != nil {
		return err
	}
	rows, err := e.db.ScopeProjects(scope)
	if err != nil {
		return err
	}
	candidates := nextCandidates(rows)
	sortProjects(candidates)

	lens := e.reg.Lens[scope]
	applyLens := !noLens && len(lens) > 0

	tokens := newTokenSet()
	blocked, readyOutsideLens := 0, 0
	var claimed *index.Project
	for _, p := range candidates {
		ds := gate.evalDepends(p)
		tokens.add(ds.Tokens)
		if !gate.nextEligible(p, ds) {
			if ds.Held() {
				blocked++
			}
			continue
		}
		if applyLens && !passesLens(p, lens) {
			readyOutsideLens++
			continue
		}
		ok, err := e.claimProject(ctx, c, scope, dir, autoCommit, p, root, hasRoot)
		if err != nil {
			return err
		}
		if ok {
			claimed = p
			break
		}
		// Lost the race (re-validation failed): skip to the next eligible candidate.
	}

	if applyLens {
		stderrln(c, lensEcho(lens))
	}
	for _, line := range tokens.lines() {
		stderrln(c, line)
	}

	if claimed != nil {
		out, err := absPath(claimed.Path)
		if err != nil {
			return err
		}
		stdoutln(c, out)
		return nil
	}
	return emptyQueueError(applyLens, lens, blocked, readyOutsideLens)
}

// claimProject re-validates one candidate from trusted on-disk state under the flock
// and, if it is still a next-eligible todo at the dir root, writes status:
// in-progress, write-throughs the index, and self-commits when available. It returns
// ok=false without writing when the candidate lost the race (already claimed, status
// changed, or file unreadable), so the caller moves to the next candidate. A write or
// commit failure after the point of no return is returned as an error.
func (e *engine) claimProject(ctx context.Context, c *cobra.Command, scope, dir string, autoCommit bool, p *index.Project, root string, hasRoot bool) (bool, error) {
	m, body, err := readProjectFile(p.Path)
	if err != nil {
		return false, nil
	}
	if m.Status != status.Todo {
		return false, nil
	}
	m.Status = status.InProgress
	if err := writeProjectFile(p.Path, m, body); err != nil {
		return false, err
	}
	if err := e.rec.SyncPaths(scope, writtenPaths(p.Path, "")); err != nil {
		return false, err
	}
	message := fmt.Sprintf("pj: %s -> %s", p.ID, status.InProgress)
	if err := e.completeStateDurability(ctx, c, scope, dir, autoCommit, message, p.Path, "", root, hasRoot); err != nil {
		return false, err
	}
	return true, nil
}
