package sshexec

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Fake is an Executor that spawns nothing. Tests drive it to reproduce
// container restarts, auth failures and dropped links deterministically.
type Fake struct {
	mu        sync.Mutex
	ips       map[string]string
	ipErrs    map[string]error
	runResult Result
	runErr    error
	forwards  []ForwardSpec
	procs     []*FakeProc
	startErr  error
	runDelay  time.Duration
}

func NewFake() *Fake {
	return &Fake{ips: map[string]string{}, ipErrs: map[string]error{}}
}

func key(host, container string) string { return host + "\x00" + container }

func (f *Fake) SetContainerIP(host, container, ip string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ips[key(host, container)] = ip
	delete(f.ipErrs, key(host, container))
}

func (f *Fake) SetContainerErr(host, container string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ipErrs[key(host, container)] = err
	delete(f.ips, key(host, container))
}

func (f *Fake) SetRunResult(r Result, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runResult, f.runErr = r, err
}

// SetStartForwardErr makes the next StartForward fail, for the local-port
// conflict path.
func (f *Fake) SetStartForwardErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startErr = err
}

func (f *Fake) ContainerIP(_ context.Context, host, container string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.ipErrs[key(host, container)]; ok {
		return "", err
	}
	ip, ok := f.ips[key(host, container)]
	if !ok {
		return "", fmt.Errorf("Error: No such object: %s", container)
	}
	return ip, nil
}

// SetRunDelay makes Run block for d before returning, so completion timeouts
// can be tested without an unreachable host.
func (f *Fake) SetRunDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runDelay = d
}

func (f *Fake) Run(ctx context.Context, _ string, _ ...string) (Result, error) {
	f.mu.Lock()
	delay, result, err := f.runDelay, f.runResult, f.runErr
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	return result, err
}

func (f *Fake) StartForward(_ context.Context, spec ForwardSpec) (Proc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		err := f.startErr
		f.startErr = nil
		return nil, err
	}
	f.forwards = append(f.forwards, spec)
	proc := NewFakeProc()
	f.procs = append(f.procs, proc)
	return proc, nil
}

func (f *Fake) Close(context.Context) {}

// Forwards returns every ForwardSpec passed to StartForward, in order. Tests
// assert on this to prove the address was re-resolved between attempts.
func (f *Fake) Forwards() []ForwardSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ForwardSpec(nil), f.forwards...)
}

// LastForward returns the most recently started process, or nil.
func (f *Fake) LastForward() *FakeProc {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.procs) == 0 {
		return nil
	}
	return f.procs[len(f.procs)-1]
}

// FakeProc is a forward that exits only when a test says so.
type FakeProc struct {
	mu        sync.Mutex
	stderr    string
	done      chan struct{}
	closed    bool
	terminate int
}

func NewFakeProc() *FakeProc { return &FakeProc{done: make(chan struct{})} }

func (p *FakeProc) Done() <-chan struct{} { return p.done }

func (p *FakeProc) Stderr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr
}

func (p *FakeProc) Terminate() error {
	p.mu.Lock()
	p.terminate++
	p.mu.Unlock()
	p.Die()
	return nil
}

func (p *FakeProc) TerminateCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.terminate
}

// EmitStderr appends a line the state machine will classify when the process
// dies.
func (p *FakeProc) EmitStderr(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stderr != "" {
		p.stderr += "\n"
	}
	p.stderr += line
}

// Die ends the process. It is safe to call more than once.
func (p *FakeProc) Die() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	close(p.done)
}
