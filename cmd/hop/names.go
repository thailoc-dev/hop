package main

import (
	"regexp"

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

// openSaved opens a tunnel from the catalogue by name. Replaced in Task 3.
func openSaved(_ *cobra.Command, _ string) error {
	return fail(exitInternal, "not implemented")
}
