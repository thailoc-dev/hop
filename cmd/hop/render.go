package main

import (
	"os"

	"github.com/spf13/cobra"
)

// ANSI colours. Constants rather than a dependency: hop needs four.
const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiDim    = "\x1b[2m"
)

// useColour reports whether to emit escapes: only for a terminal, never when
// --no-color or NO_COLOR say otherwise.
func useColour(cmd *cobra.Command) bool {
	if noColour, _ := cmd.Flags().GetBool("no-color"); noColour {
		return false
	}
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	file, ok := cmd.OutOrStdout().(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// colourFor maps an environment onto its colour. prod is red because the only
// mistake that matters is believing a prod tunnel is something else.
func colourFor(env string) string {
	switch env {
	case "prod":
		return ansiRed
	case "stg":
		return ansiYellow
	case "dev":
		return ansiGreen
	default:
		return ""
	}
}

func paint(text, colour string, enabled bool) string {
	if !enabled || colour == "" {
		return text
	}
	return colour + text + ansiReset
}

func envLabel(cmd *cobra.Command, env string) string {
	if env == "" {
		env = "—"
	}
	return paint(env, colourFor(env), useColour(cmd))
}
