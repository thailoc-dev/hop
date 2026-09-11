package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/locnguyen/hop/internal/complete"
	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/sshconfig"
	"github.com/locnguyen/hop/internal/sshexec"
	"github.com/locnguyen/hop/internal/store"
	"github.com/locnguyen/hop/internal/tunnel"
	"github.com/spf13/cobra"
)

// warmFunc schedules the out-of-band container fetch. It is a variable so
// tests can observe scheduling without spawning a process.
var warmFunc = spawnWarm

// completeHosts offers ssh config aliases matching what has been typed.
func completeHosts(toComplete string) []string {
	path, err := sshconfig.DefaultPath()
	if err != nil {
		return nil
	}
	hosts, err := sshconfig.Hosts(path)
	if err != nil {
		return nil
	}

	var out []string
	for _, host := range hosts {
		if strings.HasPrefix(host, toComplete) {
			out = append(out, host)
		}
	}
	return out
}

// completerCache builds the cache for network-backed completions.
func completerCache() (*complete.Cache, sshexec.Executor, bool) {
	paths, err := hopfs.Default()
	if err != nil {
		return nil, nil, false
	}
	return &complete.Cache{Dir: paths.CacheDir, TTL: complete.CacheTTL},
		sshexec.New(paths.SocketDir), true
}

// completeTunnelArgs completes the bare form by argument position:
// 0 host, 1 container, 2 remote port, 3 local port.
func completeTunnelArgs(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	const noFiles = cobra.ShellCompDirectiveNoFileComp

	switch len(args) {
	case 0:
		out := completeSavedNames(toComplete)
		return append(out, completeHosts(toComplete)...), noFiles

	case 1:
		cache, executor, ok := completerCache()
		if !ok {
			return nil, noFiles
		}

		warmed := false
		containers := complete.Containers(context.Background(), executor, cache, args[0],
			func(host string) { warmed = true; warmFunc(host) })

		var out []string
		for _, container := range containers {
			if strings.HasPrefix(container.Name, toComplete) {
				out = append(out, container.Name)
			}
		}

		// A cold host answers nothing on the first press: a first ssh
		// connection costs seconds and the ceiling is milliseconds. Say so.
		// Silence here is indistinguishable from a broken host, and gets
		// reported as one.
		if warmed && len(out) == 0 {
			out = cobra.AppendActiveHelp(out, fmt.Sprintf(
				"fetching containers on %s — press Tab again in a moment", args[0]))
		}
		return out, noFiles

	case 2:
		cache, executor, ok := completerCache()
		if !ok {
			return nil, noFiles
		}
		for _, container := range complete.Containers(context.Background(), executor, cache, args[0], warmFunc) {
			if container.Name != args[1] {
				continue
			}
			var out []string
			for _, port := range container.Ports {
				if text := strconv.Itoa(port); strings.HasPrefix(text, toComplete) {
					out = append(out, text)
				}
			}
			return out, noFiles
		}
		return nil, noFiles

	case 3:
		// Suggest mirroring the remote port, which is what most people want
		// and what the old script's own examples did.
		if strings.HasPrefix(args[2], toComplete) {
			return []string{args[2] + "\tsame as the remote port"}, noFiles
		}
		return nil, noFiles

	default:
		return nil, noFiles
	}
}

// completeLocalPorts offers the ports of running tunnels, with a description
// so the right one is recognisable without remembering the number.
func completeLocalPorts(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	const noFiles = cobra.ShellCompDirectiveNoFileComp

	paths, err := hopfs.Default()
	if err != nil {
		return nil, noFiles
	}
	client, err := control.Dial(paths.ControlSock)
	if err != nil {
		return nil, noFiles // no daemon means no tunnels to name
	}
	defer func() { _ = client.Close() }()

	resp, err := client.Send(control.Request{Op: control.OpList})
	if err != nil || !resp.OK {
		return nil, noFiles
	}

	var out []string
	for _, status := range resp.Statuses {
		port := strconv.Itoa(status.Spec.LocalPort)
		if !strings.HasPrefix(port, toComplete) {
			continue
		}
		env := status.Spec.Env
		if env == "" {
			env = "—"
		}
		out = append(out, fmt.Sprintf("%s\t%s %s", port, env, status.Spec.Container))
	}
	return out, noFiles
}

// completeSavedNames offers catalogue entries, each described so a name is
// recognisable without remembering what it points at.
func completeSavedNames(toComplete string) []string {
	paths, err := hopfs.Default()
	if err != nil {
		return nil
	}
	c, err := store.LoadCatalogue(paths.CatalogueFile)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(c.Tunnels))
	for name := range c.Tunnels {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []string
	for _, name := range names {
		if !strings.HasPrefix(name, toComplete) {
			continue
		}
		spec := c.Tunnels[name]
		env := spec.Env
		if env == "" {
			env = "\u2014"
		}
		out = append(out, fmt.Sprintf("%s\t%s %s@%s", name, env, spec.Container, spec.Host))
	}
	return out
}

// completeSavedNamesArg is the cobra-shaped form, for commands whose single
// argument is a saved name.
func completeSavedNamesArg(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeSavedNames(toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeTargets offers running tunnels for the commands that address one:
// by name where the tunnel has one, by port otherwise.
func completeTargets(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	const noFiles = cobra.ShellCompDirectiveNoFileComp

	paths, err := hopfs.Default()
	if err != nil {
		return nil, noFiles
	}
	client, err := control.Dial(paths.ControlSock)
	if err != nil {
		return nil, noFiles
	}
	defer func() { _ = client.Close() }()
	resp, err := client.Send(control.Request{Op: control.OpList})
	if err != nil || !resp.OK {
		return nil, noFiles
	}

	catalogue, _ := store.LoadCatalogue(paths.CatalogueFile)
	rows := mergeSaved(resp.Statuses, catalogue.Tunnels)

	var out []string
	for _, st := range rows {
		if st.State == tunnel.StateSaved {
			continue // not running: nothing to stop, restart or read logs from
		}
		env := st.Spec.Env
		if env == "" {
			env = "\u2014"
		}
		if st.Spec.Name != "" {
			if strings.HasPrefix(st.Spec.Name, toComplete) {
				out = append(out, fmt.Sprintf("%s\t%s %s :%d", st.Spec.Name, env, st.Spec.Container, st.Spec.LocalPort))
			}
			continue
		}
		port := strconv.Itoa(st.Spec.LocalPort)
		if strings.HasPrefix(port, toComplete) {
			out = append(out, fmt.Sprintf("%s\t%s %s", port, env, st.Spec.Container))
		}
	}
	return out, noFiles
}
