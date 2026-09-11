package main

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/sshconfig"
	"github.com/locnguyen/hop/internal/store"
	"github.com/locnguyen/hop/internal/tunnel"
	"github.com/spf13/cobra"
)

// namePattern is the shape of a saved-tunnel name. Lower-case so completion
// and typing never disagree on case; a letter or digit first so a name can
// never be mistaken for a flag.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// isPort reports whether arg is a bare port number: non-empty, all digits.
// Names are required to contain a non-digit precisely so this test is enough
// to tell the two apart wherever both are accepted.
func isPort(arg string) bool {
	if arg == "" {
		return false
	}
	for _, r := range arg {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validateName applies the spec's naming rules and says which one failed.
func validateName(name string) error {
	switch {
	case name == "":
		return fail(exitUsage, "name is empty")
	case len(name) > 40:
		return fail(exitUsage, "name %q is longer than 40 characters", name)
	case name[0] == '-' || name[0] == '_':
		return fail(exitUsage, "name %q must start with a letter or digit", name)
	case !namePattern.MatchString(name):
		return fail(exitUsage, "name %q must be lower-case letters, digits, - or _", name)
	case reservedWords[name]:
		return fail(exitUsage, "%q is a reserved word and cannot be a tunnel name", name)
	case isPort(name):
		return fail(exitUsage,
			"name %q is all digits and would be mistaken for a port; include a letter, - or _", name)
	}
	return nil
}

// sameTunnel reports whether two specs describe the same forward. Name and Env
// are labels, not identity.
func sameTunnel(a, b tunnel.Spec) bool {
	return a.Host == b.Host && a.Container == b.Container &&
		a.RemotePort == b.RemotePort && a.LocalPort == b.LocalPort
}

// saveToCatalogue validates the name, stamps it on the spec, and upserts.
// Saving an existing name overwrites it: that is how a saved tunnel's ports
// are changed.
func saveToCatalogue(paths hopfs.Paths, name string, spec tunnel.Spec) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	c, err := store.LoadCatalogue(paths.CatalogueFile)
	if err != nil {
		return err
	}
	spec.Name = name
	c.Tunnels[name] = spec
	return store.SaveCatalogue(paths.CatalogueFile, c)
}

// openSaved opens a catalogue entry by name. Downstream it is exactly the
// four-argument open: the same add request, the same wait, the same output.
func openSaved(cmd *cobra.Command, name string) error {
	paths, err := hopfs.Default()
	if err != nil {
		return err
	}
	c, err := store.LoadCatalogue(paths.CatalogueFile)
	if err != nil {
		return err
	}

	spec, ok := c.Tunnels[name]
	if !ok {
		return unknownNameError(name)
	}
	if override, _ := cmd.Flags().GetString("env"); override != "" {
		spec.Env = override // this run only; the catalogue is not rewritten
	}

	client, err := connect(paths)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	// Asking for something you already have is not an error.
	list, err := client.Send(control.Request{Op: control.OpList})
	if err != nil {
		return fail(exitInternal, "talk to the daemon: %v", err)
	}
	for _, st := range list.Statuses {
		if st.Spec.Name == name || sameTunnel(st.Spec, spec) {
			cmd.Printf("%s is already running on %d\n", name, st.Spec.LocalPort)
			return nil
		}
	}

	// The control protocol is one request per connection.
	_ = client.Close()
	if client, err = connect(paths); err != nil {
		return err
	}
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
	if quiet, _ := cmd.Flags().GetBool("quiet"); !quiet {
		cmd.Print(renderOpened(cmd, status))
	}
	if attach, _ := cmd.Flags().GetBool("attach"); attach {
		return attachTunnel(cmd, paths, spec.LocalPort)
	}
	if status.State != tunnel.StateHealthy {
		return fail(exitNotReady,
			"tunnel is %s after %s; it keeps retrying in the background (hop logs %s)",
			status.State, wait, name)
	}
	return nil
}

// unknownNameError explains a miss. If the name is an ssh host alias, the
// likely mistake is trying to open a host with one argument, so say so.
func unknownNameError(name string) error {
	msg := fmt.Sprintf("no saved tunnel named %q", name)
	if path, err := sshconfig.DefaultPath(); err == nil {
		if hosts, err := sshconfig.Hosts(path); err == nil && slices.Contains(hosts, name) {
			msg += fmt.Sprintf("\n%q is an ssh host; to open a tunnel to it use:\n  hop %s <container> <remote-port> <local-port>",
				name, name)
		}
	}
	return fail(exitUsage, "%s", msg)
}

func newUpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up <name>",
		Short: "Open a saved tunnel (explicit form of `hop <name>`)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return openSaved(cmd, args[0])
		},
	}
	addTunnelFlags(cmd)
	return cmd
}
