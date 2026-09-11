package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/thailoc-dev/hop/internal/tunnel"
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

// cell is one table entry: the text that occupies width, and the colour it
// is painted in. Keeping them apart is what lets columns line up with colour
// on — tabwriter cannot, because it counts the invisible escape bytes.
type cell struct {
	text   string
	colour string
}

// renderColumns lays out rows with two spaces between columns, padding by
// visible width and applying colour only after the padding is computed.
func renderColumns(w io.Writer, header []string, rows [][]cell, colour bool) {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, c := range row {
			if n := utf8.RuneCountInString(c.text); n > widths[i] {
				widths[i] = n
			}
		}
	}

	line := func(cells []cell) {
		for i, c := range cells {
			fmt.Fprint(w, paint(c.text, c.colour, colour))
			if i < len(cells)-1 {
				fmt.Fprint(w, strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c.text)+2))
			}
		}
		fmt.Fprintln(w)
	}

	plainHeader := make([]cell, len(header))
	for i, h := range header {
		plainHeader[i] = cell{text: h}
	}
	line(plainHeader)
	for _, row := range rows {
		line(row)
	}
}

func renderTable(w io.Writer, statuses []tunnel.Status, colour bool) {
	if len(statuses) == 0 {
		fmt.Fprintln(w, "no tunnels")
		return
	}

	header := []string{"NAME", "LOCAL", "ENV", "HOST", "CONTAINER", "REMOTE", "STATE", "SINCE", "RETRIES"}
	rows := make([][]cell, 0, len(statuses))

	for _, st := range statuses {
		env := st.Spec.Env
		if env == "" {
			env = "\u2014"
		}

		since, retries := "\u2014", "\u2014"
		state := cell{text: string(st.State)}
		switch st.State {
		case tunnel.StateSaved:
			// Not running: no runtime columns, and the state itself is muted so
			// the eye lands on what is actually up.
			state.colour = ansiDim
		default:
			retries = fmt.Sprintf("%d", st.Retries)
			if st.State == tunnel.StateHealthy && !st.Since.IsZero() {
				since = shortDuration(time.Since(st.Since))
			}
			if st.State == tunnel.StateRetrying && st.LastError != "" {
				state.colour = ansiDim
			}
		}

		rows = append(rows, []cell{
			{text: st.Spec.Name},
			{text: fmt.Sprintf("%d", st.Spec.LocalPort)},
			{text: env, colour: colourFor(st.Spec.Env)},
			{text: st.Spec.Host},
			{text: st.Spec.Container},
			{text: fmt.Sprintf("%d", st.Spec.RemotePort)},
			state,
			{text: since},
			{text: retries},
		})
	}
	renderColumns(w, header, rows, colour)
}
