package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandPrintsVersion(t *testing.T) {
	Version = "1.2.3"
	t.Cleanup(func() { Version = "dev" })

	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "hop 1.2.3" {
		t.Fatalf("got %q, want %q", got, "hop 1.2.3")
	}
}
