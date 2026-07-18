package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

func newScopeListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every registered scope as parse-stable TSV",
		Long: "Print one line per registered scope, sorted by name ascending:\n" +
			"  <name>\\t<dir>\\t<root>\\t<mode>\n" +
			"name/dir/root are pure registry reads (cleaned absolute paths); mode stats\n" +
			"each dir and derives its git-root — pj-driven, repo-driven, plain-files, or\n" +
			"unknown — so one bad scope never fails the listing. Soft tokens\n" +
			"(name_drift, unreachable_scope, config_unparseable) ride stderr. An empty\n" +
			"registry exits 0 with empty stdout.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			return runScopeList(app, c)
		},
	}
}

// runScopeList is shared by `pj scope list` and bare `pj scope`. TSV rows go to
// stdout (never coloured, never interleaved with diagnostics); soft tokens go to
// stderr. It returns nil even when diagnostics are present — a bad scope is a
// stderr note, not a failed listing.
func runScopeList(app *App, c *cobra.Command) error {
	listing, err := app.admin().List()
	if err != nil {
		return err
	}
	for _, d := range listing.Diagnostics {
		stderrln(c, d)
	}
	for _, r := range listing.Rows {
		stdoutln(c, strings.Join([]string{r.Name, r.Dir, r.Root, r.Mode}, "\t"))
	}
	return nil
}
