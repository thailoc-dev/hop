package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/locnguyen/hop/internal/complete"
	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/tunnel"
	"github.com/spf13/cobra"
)

func writeSSHConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestCompleteHostsReadsSSHConfig(t *testing.T) {
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	writeSSHConfig(t, home, "Host example-backend-dev\nHost example-api\n")

	got := completeHosts("")

	if !slices.Contains(got, "example-backend-dev") {
		t.Fatalf("got %v", got)
	}
}

func TestCompleteHostsFiltersByPrefix(t *testing.T) {
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	writeSSHConfig(t, home, "Host example-backend-dev\nHost other-api\n")

	got := completeHosts("example")

	if len(got) != 1 || got[0] != "example-backend-dev" {
		t.Fatalf("got %v, want only the example- prefix match", got)
	}
}

func TestCompleteHostsIsEmptyWithoutAConfig(t *testing.T) {
	t.Setenv("HOME", shortTempDir(t))

	if got := completeHosts(""); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestCompleteLocalPortsListsRunningTunnels(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	serveFakeDaemon(t, p, control.Response{OK: true, Statuses: []tunnel.Status{
		{Spec: tunnel.Spec{Host: "h", Container: "app_mongo_staging", LocalPort: 27018, Env: "stg"}},
		{Spec: tunnel.Spec{Host: "h", Container: "api_redis", LocalPort: 6380, Env: "dev"}},
	}})

	got, directive := completeLocalPorts(&cobra.Command{}, nil, "")

	if len(got) != 2 {
		t.Fatalf("got %v, want two ports", got)
	}
	// Cobra shows the text after a tab as a description.
	if got[0] != "27018\tstg app_mongo_staging" {
		t.Fatalf("got %q, want the port with a readable description", got[0])
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive = %v; completing ports must not offer filenames", directive)
	}
}

func TestCompleteLocalPortsFiltersByPrefix(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	serveFakeDaemon(t, p, control.Response{OK: true, Statuses: []tunnel.Status{
		{Spec: tunnel.Spec{Container: "a", LocalPort: 27018}},
		{Spec: tunnel.Spec{Container: "b", LocalPort: 6380}},
	}})

	got, _ := completeLocalPorts(&cobra.Command{}, nil, "27")

	if len(got) != 1 {
		t.Fatalf("got %v, want only the 27-prefixed port", got)
	}
}

func TestCompleteLocalPortsIsEmptyWithoutADaemon(t *testing.T) {
	t.Setenv("HOME", shortTempDir(t))

	got, directive := completeLocalPorts(&cobra.Command{}, nil, "")

	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive = %v", directive)
	}
}

func TestCompleteTunnelArgsByPosition(t *testing.T) {
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	writeSSHConfig(t, home, "Host example-backend-dev\n")

	// Position 0 offers hosts.
	got, _ := completeTunnelArgs(&cobra.Command{}, nil, "")
	if !slices.Contains(got, "example-backend-dev") {
		t.Fatalf("position 0 = %v, want hosts", got)
	}

	// Position 3 mirrors the remote port.
	got, _ = completeTunnelArgs(&cobra.Command{}, []string{"h", "c", "27017"}, "")
	if len(got) != 1 || !strings.HasPrefix(got[0], "27017\t") {
		t.Fatalf("position 3 = %v, want the remote port mirrored", got)
	}

	// Position 4 and beyond offer nothing: the form takes exactly four.
	got, directive := completeTunnelArgs(&cobra.Command{},
		[]string{"h", "c", "27017", "27018"}, "")
	if len(got) != 0 {
		t.Fatalf("position 4 = %v, want nothing", got)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive = %v", directive)
	}
}

func TestCompletionScriptGenerates(t *testing.T) {
	out, err := runCmd(t, shortTempDir(t), "completion", "zsh")
	if err != nil {
		t.Fatalf("completion zsh: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Fatal("completion script was empty")
	}
}

func TestEnvFlagCompletionOffersTheThreeLabels(t *testing.T) {
	out, err := runCmd(t, shortTempDir(t), "__complete", "--env", "")
	if err != nil {
		t.Fatalf("__complete: %v\n%s", err, out)
	}
	for _, want := range []string{"dev", "stg", "prod"} {
		if !slices.Contains(completionLines(out), want) {
			t.Fatalf("--env completion = %q, missing %q", out, want)
		}
	}
}

// completionLines strips cobra's descriptions and trailing directive line.
func completionLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "Completion ended") {
			continue
		}
		lines = append(lines, strings.SplitN(line, "\t", 2)[0])
	}
	return lines
}

// seedCache writes a completion cache entry directly, standing in for a
// background fetch that has already completed.
func seedCache(t *testing.T, home, host string, names ...string) {
	t.Helper()
	paths := hopfs.New(home, 501)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	containers := make([]complete.Container, len(names))
	for i, name := range names {
		containers[i] = complete.Container{Name: name, Ports: []int{3000}}
	}
	cache := &complete.Cache{Dir: paths.CacheDir, TTL: complete.CacheTTL}
	if err := cache.Put(host, containers); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
}

func TestCompleteSavedNamesAtPositionZero(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	writeSSHConfig(t, home, "Host example-tracker-dev\n")
	saveNamed(t, p, "redis-stg", redisSpec())

	got, _ := completeTunnelArgs(&cobra.Command{}, nil, "")

	var names, hosts bool
	for _, entry := range got {
		if strings.HasPrefix(entry, "redis-stg\t") && strings.Contains(entry, "tracker_redis_staging@example-tracker-dev") {
			names = true
		}
		if entry == "example-tracker-dev" {
			hosts = true
		}
	}
	if !names {
		t.Fatalf("saved name missing or undescribed: %v", got)
	}
	if !hosts {
		t.Fatalf("hosts no longer offered alongside names: %v", got)
	}
}

func TestCompleteTargetsPrefersNamesOverPorts(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	named := redisSpec()
	named.Name = "redis-stg"
	unnamed := tunnel.Spec{Host: "h", Container: "c", RemotePort: 1, LocalPort: 27018}
	serveFakeDaemon(t, p, control.Response{OK: true, Statuses: []tunnel.Status{
		healthy(named), healthy(unnamed),
	}})

	got, _ := completeTargets(&cobra.Command{}, nil, "")

	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if !strings.HasPrefix(got[0], "redis-stg\t") {
		t.Fatalf("named tunnel offered as %q, want its name", got[0])
	}
	if !strings.HasPrefix(got[1], "27018\t") {
		t.Fatalf("unnamed tunnel offered as %q, want its port", got[1])
	}
}

func TestForgetCompletesSavedNames(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	saveNamed(t, p, "redis-stg", redisSpec())

	out, err := runCmd(t, home, "__complete", "forget", "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !slices.Contains(completionLines(out), "redis-stg") {
		t.Fatalf("forget completion = %q", out)
	}
}
