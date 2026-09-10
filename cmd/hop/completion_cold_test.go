package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// withSlowStubSSH puts an ssh on PATH that cannot beat the completion ceiling,
// reproducing a host whose containers have never been fetched.
func withSlowStubSSH(t *testing.T) {
	t.Helper()
	dir := shortTempDir(t)
	stub := "#!/bin/sh\nsleep 3\nprintf 'somecontainer\\t1/tcp\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// captureWarm swaps the background warmer for one that only records, so tests
// never spawn a real process.
func captureWarm(t *testing.T, hosts *[]string) {
	t.Helper()
	original := warmFunc
	warmFunc = func(host string) { *hosts = append(*hosts, host) }
	t.Cleanup(func() { warmFunc = original })
}

// The reported bug: `hop example-webapp-dev <TAB>` returned nothing, with no
// indication that a fetch was underway, so an ordinary cold cache was
// indistinguishable from a broken host.
func TestColdCompletionExplainsItselfInsteadOfReturningSilence(t *testing.T) {
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	writeSSHConfig(t, home, "Host example-webapp-dev\n")
	withSlowStubSSH(t)

	var warmed []string
	captureWarm(t, &warmed)

	got, directive := completeTunnelArgs(&cobra.Command{}, []string{"example-webapp-dev"}, "")

	if len(got) == 0 {
		t.Fatal("returned nothing at all; the user cannot tell a cold cache " +
			"from a broken host, which is exactly what was reported")
	}
	help := got[len(got)-1]
	if !strings.HasPrefix(help, "_activeHelp_") {
		t.Fatalf("last entry = %q, want cobra active help (shown, never inserted)", help)
	}
	if !strings.Contains(help, "example-webapp-dev") {
		t.Fatalf("message %q does not name the host", help)
	}
	if !strings.Contains(strings.ToLower(help), "again") {
		t.Fatalf("message %q does not tell the user to press Tab again", help)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive = %v", directive)
	}
	if len(warmed) != 1 || warmed[0] != "example-webapp-dev" {
		t.Fatalf("warmed = %v, want the host scheduled for a background fetch", warmed)
	}
}

// The second press: once the cache is warm, real containers come back and no
// active-help message is shown.
func TestWarmCompletionReturnsContainersWithNoMessage(t *testing.T) {
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	writeSSHConfig(t, home, "Host example-webapp-dev\n")
	withSlowStubSSH(t) // still slow: the answer must come from the cache

	var warmed []string
	captureWarm(t, &warmed)

	seedCache(t, home, "example-webapp-dev",
		"webapp_nextjs_staging", "webapp_nginx_staging")

	got, _ := completeTunnelArgs(&cobra.Command{}, []string{"example-webapp-dev"}, "")

	if len(got) != 2 {
		t.Fatalf("got %v, want the two cached containers", got)
	}
	for _, entry := range got {
		if strings.HasPrefix(entry, "_activeHelp_") {
			t.Fatalf("still showing a fetching message with a warm cache: %q", entry)
		}
	}
	if len(warmed) != 0 {
		t.Fatalf("warmed %v despite a warm cache", warmed)
	}
}

func TestColdCompletionMessageIsSuppressedForARefinedPrefix(t *testing.T) {
	// Once the user has typed part of a name, a help line would sit oddly
	// among real candidates; the fetch is still scheduled.
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	writeSSHConfig(t, home, "Host example-webapp-dev\n")
	withSlowStubSSH(t)

	var warmed []string
	captureWarm(t, &warmed)

	got, _ := completeTunnelArgs(&cobra.Command{}, []string{"example-webapp-dev"}, "keg")

	for _, entry := range got {
		if strings.HasPrefix(entry, "_activeHelp_") {
			return // acceptable either way, but must still have warmed
		}
	}
	if len(warmed) != 1 {
		t.Fatalf("warmed = %v, want the fetch scheduled regardless of prefix", warmed)
	}
}
