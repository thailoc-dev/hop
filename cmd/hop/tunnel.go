package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/tunnel"
	"github.com/spf13/cobra"
)

// reservedWords are the first arguments that mean a subcommand rather than a
// hostname. See the spec's disambiguation rule.
var reservedWords = map[string]bool{
	"ls": true, "down": true, "logs": true, "restart": true, "tunnel": true,
	"run": true, "shell": true, "push": true, "pull": true, "docker-ip": true,
	"version": true, "help": true, "completion": true,
}

const usageForms = "hop <host> <container> <remote-port> <local-port>\n" +
	"       hop ls | down <local-port> | logs <local-port> | restart <local-port>"

func parsePort(value, name string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fail(exitUsage, "%s must be a number, got %q", name, value)
	}
	if port < 1 || port > 65535 {
		return 0, fail(exitUsage, "%s must be between 1 and 65535, got %d", name, port)
	}
	return port, nil
}

func parseTunnelArgs(args []string) (tunnel.Spec, error) {
	if len(args) != 4 {
		return tunnel.Spec{}, fail(exitUsage,
			"expected 4 arguments, got %d\n\nUsage: %s", len(args), usageForms)
	}

	remotePort, err := parsePort(args[2], "remote-port")
	if err != nil {
		return tunnel.Spec{}, err
	}
	localPort, err := parsePort(args[3], "local-port")
	if err != nil {
		return tunnel.Spec{}, err
	}

	return tunnel.Spec{
		Host:       args[0],
		Container:  args[1],
		RemotePort: remotePort,
		LocalPort:  localPort,
		Env:        inferEnv(args[0], args[1]),
	}, nil
}

// openTunnel is the body of both the bare form and `hop tunnel`.
func openTunnel(cmd *cobra.Command, args []string) error {
	spec, err := parseTunnelArgs(args)
	if err != nil {
		return err
	}
	if override, _ := cmd.Flags().GetString("env"); override != "" {
		spec.Env = override
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

	resp, err := client.Send(control.Request{Op: control.OpAdd, Spec: &spec})
	if err != nil {
		return fail(exitInternal, "talk to the daemon: %v", err)
	}
	if !resp.OK {
		return fail(exitFatal, "%s", resp.Error)
	}

	wait, _ := cmd.Flags().GetDuration("wait")
	status, err := waitForHealthy(paths, spec.LocalPort, wait)
	if err != nil {
		return err
	}

	quiet, _ := cmd.Flags().GetBool("quiet")
	if !quiet {
		cmd.Print(renderOpened(cmd, status))
	}

	if attach, _ := cmd.Flags().GetBool("attach"); attach {
		return attachTunnel(cmd, paths, spec.LocalPort)
	}

	if status.State != tunnel.StateHealthy {
		return fail(exitNotReady,
			"tunnel is %s after %s; it keeps retrying in the background (hop logs %d)",
			status.State, wait, spec.LocalPort)
	}
	return nil
}

// waitForHealthy polls the daemon until the tunnel is healthy, fatal, or the
// deadline passes. Polling is fine here: it is bounded, and it keeps the
// control protocol to a single request/response shape.
func waitForHealthy(paths hopfs.Paths, localPort int, wait time.Duration) (tunnel.Status, error) {
	deadline := time.Now().Add(wait)
	var last tunnel.Status

	for {
		client, err := connect(paths)
		if err != nil {
			return last, err
		}
		resp, err := client.Send(control.Request{Op: control.OpList})
		_ = client.Close()
		if err != nil {
			return last, fail(exitInternal, "talk to the daemon: %v", err)
		}

		for _, status := range resp.Statuses {
			if status.Spec.LocalPort != localPort {
				continue
			}
			last = status
			switch status.State {
			case tunnel.StateHealthy:
				return status, nil
			case tunnel.StateFatal:
				return status, fail(exitFatal, "%s", status.LastError)
			}
		}

		if !time.Now().Before(deadline) {
			return last, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func renderOpened(cmd *cobra.Command, status tunnel.Status) string {
	label := envLabel(cmd, status.Spec.Env)
	return fmt.Sprintf("  %s %s  →  localhost:%d   %s\n",
		label, status.Spec.Container, status.Spec.LocalPort, status.State)
}

// attachPoll is how often an attached tunnel asks for new events.
const attachPoll = time.Second

// newEvents returns the slice of events not yet shown. It is shared by
// --attach and `hop logs -f`, so the two cannot drift apart.
func newEvents(all []tunnel.Event, seen int) []tunnel.Event {
	if seen >= len(all) {
		return nil
	}
	return all[seen:]
}

// attachTunnel streams state changes until interrupted, then stops the tunnel.
//
// The daemon still owns the tunnel while attached, so a crash of this process
// does not orphan it; ^C is what stops it, matching the old script's feel.
func attachTunnel(cmd *cobra.Command, paths hopfs.Paths, localPort int) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	seen := 0
	for {
		client, err := control.Dial(paths.ControlSock)
		if err != nil {
			return fail(exitNoDaemon, "the daemon stopped")
		}
		resp, err := client.Send(control.Request{Op: control.OpEvents, LocalPort: localPort})
		_ = client.Close()
		if err != nil {
			return fail(exitInternal, "talk to the daemon: %v", err)
		}
		if !resp.OK {
			return fail(exitFatal, "%s", resp.Error)
		}

		for _, event := range newEvents(resp.Events, seen) {
			cmd.Printf("%s  %-11s %s\n",
				event.At.Format("15:04:05"), event.State, event.Message)
		}
		seen = len(resp.Events)

		select {
		case <-signals:
			cmd.Println("stopping tunnel")
			if client, err := control.Dial(paths.ControlSock); err == nil {
				_, _ = client.Send(control.Request{Op: control.OpRemove, LocalPort: localPort})
				_ = client.Close()
			}
			return nil
		case <-time.After(attachPoll):
		}
	}
}

func newTunnelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tunnel <host> <container> <remote-port> <local-port>",
		Short: "Open a supervised tunnel (explicit form of the bare command)",
		Args:  cobra.ExactArgs(4),
		RunE:  openTunnel,
	}
	addTunnelFlags(cmd)
	return cmd
}

// addTunnelFlags is shared by the bare form and `hop tunnel`, so the two can
// never drift apart.
func addTunnelFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("env", "e", "", "environment label (dev, stg, prod); inferred when omitted")
	cmd.Flags().Duration("wait", 10*time.Second, "how long to wait for the tunnel to become healthy")
	cmd.Flags().BoolP("attach", "a", false, "stay in the foreground and stream state changes")
}
