package main

import (
	"github.com/spf13/cobra"
	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/hopfs"
	"github.com/thailoc-dev/hop/internal/store"
)

func newRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
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
			c, err := store.LoadCatalogue(paths.CatalogueFile)
			if err != nil {
				return err
			}
			// Match against what is actually running -- by name or by port --
			// rather than accepting any port number at face value.
			st, running := findRunning(list.Statuses, c.Tunnels, args[0])
			if !running {
				// Not running. If it is saved, restart means start -- as it does
				// for a stopped docker container.
				if spec, ok := findSaved(c.Tunnels, args[0]); ok {
					return openSaved(cmd, spec.Name)
				}
				return fail(exitFatal, "no tunnel named or on port %q", args[0])
			}
			port := st.Spec.LocalPort

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
	// A stopped tunnel is started, so restart takes the same flags as open.
	addTunnelFlags(cmd)
	return cmd
}
