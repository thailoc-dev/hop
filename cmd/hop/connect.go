package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
)

// spawnWait is how long to wait for a freshly spawned daemon to start
// listening before giving up.
const spawnWait = 5 * time.Second

// maxDaemonLog is the size at which the daemon log is rotated. One previous
// generation is kept; the log is a debugging aid, not an audit trail.
const maxDaemonLog = 5 << 20 // 5 MiB

// connect returns a client for the running daemon, starting one if needed.
func connect(paths hopfs.Paths) (*control.Client, error) {
	if err := paths.EnsureDirs(); err != nil {
		return nil, err
	}

	client, err := control.Dial(paths.ControlSock)
	if err == nil {
		return client, nil
	}
	if !errors.Is(err, control.ErrNoDaemon) {
		return nil, err
	}

	release, err := acquireSpawnLock(paths)
	if err != nil {
		return nil, fmt.Errorf("acquire spawn lock: %w", err)
	}
	defer func() { _ = release() }()

	// Another caller may have won the race and started a daemon while this one
	// waited for the lock.
	if client, err := control.Dial(paths.ControlSock); err == nil {
		return client, nil
	}

	if err := clearStaleSocket(paths); err != nil {
		return nil, err
	}
	if err := spawnDaemon(paths); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(spawnWait)
	for time.Now().Before(deadline) {
		if client, err := control.Dial(paths.ControlSock); err == nil {
			return client, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fail(exitNoDaemon,
		"daemon did not start within %s; see %s", spawnWait, paths.DaemonLog)
}

// clearStaleSocket removes a socket file that no daemon is listening on. A
// live socket is left untouched.
func clearStaleSocket(paths hopfs.Paths) error {
	if _, err := os.Stat(paths.ControlSock); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if client, err := control.Dial(paths.ControlSock); err == nil {
		_ = client.Close()
		return nil // someone is home
	}
	if err := os.Remove(paths.ControlSock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket %s: %w", paths.ControlSock, err)
	}
	return nil
}

// acquireSpawnLock takes an exclusive flock, so two `hop` invocations racing
// to open the first tunnel produce one daemon rather than two.
func acquireSpawnLock(paths hopfs.Paths) (func() error, error) {
	f, err := os.OpenFile(paths.LockFile, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() error {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return f.Close()
	}, nil
}

// rotateLog moves an oversized log aside so it cannot grow without bound.
func rotateLog(path string, maxBytes int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() < maxBytes {
		return nil
	}
	if err := os.Rename(path, path+".1"); err != nil {
		return fmt.Errorf("rotate %s: %w", path, err)
	}
	return nil
}

// spawnDaemon re-executes this binary in daemon mode, detached from the
// terminal so it outlives the shell that started it.
//
// This is the one sanctioned exec.Command outside sshexec and sysprobe: hop
// re-executing itself is not spawning ssh.
func spawnDaemon(paths hopfs.Paths) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate hop binary: %w", err)
	}

	if err := rotateLog(paths.DaemonLog, maxDaemonLog); err != nil {
		return err
	}

	logFile, err := os.OpenFile(paths.DaemonLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(self, "__daemon")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Stdin = nil
	// Setsid detaches from the controlling terminal, so closing the tab that
	// opened the tunnel does not take the daemon with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// The daemon must find the same HOME the CLI did, which matters in tests.
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	// The daemon is not this process's business once started.
	return cmd.Process.Release()
}
