package main

import (
	"sort"

	"github.com/thailoc-dev/hop/internal/hopfs"
	"github.com/thailoc-dev/hop/internal/store"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// resolveTarget turns a name-or-port argument into a local port.
//
// A bare number is a port and needs no lookup. A name is matched against the
// running tunnels first by the Name they were opened under, then by identity
// against the catalogue -- so a tunnel opened the long way and saved later is
// still addressable by its name.
func resolveTarget(paths hopfs.Paths, running []tunnel.Status, arg string) (int, error) {
	if isPort(arg) {
		return parsePort(arg, "local-port")
	}

	for _, st := range running {
		if st.Spec.Name == arg {
			return st.Spec.LocalPort, nil
		}
	}

	c, err := store.LoadCatalogue(paths.CatalogueFile)
	if err != nil {
		return 0, err
	}
	if saved, ok := c.Tunnels[arg]; ok {
		for _, st := range running {
			if sameTunnel(st.Spec, saved) {
				return st.Spec.LocalPort, nil
			}
		}
	}
	return 0, fail(exitFatal, "no running tunnel named %q", arg)
}

// mergeSaved builds the `hop ls` view: running tunnels, annotated with a
// catalogue name where an unnamed one matches a saved spec, followed by saved
// tunnels that are not running, in state "saved" and sorted by name.
func mergeSaved(running []tunnel.Status, catalogue map[string]tunnel.Spec) []tunnel.Status {
	out := make([]tunnel.Status, 0, len(running)+len(catalogue))
	isRunning := map[string]bool{}

	for _, st := range running {
		if st.Spec.Name == "" {
			for name, saved := range catalogue {
				if sameTunnel(st.Spec, saved) {
					st.Spec.Name = name
					break
				}
			}
		}
		if st.Spec.Name != "" {
			isRunning[st.Spec.Name] = true
		}
		out = append(out, st)
	}

	var saved []tunnel.Status
	for name, spec := range catalogue {
		if isRunning[name] {
			continue
		}
		spec.Name = name
		saved = append(saved, tunnel.Status{Spec: spec, State: tunnel.StateSaved})
	}
	sort.Slice(saved, func(i, j int) bool { return saved[i].Spec.Name < saved[j].Spec.Name })

	return append(out, saved...)
}
