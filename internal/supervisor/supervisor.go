// Package supervisor owns the set of running tunnels and answers control
// requests about them.
package supervisor

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/sshexec"
	"github.com/locnguyen/hop/internal/store"
	"github.com/locnguyen/hop/internal/sysprobe"
	"github.com/locnguyen/hop/internal/tunnel"
)

// WatchInterval is how often the supervisor checks for a system wake or a
// changed default route.
const WatchInterval = 10 * time.Second

// SleepThreshold is the wall-versus-monotonic discrepancy that means the
// machine was suspended. Anything smaller is ordinary scheduling noise or an
// NTP correction, neither of which is worth recycling a working tunnel for.
const SleepThreshold = 30 * time.Second

// Config is everything the supervisor needs from the outside world.
type Config struct {
	StatePath string
	Exec      sshexec.Executor
	Probe     sysprobe.Prober
	Clock     tunnel.Clock
}

type entry struct {
	tun    *tunnel.Tunnel
	cancel context.CancelFunc
}

// Supervisor keys tunnels by local port, which is what every command uses to
// address them.
type Supervisor struct {
	cfg Config

	mu      sync.Mutex
	tunnels map[int]*entry
	ctx     context.Context

	emptyOnce sync.Once
	empty     chan struct{}
	seed      int64
}

func New(cfg Config) *Supervisor {
	if cfg.Clock == nil {
		cfg.Clock = tunnel.RealClock()
	}
	return &Supervisor{
		cfg:     cfg,
		tunnels: map[int]*entry{},
		empty:   make(chan struct{}),
	}
}

// Empty is closed when the last tunnel is removed, which is the daemon's cue
// to exit. It fires once: a supervisor that has gone empty is finished.
func (s *Supervisor) Empty() <-chan struct{} { return s.empty }

// Run restores persisted tunnels and blocks until ctx is cancelled.
func (s *Supervisor) Run(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()

	saved, err := store.Load(s.cfg.StatePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Close any ControlMaster sockets a previous daemon left behind, then take
	// back the local ports its forwards are still holding.
	s.cfg.Exec.Close(ctx)
	s.reapOrphans(saved.Tunnels)

	for _, spec := range saved.Tunnels {
		if _, err := s.start(spec); err != nil {
			// A tunnel that cannot start must not stop the others.
			continue
		}
	}

	go s.watch(ctx)

	<-ctx.Done()
	s.stopAll()
	return nil
}

// watch recycles every tunnel when the machine wakes from sleep or moves to a
// different network.
//
// Neither event closes the TCP connection cleanly, so ssh does not notice for
// up to 45 seconds — and after a long suspend it may never notice, because the
// peer has long since dropped the session. Recycling proactively is what makes
// reopening a laptop lid produce a working tunnel instead of a hung client.
func (s *Supervisor) watch(ctx context.Context) {
	lastWall := s.cfg.Clock.Now()
	lastMono := s.cfg.Clock.Mono()
	lastRoute, _ := s.cfg.Probe.DefaultRoute()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.cfg.Clock.After(WatchInterval):
		}

		wall := s.cfg.Clock.Now()
		mono := s.cfg.Clock.Mono()

		// Wall time advances during suspend; monotonic time does not. A gap
		// between the two is the machine having been asleep for the difference.
		drift := wall.Sub(lastWall) - (mono - lastMono)
		lastWall, lastMono = wall, mono

		if drift >= SleepThreshold {
			s.recycleAll(fmt.Sprintf("system slept for %s", drift.Round(time.Second)))
			continue
		}

		route, err := s.cfg.Probe.DefaultRoute()
		if err != nil {
			continue // a probe failure is not evidence of a change
		}
		if route != lastRoute {
			// Assign before recycling so a machine that stays offline (route
			// "") settles after one recycle instead of firing every tick.
			previous := lastRoute
			lastRoute = route
			s.recycleAll(fmt.Sprintf("default route changed from %q to %q", previous, route))
		}
	}
}

func (s *Supervisor) recycleAll(reason string) {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.tunnels))
	for _, e := range s.tunnels {
		entries = append(entries, e)
	}
	s.mu.Unlock()

	if len(entries) > 0 {
		log.Printf("recycling %d tunnel(s): %s", len(entries), reason)
	}
	for _, e := range entries {
		e.tun.Recycle()
	}
}

// Handle implements control.Handler.
func (s *Supervisor) Handle(req control.Request) control.Response {
	switch req.Op {
	case control.OpPing:
		return control.Response{OK: true}
	case control.OpAdd:
		return s.handleAdd(req)
	case control.OpList:
		return control.Response{OK: true, Statuses: s.statuses()}
	case control.OpRemove:
		return s.handleRemove(req)
	case control.OpRestart:
		return s.handleRestart(req)
	case control.OpEvents:
		return s.handleEvents(req)
	case control.OpMarkDegraded:
		return s.handleMarkDegraded(req)
	default:
		return control.Response{OK: false, Error: "unknown operation " + req.Op}
	}
}

func (s *Supervisor) handleAdd(req control.Request) control.Response {
	if req.Spec == nil {
		return control.Response{OK: false, Error: "add requires a spec"}
	}
	spec := *req.Spec

	s.mu.Lock()
	if existing, ok := s.tunnels[spec.LocalPort]; ok {
		status := existing.tun.Status()
		s.mu.Unlock()
		return control.Response{OK: false, Error: fmt.Sprintf(
			"local port %d is already forwarding %s on %s",
			spec.LocalPort, status.Spec.Container, status.Spec.Host)}
	}
	s.mu.Unlock()

	if _, err := s.start(spec); err != nil {
		return control.Response{OK: false, Error: err.Error()}
	}
	if err := s.persist(); err != nil {
		return control.Response{OK: false, Error: err.Error()}
	}
	return control.Response{OK: true, Statuses: s.statuses()}
}

func (s *Supervisor) handleRemove(req control.Request) control.Response {
	var ports []int

	s.mu.Lock()
	if req.All {
		for port := range s.tunnels {
			ports = append(ports, port)
		}
	} else {
		if _, ok := s.tunnels[req.LocalPort]; !ok {
			s.mu.Unlock()
			return control.Response{OK: false,
				Error: fmt.Sprintf("no tunnel on local port %d", req.LocalPort)}
		}
		ports = []int{req.LocalPort}
	}
	for _, port := range ports {
		e := s.tunnels[port]
		e.tun.Stop()
		e.cancel()
		delete(s.tunnels, port)
	}
	remaining := len(s.tunnels)
	s.mu.Unlock()

	if err := s.persist(); err != nil {
		return control.Response{OK: false, Error: err.Error()}
	}
	if remaining == 0 {
		s.emptyOnce.Do(func() { close(s.empty) })
	}
	return control.Response{OK: true}
}

func (s *Supervisor) handleRestart(req control.Request) control.Response {
	s.mu.Lock()
	e, ok := s.tunnels[req.LocalPort]
	s.mu.Unlock()
	if !ok {
		return control.Response{OK: false,
			Error: fmt.Sprintf("no tunnel on local port %d", req.LocalPort)}
	}
	e.tun.Recycle()
	return control.Response{OK: true}
}

func (s *Supervisor) handleEvents(req control.Request) control.Response {
	s.mu.Lock()
	e, ok := s.tunnels[req.LocalPort]
	s.mu.Unlock()
	if !ok {
		return control.Response{OK: false,
			Error: fmt.Sprintf("no tunnel on local port %d", req.LocalPort)}
	}

	events := e.tun.Events()
	if req.Limit > 0 && len(events) > req.Limit {
		events = events[len(events)-req.Limit:]
	}
	return control.Response{OK: true, Events: events}
}

// handleMarkDegraded is how `hop ls` reports a probe failure back, since it is
// the only thing that probes.
func (s *Supervisor) handleMarkDegraded(req control.Request) control.Response {
	s.mu.Lock()
	e, ok := s.tunnels[req.LocalPort]
	s.mu.Unlock()
	if !ok {
		return control.Response{OK: false,
			Error: fmt.Sprintf("no tunnel on local port %d", req.LocalPort)}
	}
	e.tun.MarkDegraded(req.Reason)
	return control.Response{OK: true}
}

func (s *Supervisor) start(spec tunnel.Spec) (*tunnel.Tunnel, error) {
	s.mu.Lock()
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	s.seed++
	seed := s.seed
	s.mu.Unlock()

	tun := tunnel.New(spec, tunnel.Deps{
		Exec: s.cfg.Exec, Probe: s.cfg.Probe, Clock: s.cfg.Clock, Seed: seed,
	})
	ctx, cancel := context.WithCancel(parent)

	s.mu.Lock()
	s.tunnels[spec.LocalPort] = &entry{tun: tun, cancel: cancel}
	s.mu.Unlock()

	go tun.Run(ctx)
	return tun, nil
}

// reapOrphans frees the local ports of forwards left by a daemon that was
// killed rather than shut down.
//
// It is deliberately narrow: only ports named in our own state file, and only
// processes named ssh. Anything else on the port is somebody else's and is
// reported as a conflict instead.
func (s *Supervisor) reapOrphans(specs []tunnel.Spec) {
	for _, spec := range specs {
		if s.cfg.Probe.PortFree(spec.LocalPort) {
			continue
		}
		holder, err := s.cfg.Probe.PortHolder(spec.LocalPort)
		if err != nil || holder.Command != "ssh" {
			continue
		}
		log.Printf("reaping orphaned ssh (pid %d) holding local port %d",
			holder.PID, spec.LocalPort)
		if err := s.cfg.Probe.Kill(holder.PID); err != nil {
			log.Printf("could not reap pid %d: %v", holder.PID, err)
		}
	}
}

func (s *Supervisor) statuses() []tunnel.Status {
	s.mu.Lock()
	out := make([]tunnel.Status, 0, len(s.tunnels))
	for _, e := range s.tunnels {
		out = append(out, e.tun.Status())
	}
	s.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].Spec.LocalPort < out[j].Spec.LocalPort
	})
	return out
}

func (s *Supervisor) persist() error {
	specs := make([]tunnel.Spec, 0)
	for _, st := range s.statuses() {
		specs = append(specs, st.Spec)
	}
	if err := store.Save(s.cfg.StatePath, store.File{
		Version: store.CurrentVersion, Tunnels: specs,
	}); err != nil {
		return fmt.Errorf("persist state: %w", err)
	}
	return nil
}

func (s *Supervisor) stopAll() {
	s.mu.Lock()
	entries := make([]*entry, 0, len(s.tunnels))
	for _, e := range s.tunnels {
		entries = append(entries, e)
	}
	s.mu.Unlock()

	for _, e := range entries {
		e.tun.Stop()
		e.cancel()
	}
	s.cfg.Exec.Close(context.Background())
}
