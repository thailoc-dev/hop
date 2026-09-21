package main

import (
	"os"

	"github.com/spf13/cobra"
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

// runPicker opens the interactive picker. Replaced in Task 5.
func runPicker(_ *cobra.Command) error {
	return fail(exitInternal, "picker not implemented")
}
