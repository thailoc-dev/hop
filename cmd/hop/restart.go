package main

import (
	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		ValidArgsFunction: completeTargets,
		Use:               "restart <name|local-port>",
		Short:             "Rebuild a tunnel, re-resolving the container address",
		Args:              cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			client, err := connect(paths)
			if err != nil {
				return err
			}
			list, err := client.Send(control.Request{Op: control.OpList})
			_ = client.Close()
			if err != nil {
				return fail(exitInternal, "talk to the daemon: %v", err)
			}
			port, err := resolveTarget(paths, list.Statuses, args[0])
			if err != nil {
				return err
			}

			// The control protocol is one request per connection.
			client, err = connect(paths)
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
