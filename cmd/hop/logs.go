package main

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/hopfs"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

func renderEvents(w io.Writer, events []tunnel.Event, colour bool) {
	if len(events) == 0 {
		fmt.Fprintln(w, "no events")
		return
	}
	for _, e := range events {
		line := fmt.Sprintf("%s  %-11s %s",
			e.At.Format("15:04:05"), e.State, e.Message)
		if colour && e.State == tunnel.StateFatal {
			line = paint(line, ansiRed, true)
		}
		fmt.Fprintln(w, line)
	}
}

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <name|local-port>",
		Short: "Show a tunnel's state transitions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("lines")
			follow, _ := cmd.Flags().GetBool("follow")

			paths, err := hopfs.Default()
			if err != nil {
				return err
			}

			var port int
			if isPort(args[0]) {
				if port, err = parsePort(args[0], "local-port"); err != nil {
					return err
				}
			} else {
				client, err := control.Dial(paths.ControlSock)
				if err != nil {
					return fail(exitNoDaemon, "no hop daemon is running")
				}
				list, err := client.Send(control.Request{Op: control.OpList})
				_ = client.Close()
				if err != nil {
					return fail(exitInternal, "talk to the daemon: %v", err)
				}
				if port, err = resolveTarget(paths, list.Statuses, args[0]); err != nil {
					return err
				}
			}

			seen := 0
			for {
				client, err := control.Dial(paths.ControlSock)
				if err != nil {
					return fail(exitNoDaemon, "no hop daemon is running")
				}
				resp, err := client.Send(control.Request{
					Op: control.OpEvents, LocalPort: port, Limit: limit,
				})
				_ = client.Close()
				if err != nil {
					return fail(exitInternal, "talk to the daemon: %v", err)
				}
				if !resp.OK {
					return fail(exitFatal, "%s", resp.Error)
				}

				if fresh := newEvents(resp.Events, seen); len(fresh) > 0 {
					renderEvents(cmd.OutOrStdout(), fresh, useColour(cmd))
				}
				seen = len(resp.Events)

				if !follow {
					return nil
				}
				time.Sleep(attachPoll)
			}
		},
	}
	cmd.ValidArgsFunction = completeTargets
	cmd.Flags().BoolP("follow", "f", false, "keep printing new events")
	cmd.Flags().IntP("lines", "n", 0, "show only the last N events")
	return cmd
}
