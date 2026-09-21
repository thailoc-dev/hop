package picker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thailoc-dev/hop/internal/complete"
)

func atRemotePortStage(t *testing.T, ports ...int) (Model, *fakeSources) {
	t.Helper()
	f := newFake()
	f.hosts = []string{"example-backend-dev"}
	m := New(f)
	m, _ = step(m, key(tea.KeyEnter))
	m, _ = step(m, containersMsg{host: "example-backend-dev", containers: []complete.Container{
		{Name: "app_mongo_staging", Ports: ports},
	}})
	m, _ = step(m, key(tea.KeyEnter))
	if m.Stage() != StageRemotePort {
		t.Fatalf("setup: stage = %v", m.Stage())
	}
	return m, f
}

func TestStage3ListsThePortsAndEnterPicksOne(t *testing.T) {
	m, _ := atRemotePortStage(t, 27017, 28017)
	if got := labels(m.Visible()); len(got) != 2 || got[0] != "27017" {
		t.Fatalf("visible = %v", got)
	}

	m, _ = step(m, key(tea.KeyDown))
	m, _ = step(m, key(tea.KeyEnter))

	if m.Stage() != StageLocalPort || m.RemotePort() != 28017 {
		t.Fatalf("stage=%v remote=%d", m.Stage(), m.RemotePort())
	}
	if m.InputValue() != "28017" {
		t.Fatalf("local port not prefilled with the remote port: %q", m.InputValue())
	}
}

func TestStage3TypedPortIsAccepted(t *testing.T) {
	m, _ := atRemotePortStage(t)
	m, _ = step(m, typeRunes("5432"))
	m, _ = step(m, key(tea.KeyEnter))
	if m.Stage() != StageLocalPort || m.RemotePort() != 5432 {
		t.Fatalf("stage=%v remote=%d", m.Stage(), m.RemotePort())
	}
}

func TestStage3RejectsANonPort(t *testing.T) {
	m, _ := atRemotePortStage(t)
	m, _ = step(m, typeRunes("lots"))
	m, _ = step(m, key(tea.KeyEnter))
	if m.Stage() != StageRemotePort {
		t.Fatal("advanced on a non-numeric port")
	}
	if !strings.Contains(m.View(), "port") {
		t.Fatalf("no explanation in view:\n%s", m.View())
	}
}

func TestStage3EscGoesBackToContainers(t *testing.T) {
	m, _ := atRemotePortStage(t, 27017)
	m, _ = step(m, key(tea.KeyEsc))
	if m.Stage() != StageContainer {
		t.Fatalf("stage = %v", m.Stage())
	}
	if got := labels(m.Visible()); len(got) != 1 || got[0] != "app_mongo_staging" {
		t.Fatalf("containers not restored: %v", got)
	}
}

func atLocalPortStage(t *testing.T) (Model, *fakeSources) {
	t.Helper()
	m, f := atRemotePortStage(t, 27017)
	m, _ = step(m, key(tea.KeyEnter))
	return m, f
}

func TestStage4TabFillsAFreePort(t *testing.T) {
	m, f := atLocalPortStage(t)
	f.freePort = 52913
	m, _ = step(m, key(tea.KeyTab))
	if m.InputValue() != "52913" {
		t.Fatalf("input = %q", m.InputValue())
	}
}

func TestStage4BusyPortIsReportedInline(t *testing.T) {
	m, f := atLocalPortStage(t)
	f.busy[27017] = true

	m, _ = step(m, key(tea.KeyEnter))

	if m.Stage() != StageLocalPort {
		t.Fatal("advanced onto a busy port")
	}
	if !strings.Contains(m.InputError(), "27017") || !strings.Contains(m.InputError(), "in use") {
		t.Fatalf("error = %q", m.InputError())
	}
}

func TestStage4TypingReplacesTheDefault(t *testing.T) {
	m, _ := atLocalPortStage(t)
	m, _ = step(m, typeRunes("9"))
	if m.InputValue() != "9" {
		t.Fatalf("input = %q, want the default replaced", m.InputValue())
	}
}

func TestStage4EnterAdvancesToName(t *testing.T) {
	m, _ := atLocalPortStage(t)
	m, _ = step(m, key(tea.KeyEnter))
	if m.Stage() != StageName {
		t.Fatalf("stage = %v", m.Stage())
	}
}

func atNameStage(t *testing.T) (Model, *fakeSources) {
	t.Helper()
	m, f := atLocalPortStage(t)
	m, _ = step(m, key(tea.KeyEnter))
	return m, f
}

func TestStage5EmptyNameFinishesUnnamed(t *testing.T) {
	m, _ := atNameStage(t)
	m, cmd := step(m, key(tea.KeyEnter))

	res, ok := m.Result()
	if !ok || cmd == nil {
		t.Fatalf("not finished: ok=%v cmd=%v", ok, cmd != nil)
	}
	want := res.Spec
	if want.Host != "example-backend-dev" || want.Container != "app_mongo_staging" ||
		want.RemotePort != 27017 || want.LocalPort != 27017 || want.Name != "" || res.Named {
		t.Fatalf("result = %+v", res)
	}
}

func TestStage5NameIsValidatedThroughSources(t *testing.T) {
	m, f := atNameStage(t)
	f.badNames["Bad"] = "must be lower-case"
	m, _ = step(m, typeRunes("Bad"))
	m, _ = step(m, key(tea.KeyEnter))

	if _, ok := m.Result(); ok {
		t.Fatal("finished with an invalid name")
	}
	if !strings.Contains(m.InputError(), "lower-case") {
		t.Fatalf("error = %q", m.InputError())
	}
}

func TestStage5NameIsCarriedOnTheSpec(t *testing.T) {
	m, _ := atNameStage(t)
	m, _ = step(m, typeRunes("mongo-stg"))
	m, _ = step(m, key(tea.KeyEnter))
	res, ok := m.Result()
	if !ok || res.Spec.Name != "mongo-stg" {
		t.Fatalf("result = %+v", res)
	}
}

func TestStage5EscGoesBackToLocalPort(t *testing.T) {
	m, _ := atNameStage(t)
	m, _ = step(m, key(tea.KeyEsc))
	if m.Stage() != StageLocalPort || m.InputValue() != "27017" {
		t.Fatalf("stage=%v input=%q", m.Stage(), m.InputValue())
	}
}
