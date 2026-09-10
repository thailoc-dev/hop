package main

import "github.com/spf13/cobra"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "hop",
		Short:         "Supervised SSH tunnels to Docker containers on remote hosts",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().Bool("json", false, "machine-readable output")
	root.PersistentFlags().Bool("no-color", false, "disable colour")
	root.PersistentFlags().BoolP("quiet", "q", false, "suppress non-error output")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newDaemonCmd())
	return root
}
