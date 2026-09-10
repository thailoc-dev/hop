package sshconfig

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestHostsReadsAliases(t *testing.T) {
	got, err := Hosts(filepath.Join("testdata", "config"))
	if err != nil {
		t.Fatalf("Hosts: %v", err)
	}

	for _, want := range []string{"example-backend-dev", "example-api", "bastion"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
}

func TestHostsReadsSeveralAliasesOnOneLine(t *testing.T) {
	got, _ := Hosts(filepath.Join("testdata", "config"))

	for _, want := range []string{"db-a", "db-b"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing %q from a multi-alias Host line: %v", want, got)
		}
	}
}

func TestHostsExcludesPatterns(t *testing.T) {
	got, _ := Hosts(filepath.Join("testdata", "config"))

	// "Host *" and negations are configuration, not connectable names.
	for _, unwanted := range []string{"*", "!nope", "web-*"} {
		if slices.Contains(got, unwanted) {
			t.Fatalf("pattern %q offered as a host: %v", unwanted, got)
		}
	}
}

func TestHostsFollowsInclude(t *testing.T) {
	got, _ := Hosts(filepath.Join("testdata", "config"))

	if !slices.Contains(got, "included-host") {
		t.Fatalf("Include was not followed: %v", got)
	}
}

func TestHostsIsCaseInsensitiveOnKeywords(t *testing.T) {
	// ssh treats "host", "Host" and "HOST" alike.
	got, _ := Hosts(filepath.Join("testdata", "config"))
	if !slices.Contains(got, "lowercase-keyword") {
		t.Fatalf("lowercase `host` keyword ignored: %v", got)
	}
}

func TestHostsIsEmptyForAMissingFile(t *testing.T) {
	got, err := Hosts(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("a missing ssh config is not an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestHostsDeduplicates(t *testing.T) {
	got, _ := Hosts(filepath.Join("testdata", "config"))
	seen := map[string]bool{}
	for _, h := range got {
		if seen[h] {
			t.Fatalf("%q appears twice in %v", h, got)
		}
		seen[h] = true
	}
}
