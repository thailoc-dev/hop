package main

import (
	"context"
	"net"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/thailoc-dev/hop/internal/complete"
	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/hopfs"
	"github.com/thailoc-dev/hop/internal/picker"
	"github.com/thailoc-dev/hop/internal/sshconfig"
	"github.com/thailoc-dev/hop/internal/sshexec"
	"github.com/thailoc-dev/hop/internal/store"
	"github.com/thailoc-dev/hop/internal/sysprobe"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// isTerminal reports whether hop is talking to a person. The picker needs
// both directions: keys come in on stdin, the screen goes out on stdout. A
// pipe on either side means a script, which gets help instead.
func isTerminal() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}

// realSources is picker.Sources over the actual catalogue, ssh config,
// daemon, executor and cache.
type realSources struct {
	paths hopfs.Paths
	probe sysprobe.Prober
}

func newRealSources(paths hopfs.Paths) *realSources {
	return &realSources{paths: paths, probe: sysprobe.New()}
}

func (s *realSources) Tunnels() []tunnel.Status {
	catalogue, err := store.LoadCatalogue(s.paths.CatalogueFile)
	if err != nil {
		return nil
	}
	var running []tunnel.Status
	if client, err := control.Dial(s.paths.ControlSock); err == nil {
		if resp, err := client.Send(control.Request{Op: control.OpList}); err == nil {
			running = resp.Statuses
		}
		_ = client.Close()
	}
	return mergeSaved(running, catalogue.Tunnels)
}

func (s *realSources) Hosts() []string {
	path, err := sshconfig.DefaultPath()
	if err != nil {
		return nil
	}
	hosts, _ := sshconfig.Hosts(path)
	return hosts
}

// Containers serves the completion cache when warm and otherwise fetches
// with the picker's generous ceiling, warming the cache for Tab later.
func (s *realSources) Containers(ctx context.Context, host string) ([]complete.Container, error) {
	cache := &complete.Cache{Dir: s.paths.CacheDir, TTL: complete.CacheTTL}
	if cs, ok := cache.Get(host); ok {
		return cs, nil
	}
	if err := complete.Fetch(ctx, sshexec.New(s.paths.SocketDir), cache, host); err != nil {
		return nil, err
	}
	cs, _ := cache.GetStale(host)
	return cs, nil
}

func (s *realSources) PortFree(port int) bool { return s.probe.PortFree(port) }

// FreePort asks the kernel for one: bind :0, read it back, release it.
func (s *realSources) FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (s *realSources) ValidateName(name string) error { return validateName(name) }

// runPicker opens the interactive picker and hands its choice to the same
// open path the argument forms use.
func runPicker(cmd *cobra.Command) error {
	paths, err := hopfs.Default()
	if err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}

	program := tea.NewProgram(picker.New(newRealSources(paths)))
	final, err := program.Run()
	if err != nil {
		return fail(exitInternal, "picker: %v", err)
	}
	res, chosen := final.(picker.Model).Result()
	if !chosen {
		return nil // esc: nothing to do, nothing to say
	}
	if res.Named {
		return openSaved(cmd, res.Spec.Name)
	}
	return openSpec(cmd, res.Spec)
}

var _ picker.Sources = (*realSources)(nil)
