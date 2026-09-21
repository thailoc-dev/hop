package picker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(t tea.KeyType) tea.KeyMsg  { return tea.KeyMsg{Type: t} }
func typeRunes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// step applies one message and returns the concrete model.
func step(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func labels(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

func TestStage1ListsSavedTunnelsThenDividerThenHosts(t *testing.T) {
	f := newFake()
	f.tunnels = append(f.tunnels, savedRedis(), runningMongo())
	f.hosts = []string{"example-backend-dev", "example-tracker-dev"}

	m := New(f)

	got := m.Visible()
	kinds := []ItemKind{}
	for _, it := range got {
		kinds = append(kinds, it.Kind)
	}
	want := []ItemKind{ItemTunnel, ItemTunnel, ItemDivider, ItemHost, ItemHost}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("position %d: %v, want %v (all: %v)", i, kinds[i], want[i], labels(got))
		}
	}
	if got[0].Label != "redis-stg" || got[3].Label != "example-backend-dev" {
		t.Fatalf("labels = %v", labels(got))
	}
}

func TestStage1FilterNarrowsBothGroups(t *testing.T) {
	f := newFake()
	f.tunnels = append(f.tunnels, savedRedis(), runningMongo())
	f.hosts = []string{"example-backend-dev", "example-tracker-dev"}
	m := New(f)

	m, _ = step(m, typeRunes("tracker"))

	got := labels(m.Visible())
	if len(got) != 3 || got[0] != "redis-stg" || got[1] != "" || got[2] != "example-tracker-dev" {
		t.Fatalf("filtered = %v (divider has an empty label)", got)
	}

	m, _ = step(m, key(tea.KeyBackspace))
	if m.Filter() != "tracke" {
		t.Fatalf("backspace: filter = %q", m.Filter())
	}
}

func TestStage1DividerDisappearsWhenAGroupIsEmpty(t *testing.T) {
	f := newFake()
	f.hosts = []string{"example-backend-dev"}
	m := New(f)

	for _, it := range m.Visible() {
		if it.Kind == ItemDivider {
			t.Fatal("divider shown with no tunnels above it")
		}
	}
}

func TestStage1EnterOnSavedTunnelYieldsANamedResult(t *testing.T) {
	f := newFake()
	f.tunnels = append(f.tunnels, savedRedis())
	m := New(f)

	m, cmd := step(m, key(tea.KeyEnter))

	res, ok := m.Result()
	if !ok || !res.Named || res.Spec.Name != "redis-stg" {
		t.Fatalf("result = %+v, %v", res, ok)
	}
	if cmd == nil {
		t.Fatal("no quit command after a choice")
	}
}

func TestStage1EnterOnRunningTunnelStillYieldsIt(t *testing.T) {
	f := newFake()
	f.tunnels = append(f.tunnels, runningMongo())
	m := New(f)

	m, _ = step(m, key(tea.KeyEnter))

	res, ok := m.Result()
	if !ok || res.Spec.Name != "mongo-dev" {
		t.Fatalf("result = %+v", res)
	}
}

func TestStage1CursorSkipsTheDivider(t *testing.T) {
	f := newFake()
	f.tunnels = append(f.tunnels, savedRedis())
	f.hosts = []string{"example-backend-dev"}
	m := New(f)

	m, _ = step(m, key(tea.KeyDown))
	if got := m.Visible()[m.Cursor()]; got.Kind != ItemHost {
		t.Fatalf("cursor landed on %v, want the host past the divider", got.Kind)
	}
	m, _ = step(m, key(tea.KeyUp))
	if got := m.Visible()[m.Cursor()]; got.Kind != ItemTunnel {
		t.Fatalf("cursor landed on %v going up", got.Kind)
	}
	m, _ = step(m, key(tea.KeyUp))
	if m.Cursor() != 0 {
		t.Fatalf("cursor = %d, want 0", m.Cursor())
	}
}

func TestStage1EnterOnHostAdvancesAndRequestsAFetch(t *testing.T) {
	f := newFake()
	f.hosts = []string{"example-backend-dev"}
	m := New(f)

	m, cmd := step(m, key(tea.KeyEnter))

	if m.Stage() != StageContainer {
		t.Fatalf("stage = %v", m.Stage())
	}
	if cmd == nil {
		t.Fatal("no fetch command was issued")
	}
	if m.Host() != "example-backend-dev" {
		t.Fatalf("host = %q", m.Host())
	}
}

func TestStage1EscQuitsWithNoResult(t *testing.T) {
	f := newFake()
	f.tunnels = append(f.tunnels, savedRedis())
	m := New(f)

	m, cmd := step(m, key(tea.KeyEsc))

	if _, ok := m.Result(); ok {
		t.Fatal("esc produced a result")
	}
	if cmd == nil {
		t.Fatal("esc did not quit")
	}
}

func TestStage1EmptyWorldShowsAMessage(t *testing.T) {
	m := New(newFake())
	if len(m.Visible()) != 0 {
		t.Fatalf("visible = %v", labels(m.Visible()))
	}
	if !strings.Contains(m.View(), "nothing to open") {
		t.Fatalf("view:\n%s", m.View())
	}
}

func TestViewMarksTheCursorAndShowsHints(t *testing.T) {
	f := newFake()
	f.tunnels = append(f.tunnels, savedRedis())
	m := New(f)
	v := m.View()
	for _, want := range []string{"redis-stg", "46379", "saved", "enter", "esc"} {
		if !strings.Contains(v, want) {
			t.Fatalf("view lacks %q:\n%s", want, v)
		}
	}
}
