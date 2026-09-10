// Package hopfs owns every path hop reads or writes. Nothing else builds
// paths, so there is exactly one place to look when asking where state lives.
package hopfs

import (
	"fmt"
	"os"
	"path/filepath"
)

// socketBudget is the usable length of a unix socket path.
//
// sun_path is 104 bytes on darwin. While establishing a ControlMaster, ssh
// binds an intermediate socket by appending a 17-byte random suffix to the
// path it was given, so the path hop supplies must leave room for it:
// 104 - 17 - 1 (NUL) = 86.
const socketBudget = 86

// longestSocketName is the longest basename hopfs will place in SocketDir:
// "cmd-" + 16 hex characters + ".sock".
const longestSocketName = "cmd-0123456789abcdef.sock"

// Paths is the resolved location of every file hop owns.
type Paths struct {
	Root        string // ~/.hop
	StateFile   string // ~/.hop/state.json
	ControlSock string // ~/.hop/ctl.sock
	LockFile    string // ~/.hop/daemon.lock
	DaemonLog   string // ~/.hop/daemon.log
	LogDir      string // ~/.hop/logs
	CacheDir    string // ~/.hop/cache
	SocketDir   string // ~/.hop/ctl, or /tmp/hop-<uid> when the budget is blown
}

// New resolves paths for the given home directory and uid. It is pure, so
// tests can exercise the fallback without a long real home directory.
func New(home string, uid int) Paths {
	root := filepath.Join(home, ".hop")

	socketDir := filepath.Join(root, "ctl")
	if len(filepath.Join(socketDir, longestSocketName)) > socketBudget {
		socketDir = fmt.Sprintf("/tmp/hop-%d", uid)
	}

	return Paths{
		Root:        root,
		StateFile:   filepath.Join(root, "state.json"),
		ControlSock: filepath.Join(root, "ctl.sock"),
		LockFile:    filepath.Join(root, "daemon.lock"),
		DaemonLog:   filepath.Join(root, "daemon.log"),
		LogDir:      filepath.Join(root, "logs"),
		CacheDir:    filepath.Join(root, "cache"),
		SocketDir:   socketDir,
	}
}

// Default resolves paths for the current user.
func Default() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locate home directory: %w", err)
	}
	return New(home, os.Getuid()), nil
}

// EnsureDirs creates every directory hop writes into. They are 0700 because
// anyone who can connect to the control socket can open a tunnel.
func (p Paths) EnsureDirs() error {
	for _, dir := range []string{p.Root, p.LogDir, p.CacheDir, p.SocketDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}
