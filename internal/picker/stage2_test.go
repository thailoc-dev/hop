package picker

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thailoc-dev/hop/internal/complete"
)

func atContainerStage(t *testing.T) (Model, *fakeSources) {
	t.Helper()
	f := newFake()
	f.hosts = []string{"example-backend-dev"}
	m := New(f)
	m, cmd := step(m, key(tea.KeyEnter))
	if m.Stage() != StageContainer || cmd == nil {
		t.Fatalf("setup: stage=%v cmd=%v", m.Stage(), cmd != nil)
	}
	return m, f
}

func TestStage2ShowsASpinnerWhileFetching(t *testing.T) {
	m, _ := atContainerStage(t)
	if !m.Fetching() {
		t.Fatal("not fetching")
	}
	if !strings.Contains(m.View(), "fetching containers on example-backend-dev") {
		t.Fatalf("view:\n%s", m.View())
	}
}

func TestStage2ListsContainersOnceFetched(t *testing.T) {
	m, _ := atContainerStage(t)

	m, _ = step(m, containersMsg{host: "example-backend-dev", containers: []complete.Container{
		{Name: "app_mongo_staging", Ports: []int{27017}},
		{Name: "app_redis", Ports: []int{6379}},
	}})

	if m.Fetching() {
		t.Fatal("still fetching")
	}
	got := labels(m.Visible())
	if len(got) != 2 || got[0] != "app_mongo_staging" {
		t.Fatalf("visible = %v", got)
	}
}

func TestStage2IgnoresAStaleFetchForAnotherHost(t *testing.T) {
	m, _ := atContainerStage(t)
	m, _ = step(m, containersMsg{host: "somewhere-else", containers: []complete.Container{{Name: "x"}}})
	if len(m.Visible()) != 0 || !m.Fetching() {
		t.Fatal("accepted a result for a host we did not ask about")
	}
}

func TestStage2EnterOnAContainerAdvances(t *testing.T) {
	m, _ := atContainerStage(t)
	m, _ = step(m, containersMsg{host: "example-backend-dev", containers: []complete.Container{
		{Name: "app_mongo_staging", Ports: []int{27017}},
	}})

	m, _ = step(m, key(tea.KeyEnter))

	if m.Stage() != StageRemotePort {
		t.Fatalf("stage = %v", m.Stage())
	}
	if m.Container().Name != "app_mongo_staging" {
		t.Fatalf("container = %+v", m.Container())
	}
}

func TestStage2FilterNarrowsContainers(t *testing.T) {
	m, _ := atContainerStage(t)
	m, _ = step(m, containersMsg{host: "example-backend-dev", containers: []complete.Container{
		{Name: "app_mongo_staging"}, {Name: "app_redis"},
	}})

	m, _ = step(m, typeRunes("red"))

	if got := labels(m.Visible()); len(got) != 1 || got[0] != "app_redis" {
		t.Fatalf("visible = %v", got)
	}
}

func TestStage2FetchErrorAllowsManualEntry(t *testing.T) {
	m, _ := atContainerStage(t)
	m, _ = step(m, containersMsg{host: "example-backend-dev", err: errors.New("Connection timed out")})

	if !strings.Contains(m.View(), "Connection timed out") {
		t.Fatalf("view:\n%s", m.View())
	}
	m, _ = step(m, typeRunes("app_mongo_staging"))
	m, _ = step(m, key(tea.KeyEnter))

	if m.Stage() != StageRemotePort || m.Container().Name != "app_mongo_staging" {
		t.Fatalf("stage=%v container=%+v", m.Stage(), m.Container())
	}
}

func TestStage2EnterWithNothingSelectedAndNothingTypedDoesNothing(t *testing.T) {
	m, _ := atContainerStage(t)
	m, _ = step(m, containersMsg{host: "example-backend-dev", err: errors.New("nope")})
	m, _ = step(m, key(tea.KeyEnter))
	if m.Stage() != StageContainer {
		t.Fatalf("advanced with no container: %v", m.Stage())
	}
}

func TestStage2EscGoesBackToTarget(t *testing.T) {
	m, _ := atContainerStage(t)
	m, _ = step(m, key(tea.KeyEsc))
	if m.Stage() != StageTarget || m.Host() != "" {
		t.Fatalf("stage=%v host=%q", m.Stage(), m.Host())
	}
	if len(m.Visible()) != 1 {
		t.Fatalf("visible = %v", labels(m.Visible()))
	}
}
