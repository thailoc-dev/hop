package main

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "hop <host> <container> <remote-port> <local-port>",
		Short: "Supervised SSH tunnels to Docker containers on remote hosts",
		Long: "Open a supervised tunnel:\n\n  " + usageForms + "\n\n" +
			"The tunnel runs in the background and survives container restarts,\n" +
			"system sleep and network changes.",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return openTunnel(cmd, args)
		},
	}
	root.PersistentFlags().Bool("json", false, "machine-readable output")
	root.PersistentFlags().Bool("no-color", false, "disable colour")
	root.PersistentFlags().BoolP("quiet", "q", false, "suppress non-error output")
	addTunnelFlags(root)

	root.AddCommand(newVersionCmd())
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newTunnelCmd())
	root.AddCommand(newLsCmd())
	root.AddCommand(newDownCmd(), newRestartCmd(), newLogsCmd())
	return root
}
