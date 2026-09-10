package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/locnguyen/hop/internal/complete"
	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/sshconfig"
	"github.com/locnguyen/hop/internal/sshexec"
	"github.com/spf13/cobra"
)

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
		return completeHosts(toComplete), noFiles

	case 1:
		cache, executor, ok := completerCache()
		if !ok {
			return nil, noFiles
		}
		var out []string
		for _, container := range complete.Containers(context.Background(), executor, cache, args[0]) {
			if strings.HasPrefix(container.Name, toComplete) {
				out = append(out, container.Name)
			}
		}
		return out, noFiles

	case 2:
		cache, executor, ok := completerCache()
		if !ok {
			return nil, noFiles
		}
		for _, container := range complete.Containers(context.Background(), executor, cache, args[0]) {
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
