package main

import (
	"github.com/spf13/cobra"
	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/hopfs"
)

func newDownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down <name|local-port>",
		Short: "Stop a tunnel",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if !all && len(args) != 1 {
				return fail(exitUsage, "specify a tunnel name or local port, or --all to stop every tunnel")
			}

			paths, err := hopfs.Default()
			if err != nil {
				return err
			}

			// No daemon means nothing is running, which is the requested state.
			client, err := control.Dial(paths.ControlSock)
			if err != nil {
				return nil
			}
			defer func() { _ = client.Close() }()

			req := control.Request{Op: control.OpRemove, All: all}
			if !all {
				list, err := client.Send(control.Request{Op: control.OpList})
				if err != nil {
					return fail(exitInternal, "talk to the daemon: %v", err)
				}
				port, err := resolveTarget(paths, list.Statuses, args[0])
				if err != nil {
					return err
				}
				req.LocalPort = port
				// The control protocol is one request per connection.
				_ = client.Close()
				if client, err = control.Dial(paths.ControlSock); err != nil {
					return nil
				}
			}

			resp, err := client.Send(req)
			if err != nil {
				return fail(exitInternal, "talk to the daemon: %v", err)
			}
			if !resp.OK {
				return fail(exitFatal, "%s", resp.Error)
			}
			return nil
		},
	}
	cmd.ValidArgsFunction = completeTargets
	cmd.Flags().Bool("all", false, "stop every tunnel")
	return cmd
}
