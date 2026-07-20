package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/start-cli/pj/internal/index"
	"github.com/start-cli/pj/internal/scopeconfig"
	"github.com/start-cli/pj/internal/status"
)

func newListCmd(app *App) *cobra.Command {
	var (
		scope  string
		tags   []string
		all    bool
		noLens bool
	)
	cmd := &cobra.Command{
		Use:   "list [status...] [--scope S] [--tag T]... [--all] [--no-lens]",
		Short: "Board / inventory for one scope as parse-stable TSV",
		Long: "Print one scope's projects, sorted (order, id), one TSV line each:\n" +
			"  <full-id>\\t<status>\\t<title>\\t<summary>\\t<waiting-on>\n" +
			"Bare list is the default active set. Status positionals union-filter (an\n" +
			"unknown status exits 2). --tag repeats as OR; the lens applies unless\n" +
			"--no-lens (lens AND --tag). --all includes done/backlog and archived. Lens\n" +
			"echo and integrity tokens ride stderr only, never the TSV. Pure read.",
		Args: usageArgs(cobra.ArbitraryArgs),
		RunE: func(c *cobra.Command, args []string) error {
			return runList(app, c, listParams{statuses: args, scope: scope, tags: tags, all: all, noLens: noLens})
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "scope to list (defaults to ambient; wins over ambient)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "keep projects with any of these tags (repeatable; OR)")
	cmd.Flags().BoolVar(&all, "all", false, "include done/backlog and archived projects")
	cmd.Flags().BoolVar(&noLens, "no-lens", false, "ignore the active lens")
	return cmd
}

type listParams struct {
	statuses []string
	scope    string
	tags     []string
	all      bool
	noLens   bool
}

func runList(app *App, c *cobra.Command, p listParams) error {
	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	resolved, err := e.resolveAmbient(p.scope)
	if err != nil {
		return err
	}
	scope := resolved.Name

	res, err := e.reconcile(c, map[string]string{scope: resolved.Entry.Dir})
	if err != nil {
		return err
	}
	// An unreachable scope still lists from what is indexed (rows may be stale); the
	// unreachable_scope warning already rode stderr from reconcile.
	schema := res.Schema(scope)

	statusFilter, err := parseStatusFilter(p.statuses, schema)
	if err != nil {
		return err
	}

	rows, err := e.db.ScopeProjects(scope)
	if err != nil {
		return err
	}
	gate, err := e.buildGate(res, []string{scope})
	if err != nil {
		return err
	}

	lens := e.reg.Lens[scope]
	applyLens := !p.noLens && len(lens) > 0

	var kept []*index.Project
	for _, row := range rows {
		if !listVisible(row, statusFilter, p.all, schema) {
			continue
		}
		if len(p.tags) > 0 && !matchesAnyTag(row, p.tags) {
			continue
		}
		if applyLens && !passesLens(row, lens) {
			continue
		}
		kept = append(kept, row)
	}
	sortProjects(kept)

	tokens := newTokenSet()
	for _, row := range kept {
		ds := gate.evalDepends(row)
		tokens.add(ds.Tokens)
		stdoutln(c, tsvLine(row.ID, row.Status, row.Title, row.Summary, strings.Join(ds.WaitingOn, " ")))
	}

	if applyLens {
		stderrln(c, lensEcho(lens))
	}
	for _, line := range tokens.lines() {
		stderrln(c, line)
	}
	return nil
}

// parseStatusFilter validates the status positionals against the target scope's
// known statuses (built-ins plus its customs; only built-ins when the config is
// unusable) and returns the set to union-filter on. An unknown status is a usage
// error (exit 2). An empty result means "no explicit status filter".
func parseStatusFilter(names []string, schema *scopeconfig.Schema) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, nil
	}
	custom := schemaCustom(schema)
	out := map[string]bool{}
	for _, n := range names {
		if !status.IsKnown(n, custom) {
			return nil, usageErrorf("unknown status %q for this scope", n)
		}
		out[n] = true
	}
	return out, nil
}

// listVisible applies the status/archive axes: explicit positionals select exact
// statuses; otherwise the default active set (or every status under --all). Archived
// projects appear only under --all (there is no --archived flag). Quarantined
// (parse_error) rows are never board rows — get/search locate them for repair.
func listVisible(p *index.Project, statusFilter map[string]bool, all bool, schema *scopeconfig.Schema) bool {
	// A quarantined row has no trustworthy status/title; it is located via get/search
	// (its repair surface), never surfaced as a blank board row — matching next.
	if p.ParseError {
		return false
	}
	if p.Archived && !all {
		return false
	}
	if len(statusFilter) > 0 {
		return statusFilter[p.Status]
	}
	if all {
		return true
	}
	return status.InDefaultList(p.Status, schemaCustom(schema))
}

func matchesAnyTag(p *index.Project, tags []string) bool {
	for _, want := range tags {
		for _, have := range p.Tags {
			if want == have {
				return true
			}
		}
	}
	return false
}

// sortProjects orders a board by (order, id): the order key sorts by byte value,
// which equals rank order by construction, then full id breaks ties stably.
func sortProjects(rows []*index.Project) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].OrderKey != rows[j].OrderKey {
			return rows[i].OrderKey < rows[j].OrderKey
		}
		return rows[i].ID < rows[j].ID
	})
}

// tokenSet collects diagnostic token lines in first-seen order without duplicates,
// so a condition shared by several rows rides stderr once.
type tokenSet struct {
	seen  map[string]bool
	order []string
}

func newTokenSet() *tokenSet { return &tokenSet{seen: map[string]bool{}} }

func (t *tokenSet) add(lines []string) {
	for _, l := range lines {
		if !t.seen[l] {
			t.seen[l] = true
			t.order = append(t.order, l)
		}
	}
}

func (t *tokenSet) lines() []string { return t.order }

// tsvLine joins fields into one tab-separated stdout record, neutralising any
// tab, carriage return, or newline inside a field to a single space first. A
// YAML summary or an H1 title can legitimately carry those control characters,
// and left raw they would split one record into extra columns or extra lines —
// silently breaking the parse-stable "one TSV line per project" contract every
// board reader depends on. The index keeps the raw bytes; only this output
// boundary flattens them.
func tsvLine(fields ...string) string {
	cleaned := make([]string, len(fields))
	for i, f := range fields {
		cleaned[i] = tsvSanitize(f)
	}
	return strings.Join(cleaned, "\t")
}

// tsvSanitize replaces every tab, carriage return, and newline in a TSV field
// with a single space so the field stays on its own column and line.
func tsvSanitize(field string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, field)
}
