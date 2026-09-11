package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/hopfs"
	"github.com/thailoc-dev/hop/internal/store"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// The lifecycle verbs mirror docker's: stop keeps the thing so start can bring
// it back, and only rm deletes. "The thing" is the tunnel definition in the
// catalogue -- host, container, ports, name. Nothing here touches the VPS:
// stop kills the local ssh forward, start opens a new one, and the container
// on the remote host is never started, stopped or removed by hop.

// autoName derives a catalogue name for a tunnel that was never given one, so
// that stopping it does not lose it. Docker does the same with its generated
// container names; ours are recognisable rather than whimsical:
// "<container>-<local-port>", made to fit the naming rules.
func autoName(spec tunnel.Spec, catalogue map[string]tunnel.Spec) string {
	var b strings.Builder
	for _, r := range strings.ToLower(spec.Container) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	base := strings.Trim(b.String(), "-_")
	if base == "" {
		base = "tunnel"
	}

	suffix := fmt.Sprintf("-%d", spec.LocalPort)
	// Leave room for a collision counter ("-9") inside the 40-char limit.
	if max := 40 - len(suffix) - 2; len(base) > max {
		base = strings.TrimRight(base[:max], "-_")
	}
	name := base + suffix

	// The same tunnel under this name already: reuse it. A different one:
	// step aside rather than overwrite somebody's saved definition.
	for i := 2; ; i++ {
		existing, taken := catalogue[name]
		if !taken || sameTunnel(existing, spec) {
			return name
		}
		name = fmt.Sprintf("%s%s-%d", base, suffix, i)
	}
}

// runningList asks the daemon what is running. No daemon means nothing is.
func runningList(paths hopfs.Paths) ([]tunnel.Status, bool, error) {
	client, err := control.Dial(paths.ControlSock)
	if err != nil {
		return nil, false, nil
	}
	defer func() { _ = client.Close() }()
	resp, err := client.Send(control.Request{Op: control.OpList})
	if err != nil {
		return nil, true, fail(exitInternal, "talk to the daemon: %v", err)
	}
	return resp.Statuses, true, nil
}

// removeRunning stops one forward (or all of them) in the daemon.
func removeRunning(paths hopfs.Paths, port int, all bool) error {
	client, err := control.Dial(paths.ControlSock)
	if err != nil {
		return nil // nothing running, nothing to stop
	}
	defer func() { _ = client.Close() }()
	resp, err := client.Send(control.Request{Op: control.OpRemove, LocalPort: port, All: all})
	if err != nil {
		return fail(exitInternal, "talk to the daemon: %v", err)
	}
	if !resp.OK {
		return fail(exitFatal, "%s", resp.Error)
	}
	return nil
}

// findRunning matches a name-or-port against the running list, by Name, by
// port, or by catalogue identity for an unnamed tunnel that was saved later.
func findRunning(running []tunnel.Status, catalogue map[string]tunnel.Spec, arg string) (tunnel.Status, bool) {
	for _, st := range running {
		if st.Spec.Name == arg || (isPort(arg) && fmt.Sprint(st.Spec.LocalPort) == arg) {
			return st, true
		}
	}
	if saved, ok := catalogue[arg]; ok {
		for _, st := range running {
			if sameTunnel(st.Spec, saved) {
				return st, true
			}
		}
	}
	return tunnel.Status{}, false
}

// findSaved matches a name-or-port against the catalogue.
func findSaved(catalogue map[string]tunnel.Spec, arg string) (tunnel.Spec, bool) {
	if spec, ok := catalogue[arg]; ok {
		return spec, true
	}
	if isPort(arg) {
		for _, spec := range catalogue {
			if fmt.Sprint(spec.LocalPort) == arg {
				return spec, true
			}
		}
	}
	return tunnel.Spec{}, false
}

// stopOne stops a running tunnel and makes sure it is in the catalogue, naming
// it if it never was. Returns the name it is kept under.
func stopOne(paths hopfs.Paths, c *store.Catalogue, st tunnel.Status) (string, error) {
	name := st.Spec.Name
	if name == "" {
		for n, saved := range c.Tunnels {
			if sameTunnel(saved, st.Spec) {
				name = n
				break
			}
		}
	}
	if name == "" {
		name = autoName(st.Spec, c.Tunnels)
	}
	if _, saved := c.Tunnels[name]; !saved {
		spec := st.Spec
		spec.Name = name
		c.Tunnels[name] = spec
		if err := store.SaveCatalogue(paths.CatalogueFile, *c); err != nil {
			return "", err
		}
	}
	return name, removeRunning(paths, st.Spec.LocalPort, false)
}

func newStopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "stop <name|local-port>",
		Aliases: []string{"down"},
		Short:   "Stop a tunnel's ssh forward, keeping it so `hop start` can bring it back",
		Long: "Stops the local ssh forward. The tunnel stays in the catalogue -- an\n" +
			"unnamed one is given a name first -- so `hop start <name>` reopens it.\n" +
			"Nothing on the remote host is touched.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeTargets,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if !all && len(args) != 1 {
				return fail(exitUsage, "specify a tunnel name or local port, or --all to stop every tunnel")
			}
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			c, err := store.LoadCatalogue(paths.CatalogueFile)
			if err != nil {
				return err
			}
			running, _, err := runningList(paths)
			if err != nil {
				return err
			}
			quiet, _ := cmd.Flags().GetBool("quiet")

			if all {
				for _, st := range running {
					name, err := stopOne(paths, &c, st)
					if err != nil {
						return err
					}
					if !quiet {
						cmd.Printf("stopped %s\n", name)
					}
				}
				return nil
			}

			st, ok := findRunning(running, c.Tunnels, args[0])
			if !ok {
				if saved, known := findSaved(c.Tunnels, args[0]); known {
					cmd.Printf("%s is already stopped\n", saved.Name)
					return nil
				}
				return fail(exitFatal, "no tunnel named or on port %q", args[0])
			}
			wasNamed := st.Spec.Name != ""
			name, err := stopOne(paths, &c, st)
			if err != nil {
				return err
			}
			if !quiet {
				if wasNamed {
					cmd.Printf("stopped %s\n", name)
				} else {
					cmd.Printf("stopped; saved as %s  (hop start %s)\n", name, name)
				}
			}
			return nil
		},
	}
	cmd.Flags().Bool("all", false, "stop every tunnel")
	return cmd
}

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "start <name|local-port>",
		Aliases: []string{"up"},
		Short:   "Start a stopped tunnel (opens a new ssh forward)",
		Args:    cobra.ExactArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completeSavedNames(toComplete), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if isPort(name) {
				paths, err := hopfs.Default()
				if err != nil {
					return err
				}
				c, err := store.LoadCatalogue(paths.CatalogueFile)
				if err != nil {
					return err
				}
				spec, ok := findSaved(c.Tunnels, name)
				if !ok {
					return fail(exitUsage, "no saved tunnel on local port %s", name)
				}
				name = spec.Name
			}
			return openSaved(cmd, name)
		},
	}
	addTunnelFlags(cmd)
	return cmd
}

func newRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name|local-port>",
		Aliases: []string{"forget"},
		Short:   "Remove a tunnel from the catalogue, stopping it first if it is running",
		Args:    cobra.ExactArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completeSavedNames(toComplete), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			c, err := store.LoadCatalogue(paths.CatalogueFile)
			if err != nil {
				return err
			}
			running, _, err := runningList(paths)
			if err != nil {
				return err
			}

			found := false
			if st, ok := findRunning(running, c.Tunnels, args[0]); ok {
				if err := removeRunning(paths, st.Spec.LocalPort, false); err != nil {
					return err
				}
				found = true
			}
			if spec, ok := findSaved(c.Tunnels, args[0]); ok {
				delete(c.Tunnels, spec.Name)
				if err := store.SaveCatalogue(paths.CatalogueFile, c); err != nil {
					return err
				}
				found = true
			}
			if !found {
				return fail(exitUsage, "no saved tunnel named %q", args[0])
			}
			return nil
		},
	}
}
