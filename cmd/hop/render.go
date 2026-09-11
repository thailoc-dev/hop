package main

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/locnguyen/hop/internal/tunnel"
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

// shortDuration formats an age the way a person reads it at a glance.
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func renderTable(w io.Writer, statuses []tunnel.Status, colour bool) {
	if len(statuses) == 0 {
		fmt.Fprintln(w, "no tunnels")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tLOCAL\tENV\tHOST\tCONTAINER\tREMOTE\tSTATE\tSINCE\tRETRIES")

	for _, st := range statuses {
		env := st.Spec.Env
		if env == "" {
			env = "\u2014"
		}

		since, retries := "\u2014", "\u2014"
		state := string(st.State)
		switch st.State {
		case tunnel.StateSaved:
			// Not running: no runtime columns, and the state itself is muted so
			// the eye lands on what is actually up.
			state = paint(state, ansiDim, colour)
		default:
			retries = fmt.Sprintf("%d", st.Retries)
			if st.State == tunnel.StateHealthy && !st.Since.IsZero() {
				since = shortDuration(time.Since(st.Since))
			}
			if st.State == tunnel.StateRetrying && st.LastError != "" {
				state = paint(state, ansiDim, colour)
			}
		}

		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			st.Spec.Name,
			st.Spec.LocalPort,
			paint(env, colourFor(st.Spec.Env), colour),
			st.Spec.Host, st.Spec.Container, st.Spec.RemotePort,
			state, since, retries)
	}
	_ = tw.Flush()
}
