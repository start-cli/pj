package cli

import "github.com/spf13/cobra"

// newScopeCmd builds the `pj scope` command family. Scope administration is
// container management, not project work, so it groups under one noun. `pj
// scopes` aliases it, and bare `pj scope` (no subcommand) runs list.
func newScopeCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "scope",
		Aliases: []string{"scopes"},
		Short:   "Manage scopes — register, address, and inspect project containers",
		Long: "A scope is a directory of project markdown files plus its pj.cue. Scope\n" +
			"administration registers scopes on this machine, rebinds their paths, and\n" +
			"lists them. Bare `pj scope` runs `list`.",
		// Cobra dispatches a matching subcommand before this RunE, so a non-empty
		// positional here is an unknown subcommand (a mistyped verb), not list input.
		// Refuse it as a usage error rather than silently listing.
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageErrorf("unknown scope subcommand %q; run `pj scope --help` for the available subcommands", args[0])
			}
			return runScopeList(app, c)
		},
	}
	cmd.AddCommand(
		newScopeInitCmd(app),
		newScopeImportCmd(app),
		newScopeRebindCmd(app),
		newScopeForgetCmd(app),
		newScopeListCmd(app),
		newScopeRenameCmd(app),
	)
	return cmd
}
