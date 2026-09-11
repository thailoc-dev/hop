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
			switch len(args) {
			case 0:
				return cmd.Help()
			case 1:
				return openSaved(cmd, args[0])
			default:
				return openTunnel(cmd, args)
			}
		},
	}
	root.PersistentFlags().Bool("json", false, "machine-readable output")
	root.PersistentFlags().Bool("no-color", false, "disable colour")
	root.PersistentFlags().BoolP("quiet", "q", false, "suppress non-error output")
	addTunnelFlags(root)
	root.ValidArgsFunction = completeTunnelArgs

	root.AddCommand(newVersionCmd())
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newWarmCmd())
	root.AddCommand(newTunnelCmd())
	root.AddCommand(newSaveCmd())
	root.AddCommand(newLsCmd())
	root.AddCommand(newStopCmd(), newStartCmd(), newRmCmd(), newRestartCmd(), newLogsCmd())
	root.AddCommand(newRunCmd(), newShellCmd(), newPushCmd(), newPullCmd(), newDockerIPCmd())
	return root
}
