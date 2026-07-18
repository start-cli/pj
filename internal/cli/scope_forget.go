package cli

import "github.com/spf13/cobra"

func newScopeForgetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "forget <name>",
		Short: "Unregister a scope (registry and lens entries only)",
		Long: "Drop a scope's registry and lens entries so this machine forgets it. It\n" +
			"never touches the scope's files or repo — they simply become unknown until\n" +
			"re-registered with pj scope import. Dropping any index rows is a later\n" +
			"project; forget here removes registry and lens only. A merely unreachable\n" +
			"dir stays registered until forget.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			return app.admin().Forget(args[0])
		},
	}
}
