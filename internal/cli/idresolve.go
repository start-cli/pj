package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/reconcile"
	"github.com/start-cli/pj/internal/token"
)

// resolution is the outcome of resolving an id argument to project rows: the scope
// it resolved in, the matching rows (more than one only under a duplicate_id
// collision), and the reconcile result so a verb can reuse the schema and let the
// warnings ride.
type resolution struct {
	scope string
	rows  []*index.Project
	res   *reconcile.Result
}

// resolveProject is the shared id resolution for get, meta, deps, and edit (and, in
// P4, the id-taking mutators). It classifies the argument, resolves the target
// scope (the id's own scope for a full id; the ambient scope for a short id),
// reconciles that scope, and returns the matching rows. A malformed id is a usage
// error (exit 2); every other failure — no ambient scope for a short id, an
// unregistered scope, an unknown-but-well-formed id — is generic non-zero.
func (e *engine) resolveProject(c *cobra.Command, idArg, scopeFlag string) (*resolution, error) {
	form, ok := parseIDArg(idArg)
	if !ok {
		return nil, usageErrorf("%q is not a valid project id", idArg)
	}

	scope, err := e.scopeForID(idArg, form, scopeFlag)
	if err != nil {
		return nil, err
	}
	entry, registered := e.reg.Scopes[scope]
	if !registered {
		return nil, fmt.Errorf("unknown project id %q: scope %q is not registered here", idArg, scope)
	}

	// Reconcile without printing yet: a duplicate-id resolution refuses with its own
	// actionable duplicate_id line, so the generic integrity echo for that id must be
	// suppressed before the rest of the warnings ride stderr.
	res, err := e.reconcileResult(map[string]string{scope: entry.Dir})
	if err != nil {
		return nil, err
	}
	if res.Unreachable[scope] {
		e.printWarnings(c, res.Warnings)
		return nil, fmt.Errorf("cannot resolve %q: scope %q is not reachable", idArg, scope)
	}

	var rows []*index.Project
	switch form {
	case idFull:
		rows, err = e.db.ProjectsByID(scope, idArg)
	default:
		rows, err = e.db.ProjectsByShortID(scope, idArg)
	}
	if err != nil {
		e.printWarnings(c, res.Warnings)
		return nil, err
	}
	if len(rows) == 0 {
		e.printWarnings(c, res.Warnings)
		return nil, fmt.Errorf("unknown project id %q", idArg)
	}

	warnings := res.Warnings
	if len(rows) > 1 {
		warnings = suppressDuplicateID(warnings, rows[0].ID)
	}
	e.printWarnings(c, warnings)
	return &resolution{scope: scope, rows: rows, res: res}, nil
}

// suppressDuplicateID drops reconcile's duplicate_id integrity warning for id, so a
// verb that refuses with its own actionable duplicate_id line does not echo the same
// condition twice. Other ids' duplicate warnings are left in place.
func suppressDuplicateID(warnings []string, id string) []string {
	prefix := token.Line(token.DuplicateID, id+" claimed by ")
	var out []string
	for _, w := range warnings {
		if strings.HasPrefix(w, prefix) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// scopeForID picks the scope an id resolves in: a full id carries its own scope; a
// short id needs the ambient scope (--scope / PJ_SCOPE / code-root).
func (e *engine) scopeForID(idArg string, form idForm, scopeFlag string) (string, error) {
	if form == idFull {
		return scopeOfFullID(idArg), nil
	}
	resolved, err := e.resolveAmbient(scopeFlag)
	if err != nil {
		return "", err
	}
	return resolved.Name, nil
}

// duplicateRefusal formats the duplicate_id refusal for an id resolved to more than
// one file: non-zero, no path on stdout, naming the id and every colliding path.
func duplicateRefusal(rows []*index.Project) error {
	paths := make([]string, len(rows))
	for i, r := range rows {
		paths[i] = r.Path
	}
	return fmt.Errorf("%s", token.Line(token.DuplicateID,
		fmt.Sprintf("%s is claimed by %d files: %s — resolve with pj doctor --repair", rows[0].ID, len(rows), joinComma(paths))))
}

// scopeOfFullID returns the scope prefix of a validated full id.
func scopeOfFullID(fullID string) string {
	for i := 0; i < len(fullID); i++ {
		if fullID[i] == '-' {
			return fullID[:i]
		}
	}
	return fullID
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
