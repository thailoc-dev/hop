// Package complete supplies tab-completion data. Every network-backed lookup
// in this package is bounded by Timeout and backed by Cache, because a shell
// that freezes on Tab is worse than one that completes nothing.
package complete

import (
	"sort"
	"strconv"
	"strings"
)

// Container is one running container, as far as completion cares.
type Container struct {
	Name  string `json:"name"`
	Ports []int  `json:"ports"`
}

// PSFormat is the docker format string ParsePS expects.
const PSFormat = `{{.Names}}\t{{.Ports}}`

// ParsePS turns `docker ps` output into containers. Malformed lines are
// skipped rather than reported: this feeds a completion list, and a partial
// answer is more useful than an error the shell cannot display.
func ParsePS(stdout string) []Container {
	var out []Container
	for _, line := range strings.Split(stdout, "\n") {
		name, ports, found := strings.Cut(line, "\t")
		if !found || strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, Container{Name: strings.TrimSpace(name), Ports: ContainerPorts(ports)})
	}
	return out
}

// ContainerPorts extracts the container-side ports from docker's Ports field.
//
// The field takes several shapes: "27017/tcp" for an unpublished port, and
// "0.0.0.0:27018->27017/tcp, :::27018->27017/tcp" for a published one, where
// the number after -> is the one inside the container. That is the number a
// tunnel needs, and the one the host publishes on is irrelevant here.
func ContainerPorts(portsField string) []int {
	seen := map[int]bool{}
	for _, part := range strings.Split(portsField, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, after, found := strings.Cut(part, "->"); found {
			part = after
		}
		number, _, _ := strings.Cut(part, "/")
		if port, err := strconv.Atoi(strings.TrimSpace(number)); err == nil {
			seen[port] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}

	out := make([]int, 0, len(seen))
	for port := range seen {
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}
