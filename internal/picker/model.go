package picker

import (
	"context"
	"fmt"
	"strconv"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/thailoc-dev/hop/internal/complete"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// Stage is where the user is in the flow.
type Stage int

const (
	StageTarget     Stage = iota // saved tunnels and hosts
	StageContainer               // containers on the chosen host
	StageRemotePort              // the container's ports
	StageLocalPort               // local port, prefilled
	StageName                    // optional name
)

// ItemKind is what a list row is.
type ItemKind int

const (
	ItemTunnel ItemKind = iota
	ItemDivider
	ItemHost
	ItemContainer
	ItemPort
)

// Item is one row of whatever list the current stage shows.
type Item struct {
	Kind      ItemKind
	Label     string // what filtering matches and the view prints
	Status    tunnel.Status
	Host      string
	Container complete.Container
	Port      int
}

// containersMsg is the fetch result for stage 2.
type containersMsg struct {
	host       string
	containers []complete.Container
	err        error
}

// Model is the picker. Update returns a new value; nothing is mutated in
// place, which is what makes the tests a sequence of pure steps.
type Model struct {
	src   Sources
	stage Stage

	all    []Item // every row for the current stage, unfiltered
	filter string
	cursor int

	host      string
	container complete.Container
	remote    int
	local     int
	known     []complete.Container // stage-2 fetch result, kept for Esc

	fetching bool
	fetchErr string
	spin     spinner.Model

	input      textinput.Model
	inputErr   string
	freshInput bool // first keystroke replaces the prefill

	result Result
	done   bool
}

func New(src Sources) Model {
	m := Model{
		src:  src,
		spin: spinner.New(spinner.WithSpinner(spinner.Dot)),
	}
	m.input = textinput.New()
	m.input.CharLimit = 40
	m.all = m.targetItems()
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// Accessors for tests and the view.
func (m Model) Stage() Stage                  { return m.stage }
func (m Model) Filter() string                { return m.filter }
func (m Model) Cursor() int                   { return m.cursor }
func (m Model) Host() string                  { return m.host }
func (m Model) Container() complete.Container { return m.container }
func (m Model) RemotePort() int               { return m.remote }
func (m Model) Fetching() bool                { return m.fetching }
func (m Model) FetchError() string            { return m.fetchErr }
func (m Model) InputValue() string            { return m.input.Value() }
func (m Model) InputError() string            { return m.inputErr }
func (m Model) Result() (Result, bool)        { return m.result, m.done }

// Visible is the current list after filtering. The divider separates two
// groups (tunnels above, hosts below) and is shown only when both groups
// still have rows after filtering; a divider heading nothing is noise.
func (m Model) Visible() []Item {
	var above, below []Item
	pastDivider := false
	for _, it := range m.all {
		if it.Kind == ItemDivider {
			pastDivider = true
			continue
		}
		if !fuzzyMatch(m.filter, it.searchText()) {
			continue
		}
		if pastDivider {
			below = append(below, it)
		} else {
			above = append(above, it)
		}
	}
	out := above
	if len(above) > 0 && len(below) > 0 {
		out = append(out, Item{Kind: ItemDivider})
	}
	return append(out, below...)
}

func (it Item) searchText() string {
	switch it.Kind {
	case ItemTunnel:
		s := it.Status.Spec
		return s.Name + " " + s.Container + " " + s.Host + " " + strconv.Itoa(s.LocalPort)
	default:
		return it.Label
	}
}

func (m Model) targetItems() []Item {
	var items []Item
	for _, st := range m.src.Tunnels() {
		items = append(items, Item{Kind: ItemTunnel, Label: st.Spec.Name, Status: st})
	}
	hosts := m.src.Hosts()
	if len(items) > 0 && len(hosts) > 0 {
		items = append(items, Item{Kind: ItemDivider})
	}
	for _, h := range hosts {
		items = append(items, Item{Kind: ItemHost, Label: h, Host: h})
	}
	return items
}

// selected is the item under the cursor, if any.
func (m Model) selected() (Item, bool) {
	v := m.Visible()
	if len(v) == 0 || m.cursor < 0 || m.cursor >= len(v) {
		return Item{}, false
	}
	return v[m.cursor], true
}

// move steps the cursor, skipping dividers and clamping at the ends.
func (m Model) move(delta int) Model {
	v := m.Visible()
	if len(v) == 0 {
		return m
	}
	next := m.cursor + delta
	for next >= 0 && next < len(v) && v[next].Kind == ItemDivider {
		next += delta
	}
	if next < 0 || next >= len(v) {
		return m
	}
	m.cursor = next
	return m
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		switch m.stage {
		case StageTarget:
			return m.updateTarget(msg)
		case StageContainer:
			return m.updateContainer(msg)
		case StageRemotePort:
			return m.updateRemotePort(msg)
		case StageLocalPort:
			return m.updateLocalPort(msg)
		case StageName:
			return m.updateName(msg)
		}
	case containersMsg:
		return m.receiveContainers(msg)
	case spinner.TickMsg:
		if m.fetching {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

// listKeys handles the keys every list stage shares: filter, move. It
// reports whether it consumed the key.
func (m Model) listKeys(msg tea.KeyMsg) (Model, bool) {
	switch msg.Type {
	case tea.KeyUp:
		return m.move(-1), true
	case tea.KeyDown:
		return m.move(+1), true
	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
			m.cursor = 0
		}
		return m, true
	case tea.KeyRunes:
		m.filter += string(msg.Runes)
		m.cursor = 0
		return m, true
	}
	return m, false
}

func (m Model) updateTarget(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if next, ok := m.listKeys(msg); ok {
		return next, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyEnter:
		it, ok := m.selected()
		if !ok {
			return m, nil
		}
		switch it.Kind {
		case ItemTunnel:
			m.result = Result{Spec: it.Status.Spec, Named: true}
			m.done = true
			return m, tea.Quit
		case ItemHost:
			m.host = it.Host
			m.stage = StageContainer
			m.filter, m.cursor = "", 0
			m.all = nil
			m.fetching, m.fetchErr = true, ""
			return m, tea.Batch(m.spin.Tick, m.fetchContainers())
		}
	}
	return m, nil
}

// fetchContainers asks Sources for the host's containers, off the UI loop.
func (m Model) fetchContainers() tea.Cmd {
	src, host := m.src, m.host
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
		defer cancel()
		cs, err := src.Containers(ctx, host)
		return containersMsg{host: host, containers: cs, err: err}
	}
}

func (m Model) receiveContainers(msg containersMsg) (tea.Model, tea.Cmd) {
	if msg.host != m.host || m.stage != StageContainer {
		return m, nil // stale: the user already moved on
	}
	m.fetching = false
	if msg.err != nil {
		m.fetchErr = msg.err.Error()
		return m, nil
	}
	m.known = msg.containers
	for _, c := range msg.containers {
		m.all = append(m.all, Item{Kind: ItemContainer, Label: c.Name, Container: c})
	}
	return m, nil
}

func (m Model) updateContainer(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if next, ok := m.listKeys(msg); ok {
		return next, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		return m.backToTarget(), nil
	case tea.KeyEnter:
		if it, ok := m.selected(); ok && it.Kind == ItemContainer {
			return m.chooseContainer(it.Container), nil
		}
		// No list row: the fetch failed or nothing matched. A typed name is
		// still a valid choice -- the host may be reachable but slow.
		if !m.fetching && m.filter != "" {
			return m.chooseContainer(complete.Container{Name: m.filter}), nil
		}
	}
	return m, nil
}

func (m Model) chooseContainer(c complete.Container) Model {
	m.container = c
	m.stage = StageRemotePort
	m.filter, m.cursor = "", 0
	m.all = nil
	for _, p := range c.Ports {
		m.all = append(m.all, Item{Kind: ItemPort, Label: portLabel(p), Port: p})
	}
	return m
}

// parsePort is stricter than Atoi: a port is 1..65535.
func parsePort(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}

func (m Model) updateRemotePort(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if next, ok := m.listKeys(msg); ok {
		next.inputErr = ""
		return next, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.stage = StageContainer
		m.filter, m.cursor, m.inputErr = "", 0, ""
		m.all = nil
		for _, c := range m.known {
			m.all = append(m.all, Item{Kind: ItemContainer, Label: c.Name, Container: c})
		}
		return m, nil
	case tea.KeyEnter:
		if it, ok := m.selected(); ok && it.Kind == ItemPort {
			return m.chooseRemote(it.Port), nil
		}
		if p, ok := parsePort(m.filter); ok {
			return m.chooseRemote(p), nil
		}
		m.inputErr = "type a port between 1 and 65535, or pick one"
	}
	return m, nil
}

func (m Model) chooseRemote(p int) Model {
	m.remote = p
	m.stage = StageLocalPort
	m.filter, m.cursor, m.inputErr = "", 0, ""
	m.input = textinput.New()
	m.input.CharLimit = 5
	m.input.SetValue(strconv.Itoa(p))
	m.input.Focus()
	m.freshInput = true // first keystroke replaces the prefill
	return m
}

func (m Model) updateLocalPort(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.stage = StageRemotePort
		m.inputErr = ""
		return m, nil
	case tea.KeyTab:
		if p, err := m.src.FreePort(); err == nil {
			m.input.SetValue(strconv.Itoa(p))
			m.freshInput = false
		}
		return m, nil
	case tea.KeyEnter:
		p, ok := parsePort(m.input.Value())
		if !ok {
			m.inputErr = "a local port is a number between 1 and 65535"
			return m, nil
		}
		if !m.src.PortFree(p) {
			m.inputErr = fmt.Sprintf("local port %d is in use — try another, or tab for a free one", p)
			return m, nil
		}
		m.local = p
		m.stage = StageName
		m.inputErr = ""
		m.input = textinput.New()
		m.input.CharLimit = 40
		m.input.Placeholder = "name (optional)"
		m.input.Focus()
		return m, nil
	case tea.KeyRunes:
		if m.freshInput {
			m.input.SetValue("")
			m.freshInput = false
		}
	}
	m.inputErr = ""
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) updateName(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m.chooseRemote(m.remote), nil // back to local port, prefilled again
	case tea.KeyEnter:
		name := m.input.Value()
		if name != "" {
			if err := m.src.ValidateName(name); err != nil {
				m.inputErr = err.Error()
				return m, nil
			}
		}
		m.result = Result{Spec: tunnel.Spec{
			Host: m.host, Container: m.container.Name,
			RemotePort: m.remote, LocalPort: m.local, Name: name,
		}}
		m.done = true
		return m, tea.Quit
	}
	m.inputErr = ""
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) backToTarget() Model {
	m.stage = StageTarget
	m.host, m.filter, m.cursor = "", "", 0
	m.fetching, m.fetchErr = false, ""
	m.known = nil
	m.all = m.targetItems()
	return m
}

func portLabel(p int) string { return fmt.Sprintf("%d", p) }
