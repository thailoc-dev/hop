package picker

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/thailoc-dev/hop/internal/tunnel"
)

var (
	styleTitle   = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Faint(true)
	styleCursor  = lipgloss.NewStyle().Bold(true)
	styleDivider = lipgloss.NewStyle().Faint(true)
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	envStyles    = map[string]lipgloss.Style{
		"prod": lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		"stg":  lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"dev":  lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
	}
)

func envText(env string) string {
	if env == "" {
		env = "—"
	}
	if st, ok := envStyles[env]; ok {
		return st.Render(fmt.Sprintf("%-4s", env))
	}
	return fmt.Sprintf("%-4s", env)
}

func (m Model) View() string {
	var b strings.Builder

	switch m.stage {
	case StageTarget:
		fmt.Fprintf(&b, "  %s%s\n", styleTitle.Render("Tunnels"), m.filterHint())
		m.renderList(&b, "nothing to open: no saved tunnels, no ssh hosts")
		b.WriteString(styleDim.Render("  ↑↓ move   enter open   esc quit") + "\n")
	default:
		fmt.Fprintf(&b, "  %s › %s%s\n", styleTitle.Render("Tunnels"), m.host, m.filterHint())
		if m.fetching {
			fmt.Fprintf(&b, "  %s fetching containers on %s…\n", m.spin.View(), m.host)
		}
		if m.fetchErr != "" {
			fmt.Fprintf(&b, "  %s\n", styleErr.Render(m.fetchErr))
		}
		m.renderList(&b, "")
		hint := "  ↑↓ move   enter choose   esc back"
		if m.stage == StageContainer && m.fetchErr != "" {
			hint = "  type a container name and press enter   esc back"
		}
		b.WriteString(styleDim.Render(hint) + "\n")
	}
	return b.String()
}

func (m Model) filterHint() string {
	if m.filter == "" {
		return styleDim.Render("    type to filter…")
	}
	return "    " + m.filter + "▏"
}

func (m Model) renderList(b *strings.Builder, empty string) {
	v := m.Visible()
	if len(v) == 0 && empty != "" {
		b.WriteString(styleDim.Render("  "+empty) + "\n")
		return
	}
	for i, it := range v {
		prefix := "  "
		if i == m.cursor {
			prefix = styleCursor.Render("› ")
		}
		switch it.Kind {
		case ItemDivider:
			b.WriteString(styleDivider.Render("  ── hosts ──") + "\n")
		case ItemTunnel:
			s := it.Status
			state := string(s.State)
			if s.State == tunnel.StateSaved {
				state = styleDim.Render(state)
			}
			fmt.Fprintf(b, "%s%-14s %s %-24s :%-6d %s\n",
				prefix, s.Spec.Name, envText(s.Spec.Env), s.Spec.Container, s.Spec.LocalPort, state)
		default:
			fmt.Fprintf(b, "%s%s\n", prefix, it.Label)
		}
	}
}
