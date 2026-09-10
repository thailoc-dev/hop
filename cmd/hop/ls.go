package main

import (
	"encoding/json"
	"net"
	"strconv"
	"time"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/tunnel"
	"github.com/spf13/cobra"
)

const probeTimeout = 500 * time.Millisecond

// probeLocalPort dials the forward to find out whether it actually works. An
// ssh process that is alive is not the same as a forward that accepts
// connections, and only this answers the question the user is really asking.
func probeLocalPort(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), probeTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Short:   "List running tunnels",
		Aliases: []string{"list"},
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}

			asJSON, _ := cmd.Flags().GetBool("json")

			client, err := control.Dial(paths.ControlSock)
			if err != nil {
				// No daemon means no tunnels, which is an answer, not a failure.
				if asJSON {
					cmd.Println("[]")
					return nil
				}
				renderTable(cmd.OutOrStdout(), nil, false)
				return nil
			}
			resp, err := client.Send(control.Request{Op: control.OpList})
			_ = client.Close()
			if err != nil {
				return fail(exitInternal, "talk to the daemon: %v", err)
			}

			statuses := probeAll(paths, resp.Statuses)

			if asJSON {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(statuses)
			}
			renderTable(cmd.OutOrStdout(), statuses, useColour(cmd))
			return nil
		},
	}
}

// probeAll downgrades any tunnel claiming health that does not answer, and
// tells the daemon so it recycles rather than lying again next time.
func probeAll(paths hopfs.Paths, statuses []tunnel.Status) []tunnel.Status {
	for i, s := range statuses {
		if s.State != tunnel.StateHealthy {
			continue
		}
		if probeLocalPort(s.Spec.LocalPort) {
			continue
		}

		statuses[i].State = tunnel.StateDegraded
		if client, err := control.Dial(paths.ControlSock); err == nil {
			_, _ = client.Send(control.Request{
				Op:        control.OpMarkDegraded,
				LocalPort: s.Spec.LocalPort,
				Reason:    "local port did not accept a connection",
			})
			_ = client.Close()
		}
	}
	return statuses
}
