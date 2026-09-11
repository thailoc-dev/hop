package main

import (
	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/store"
	"github.com/locnguyen/hop/internal/tunnel"
	"github.com/spf13/cobra"
)

// newestStatus picks the most recently OPENED tunnel — by CreatedAt, which
// never moves, not by Since, which resets on every reconnect.
func newestStatus(statuses []tunnel.Status) (tunnel.Status, bool) {
	var best tunnel.Status
	found := false
	for _, st := range statuses {
		if !found || st.CreatedAt.After(best.CreatedAt) {
			best, found = st, true
		}
	}
	return best, found
}

func newSaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "save <name> [local-port]",
		Short: "Save a running tunnel under a name",
		Long: "Saves a running tunnel so it can be reopened with `hop <name>`.\n" +
			"With no port, the most recently opened tunnel is saved.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			// Validate before contacting the daemon, so a bad name costs nothing.
			if err := validateName(name); err != nil {
				return err
			}

			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			client, err := control.Dial(paths.ControlSock)
			if err != nil {
				return fail(exitUsage, "no tunnels are running; open one first, then save it")
			}
			resp, err := client.Send(control.Request{Op: control.OpList})
			_ = client.Close()
			if err != nil {
				return fail(exitInternal, "talk to the daemon: %v", err)
			}

			var chosen tunnel.Status
			if len(args) == 2 {
				port, err := parsePort(args[1], "local-port")
				if err != nil {
					return err
				}
				found := false
				for _, st := range resp.Statuses {
					if st.Spec.LocalPort == port {
						chosen, found = st, true
					}
				}
				if !found {
					return fail(exitFatal, "no tunnel on local port %d", port)
				}
			} else {
				st, ok := newestStatus(resp.Statuses)
				if !ok {
					return fail(exitUsage, "no tunnels are running; open one first, then save it")
				}
				chosen = st
			}

			if err := saveToCatalogue(paths, name, chosen.Spec); err != nil {
				return err
			}
			if quiet, _ := cmd.Flags().GetBool("quiet"); !quiet {
				cmd.Printf("saved %s: %s %s %d %d\n", name,
					chosen.Spec.Host, chosen.Spec.Container, chosen.Spec.RemotePort, chosen.Spec.LocalPort)
			}
			return nil
		},
	}
}

func newForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget <name>",
		Short: "Remove a saved tunnel from the catalogue",
		Long: "Removes the name. A running instance of the tunnel keeps running;\n" +
			"forgetting is about the future, not the present.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			c, err := store.LoadCatalogue(paths.CatalogueFile)
			if err != nil {
				return err
			}
			if _, ok := c.Tunnels[name]; !ok {
				return fail(exitUsage, "no saved tunnel named %q", name)
			}
			delete(c.Tunnels, name)
			return store.SaveCatalogue(paths.CatalogueFile, c)
		},
	}
}
