package sysprobe

import (
	"fmt"
	"sync"
)

// Fake is a Prober that spawns nothing.
type Fake struct {
	mu       sync.Mutex
	busy     map[int]Holder
	route    string
	routeErr error
}

func NewFake() *Fake { return &Fake{busy: map[int]Holder{}} }

func (f *Fake) SetPortBusy(port int, h Holder) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy[port] = h
}

func (f *Fake) SetPortFree(port int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.busy, port)
}

func (f *Fake) SetDefaultRoute(gateway string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.route, f.routeErr = gateway, nil
}

func (f *Fake) PortFree(port int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, busy := f.busy[port]
	return !busy
}

func (f *Fake) PortHolder(port int) (Holder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.busy[port]
	if !ok {
		return Holder{}, fmt.Errorf("nothing is listening on port %d", port)
	}
	return h, nil
}

func (f *Fake) DefaultRoute() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.route, f.routeErr
}
