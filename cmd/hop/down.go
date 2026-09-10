package main

import (
	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/spf13/cobra"
)

func newDownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down <local-port>",
		Short: "Stop a tunnel",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if !all && len(args) != 1 {
				return fail(exitUsage, "specify a local port, or --all to stop every tunnel")
			}

			req := control.Request{Op: control.OpRemove, All: all}
			if !all {
				port, err := parsePort(args[0], "local-port")
				if err != nil {
					return err
				}
				req.LocalPort = port
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
	cmd.Flags().Bool("all", false, "stop every tunnel")
	return cmd
}
