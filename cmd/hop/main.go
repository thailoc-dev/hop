package main

import (
	"errors"
	"fmt"
	"os"
)

// Exit codes. See the spec's "Exit codes" table.
const (
	exitOK       = 0
	exitFatal    = 1
	exitUsage    = 64
	exitNoDaemon = 69
	exitInternal = 70
	exitNotReady = 75
)

// codedError carries an exit code out of a cobra RunE.
type codedError struct {
	code int
	err  error
}

func (e codedError) Error() string { return e.err.Error() }
func (e codedError) Unwrap() error { return e.err }

func fail(code int, format string, args ...any) error {
	return codedError{code: code, err: fmt.Errorf(format, args...)}
}

func main() {
	cmd := newRootCmd()
	if err := cmd.Execute(); err != nil {
		var ce codedError
		if errors.As(err, &ce) {
			fmt.Fprintln(os.Stderr, "hop: "+ce.Error())
			os.Exit(ce.code)
		}
		fmt.Fprintln(os.Stderr, "hop: "+err.Error())
		os.Exit(exitUsage)
	}
	os.Exit(exitOK)
}
