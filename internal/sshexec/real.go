package sshexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// terminateGrace is how long a forward gets to honour SIGTERM before it is
// killed outright.
const terminateGrace = 5 * time.Second

type realExecutor struct {
	socketDir string
}

// New returns an Executor that spawns real ssh processes. socketDir holds
// ControlMaster sockets for multiplexed command connections; see hopfs for
// why it is not always under the home directory.
func New(socketDir string) Executor { return &realExecutor{socketDir: socketDir} }

// commandArgs builds the ssh arguments for a short-lived remote command.
//
// These multiplex: container addresses are re-resolved on every retry, and
// paying a full TCP and key exchange each time would make backoff meaningless.
func commandArgs(socketDir, host string) []string {
	sum := sha256.Sum256([]byte(host))
	socket := filepath.Join(socketDir, "cmd-"+hex.EncodeToString(sum[:8])+".sock")
	return []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + socket,
		"-o", "ControlPersist=60",
		"-o", "BatchMode=yes",
		host,
	}
}

// forwardArgs builds the ssh arguments for the port forward itself.
//
// No ControlMaster here: a forward needs no out-of-band control channel, and
// a socket is one more thing that can leak. The keepalives make ssh exit
// within ~45s of a link that vanished without closing, and
// ExitOnForwardFailure makes it exit at once rather than sitting connected
// with no forward when the local bind fails.
func forwardArgs(spec ForwardSpec) []string {
	forward := fmt.Sprintf("127.0.0.1:%d:%s:%d",
		spec.LocalPort, spec.RemoteAddr, spec.RemotePort)
	return []string{
		"-N",
		"-L", forward,
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "BatchMode=yes",
		spec.Host,
	}
}

// shellSafe are the characters that need no quoting for a POSIX shell.
const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789" +
	"_-.,:/=@+"

// shellQuote makes one argument survive the remote shell.
//
// ssh does not take a remote argv: it joins the arguments it is given with
// spaces and hands the result to the login shell, which parses it a second
// time. An unquoted `{{.IPAddress}} {{end}}` therefore arrives as two words,
// and an unquoted `\t` arrives as a bare `t`. Both silently produce the wrong
// docker invocation rather than an error anyone would notice.
func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.IndexFunc(arg, func(r rune) bool {
		return !strings.ContainsRune(shellSafe, r)
	}) < 0 {
		return arg
	}
	// Single quotes protect everything except a single quote itself, which is
	// closed, escaped and reopened.
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// remoteCommand renders a command as one shell-safe string for ssh.
func remoteCommand(cmd []string) string {
	quoted := make([]string, len(cmd))
	for i, arg := range cmd {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func (e *realExecutor) Run(ctx context.Context, host string, cmd ...string) (Result, error) {
	args := commandArgs(e.socketDir, host)
	if len(cmd) > 0 {
		args = append(args, remoteCommand(cmd))
	}
	c := exec.CommandContext(ctx, "ssh", args...)

	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr

	err := c.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}

	// A non-zero exit is information, not a failure to run: ssh exits 255 for
	// almost everything, and the caller classifies stderr to decide what it
	// means. Only a failure to execute at all is returned as an error.
	var exitErr *exec.ExitError
	if err != nil {
		if !errors.As(err, &exitErr) {
			return result, fmt.Errorf("run ssh: %w", err)
		}
		result.ExitCode = exitErr.ExitCode()
	}
	return result, nil
}

// dockerIPFormat asks docker for the first network's address. A container on
// several networks yields them concatenated, which is why the result is
// validated by the caller rather than trusted.
const dockerIPFormat = `{{range.NetworkSettings.Networks}}{{.IPAddress}} {{end}}`

func (e *realExecutor) ContainerIP(ctx context.Context, host, container string) (string, error) {
	result, err := e.Run(ctx, host, "docker", "inspect", "-f", dockerIPFormat, container)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("inspect %s on %s: %s", container, host, strings.TrimSpace(result.Stderr))
	}

	fields := strings.Fields(result.Stdout)
	if len(fields) == 0 {
		return "", fmt.Errorf("container %s on %s has no network address", container, host)
	}
	return fields[0], nil
}

func (e *realExecutor) StartForward(ctx context.Context, spec ForwardSpec) (Proc, error) {
	c := exec.Command("ssh", forwardArgs(spec)...)

	// Its own process group, so Terminate can take down anything ssh spawned.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stderr lockedBuffer
	c.Stderr = &stderr

	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("start ssh: %w", err)
	}

	proc := &realProc{cmd: c, stderr: &stderr, done: make(chan struct{})}
	go func() {
		_ = c.Wait()
		close(proc.done)
	}()
	return proc, nil
}

func (e *realExecutor) Close(ctx context.Context) {
	matches, err := filepath.Glob(filepath.Join(e.socketDir, "cmd-*.sock"))
	if err != nil {
		return
	}
	for _, socket := range matches {
		_ = exec.CommandContext(ctx, "ssh", "-o", "ControlPath="+socket, "-O", "exit", "dummy").Run()
	}
}

type realProc struct {
	cmd    *exec.Cmd
	stderr *lockedBuffer
	done   chan struct{}
}

func (p *realProc) Done() <-chan struct{} { return p.done }
func (p *realProc) Stderr() string        { return p.stderr.String() }

// Terminate signals the whole process group, then escalates to SIGKILL if the
// process is still alive after terminateGrace.
func (p *realProc) Terminate() error {
	if p.cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(p.cmd.Process.Pid)
	if err != nil {
		pgid = p.cmd.Process.Pid
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	select {
	case <-p.done:
		return nil
	case <-time.After(terminateGrace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return nil
	}
}

// lockedBuffer is a bytes.Buffer that is safe to read while os/exec's copier
// goroutine writes to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
