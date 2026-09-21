package picker

import (
	"context"
	"errors"

	"github.com/thailoc-dev/hop/internal/complete"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// fakeSources is the picker's world in tests: nothing is fetched, everything
// is configured.
type fakeSources struct {
	tunnels    []tunnel.Status
	hosts      []string
	containers map[string][]complete.Container
	fetchErr   error
	busy       map[int]bool
	freePort   int
	badNames   map[string]string
	fetched    []string
}

func newFake() *fakeSources {
	return &fakeSources{
		containers: map[string][]complete.Container{},
		busy:       map[int]bool{},
		badNames:   map[string]string{},
		freePort:   52913,
	}
}

func (f *fakeSources) Tunnels() []tunnel.Status { return f.tunnels }
func (f *fakeSources) Hosts() []string          { return f.hosts }

func (f *fakeSources) Containers(_ context.Context, host string) ([]complete.Container, error) {
	f.fetched = append(f.fetched, host)
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.containers[host], nil
}

func (f *fakeSources) PortFree(port int) bool { return !f.busy[port] }
func (f *fakeSources) FreePort() (int, error) { return f.freePort, nil }

func (f *fakeSources) ValidateName(name string) error {
	if msg, bad := f.badNames[name]; bad {
		return errors.New(msg)
	}
	return nil
}

func savedRedis() tunnel.Status {
	return tunnel.Status{
		Spec: tunnel.Spec{Name: "redis-stg", Host: "example-tracker-dev", Container: "tracker_redis_staging",
			RemotePort: 6379, LocalPort: 46379, Env: "stg"},
		State: tunnel.StateSaved,
	}
}

func runningMongo() tunnel.Status {
	return tunnel.Status{
		Spec: tunnel.Spec{Name: "mongo-dev", Host: "example-backend-dev", Container: "app_mongo_staging",
			RemotePort: 27017, LocalPort: 27018, Env: "dev"},
		State: tunnel.StateHealthy,
	}
}
