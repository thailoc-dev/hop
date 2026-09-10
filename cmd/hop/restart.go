package main

import (
	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		ValidArgsFunction: completeLocalPorts,
		Use:               "restart <local-port>",
		Short:             "Rebuild a tunnel, re-resolving the container address",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := parsePort(args[0], "local-port")
			if err != nil {
				return err
			}

			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			client, err := connect(paths)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			resp, err := client.Send(control.Request{Op: control.OpRestart, LocalPort: port})
			if err != nil {
				return fail(exitInternal, "talk to the daemon: %v", err)
			}
			if !resp.OK {
				return fail(exitFatal, "%s", resp.Error)
			}
			return nil
		},
	}
}
