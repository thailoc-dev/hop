package main

import "github.com/spf13/cobra"

// Version is overridden at build time with -ldflags "-X main.Version=...".
var Version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the hop version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("hop %s\n", Version)
			return nil
		},
	}
}
