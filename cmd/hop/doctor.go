package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/thailoc-dev/hop/internal/complete"
	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/hopfs"
	"github.com/thailoc-dev/hop/internal/sshconfig"
	"github.com/thailoc-dev/hop/internal/sshexec"
	"github.com/thailoc-dev/hop/internal/store"
	"github.com/thailoc-dev/hop/internal/sysprobe"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// hostProbeTimeout bounds each `docker ps` doctor runs. Hosts are probed one
// at a time, so this is also the most one unreachable host can cost.
const hostProbeTimeout = 5 * time.Second

type level int

const (
	levelOK level = iota
	levelWarn
	levelFail
)

func (l level) String() string {
	switch l {
	case levelWarn:
		return "warn"
	case levelFail:
		return "fail"
	default:
		return "ok"
	}
}

type findingKind int

const (
	kindLocal findingKind = iota
	kindHost
)

// finding is one line of the doctor's report.
type finding struct {
	name   string
	kind   findingKind
	level  level
	detail string
}

// doctorEnv is everything the checks look at, as seams so each check can be
// tested against fakes: a machine with no lsof, a PATH that resolves another
// hop, a host that does not answer.
type doctorEnv struct {
	paths       hopfs.Paths
	home        string
	version     string
	shell       string
	lookPath    func(name string) (string, error)
	executable  func() (string, error)
	probe       sysprobe.Prober
	exec        sshexec.Executor // nil means offline: no host is probed
	hostTimeout time.Duration
}

// diagnose runs every check in report order. Host probes come last and run
// sequentially: the VPS rate-limits bursts of connections, and a doctor
// that trips the limit it is meant to diagnose is no doctor.
func diagnose(ctx context.Context, env *doctorEnv) []finding {
	catalogue, catalogueErr := store.LoadCatalogue(env.paths.CatalogueFile)
	running := runningTunnels(env.paths)

	findings := []finding{
		checkBinary(env),
		checkSSH(env),
		checkLsof(env),
		checkHopDir(env, catalogue, catalogueErr),
		checkSocket(env),
		checkDaemon(env, running),
		checkPorts(env, catalogue, running),
	}
	if f, ok := checkCompletion(env); ok {
		findings = append(findings, f)
	}
	if env.exec != nil {
		findings = append(findings, checkHosts(ctx, env, catalogue)...)
	}
	return findings
}

// runningTunnels asks the daemon for its list. No daemon, or a daemon that
// will not answer, is an empty list here; checkDaemon reports on it.
func runningTunnels(paths hopfs.Paths) []tunnel.Status {
	client, err := control.Dial(paths.ControlSock)
	if err != nil {
		return nil
	}
	defer func() { _ = client.Close() }()
	resp, err := client.Send(control.Request{Op: control.OpList})
	if err != nil {
		return nil
	}
	return resp.Statuses
}

// checkBinary catches the stale-binary trap: `make install` put a new hop in
// one place while the shell keeps running an older one from another.
func checkBinary(env *doctorEnv) finding {
	self, err := env.executable()
	if err != nil {
		return finding{name: "hop", level: levelWarn, detail: fmt.Sprintf("%s, cannot locate own binary: %v", env.version, err)}
	}
	onPath, err := env.lookPath("hop")
	if err != nil {
		return finding{name: "hop", level: levelWarn, detail: fmt.Sprintf("%s at %s — not on PATH", env.version, self)}
	}
	if !sameFile(self, onPath) {
		return finding{name: "hop", level: levelWarn,
			detail: fmt.Sprintf("%s at %s, but PATH resolves %s — a stale install?", env.version, self, onPath)}
	}
	return finding{name: "hop", level: levelOK, detail: fmt.Sprintf("%s at %s", env.version, tildeify(env.home, self))}
}

func sameFile(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ia, ib)
}

func checkSSH(env *doctorEnv) finding {
	sshPath, err := env.lookPath("ssh")
	if err != nil {
		return finding{name: "ssh", level: levelFail, detail: "no ssh on PATH — nothing can be forwarded"}
	}
	config := filepath.Join(env.home, ".ssh", "config")
	if _, err := os.Stat(config); err != nil {
		return finding{name: "ssh", level: levelWarn, detail: sshPath + ", no ~/.ssh/config — hosts must be typed in full"}
	}
	hosts, err := sshconfig.Hosts(config)
	if err != nil {
		return finding{name: "ssh", level: levelWarn, detail: fmt.Sprintf("%s, ~/.ssh/config: %v", sshPath, err)}
	}
	return finding{name: "ssh", level: levelOK, detail: fmt.Sprintf("%s, ~/.ssh/config has %s", sshPath, plural(len(hosts), "host"))}
}

func checkLsof(env *doctorEnv) finding {
	if _, err := env.lookPath("lsof"); err != nil {
		return finding{name: "lsof", level: levelWarn,
			detail: "not on PATH — busy-port and orphan detection cannot name the holder"}
	}
	return finding{name: "lsof", level: levelOK, detail: "found (port ownership and orphan detection work)"}
}

func checkHopDir(env *doctorEnv, catalogue store.Catalogue, catalogueErr error) finding {
	p := env.paths
	if _, err := os.Stat(p.Root); err != nil {
		return finding{name: "~/.hop", level: levelOK, detail: "not created yet (first tunnel does)"}
	}
	if _, err := store.Load(p.StateFile); err != nil {
		return finding{name: "~/.hop", level: levelFail, detail: fmt.Sprintf("state.json: %v", err)}
	}
	if catalogueErr != nil {
		return finding{name: "~/.hop", level: levelFail, detail: fmt.Sprintf("tunnels.json: %v", catalogueErr)}
	}
	// The store sets aside what it cannot parse and starts afresh, which is
	// right for a daemon and invisible to a person: say so here.
	if corrupt, _ := filepath.Glob(filepath.Join(p.Root, "*.corrupt")); len(corrupt) > 0 {
		var names []string
		for _, path := range corrupt {
			names = append(names, filepath.Base(path))
		}
		return finding{name: "~/.hop", level: levelWarn,
			detail: strings.Join(names, ", ") + " set aside unreadable — its contents were not carried over"}
	}
	parts := []string{"state.json ok", plural(len(catalogue.Tunnels), "saved tunnel")}
	if entries, err := os.ReadDir(p.CacheDir); err == nil {
		parts = append(parts, fmt.Sprintf("cache %s", plural(len(entries), "host")))
	}
	if info, err := os.Stat(p.DaemonLog); err == nil {
		parts = append(parts, fmt.Sprintf("log %s", humanBytes(info.Size())))
	}
	return finding{name: "~/.hop", level: levelOK, detail: strings.Join(parts, ", ")}
}

func checkSocket(env *doctorEnv) finding {
	p := env.paths
	detail := fmt.Sprintf("%s (%d bytes of %d budget)", p.ControlSock, len(p.ControlSock), hopfs.SocketBudget)
	if filepath.Dir(p.SocketDir) != p.Root {
		detail += fmt.Sprintf("; command sockets fell back to %s — HOME is long", p.SocketDir)
	}
	return finding{name: "socket", level: levelOK, detail: detail}
}

func checkDaemon(env *doctorEnv, running []tunnel.Status) finding {
	p := env.paths
	client, err := control.Dial(p.ControlSock)
	if err != nil {
		if _, statErr := os.Stat(p.ControlSock); statErr == nil {
			return finding{name: "daemon", level: levelWarn,
				detail: "stale socket: nothing answers on " + p.ControlSock + " — the next hop command respawns it"}
		}
		return finding{name: "daemon", level: levelOK, detail: "not running (nothing open)"}
	}
	_, err = client.Send(control.Request{Op: control.OpPing})
	_ = client.Close()
	if err != nil {
		return finding{name: "daemon", level: levelFail, detail: fmt.Sprintf("connected but ping failed: %v", err)}
	}
	if len(running) == 0 {
		return finding{name: "daemon", level: levelOK, detail: "running, no tunnels"}
	}
	return finding{name: "daemon", level: levelOK, detail: "running, " + stateSummary(running)}
}

// stateSummary counts tunnels by state, worst first: "1 degraded, 2 healthy".
func stateSummary(statuses []tunnel.Status) string {
	counts := map[tunnel.State]int{}
	for _, st := range statuses {
		counts[st.State]++
	}
	order := []tunnel.State{tunnel.StateFatal, tunnel.StateDegraded, tunnel.StateRetrying,
		tunnel.StateConnecting, tunnel.StateResolving, tunnel.StateHealthy, tunnel.StateStopped}
	var parts []string
	for _, state := range order {
		if n := counts[state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, state))
		}
	}
	return strings.Join(parts, ", ")
}

// checkPorts answers "why won't redis-stg start": a saved tunnel whose local
// port something else now holds.
func checkPorts(env *doctorEnv, catalogue store.Catalogue, running []tunnel.Status) finding {
	live := map[int]bool{}
	for _, st := range running {
		live[st.Spec.LocalPort] = true
	}
	var held []string
	for _, name := range sortedNames(catalogue.Tunnels) {
		spec := catalogue.Tunnels[name]
		if live[spec.LocalPort] || env.probe.PortFree(spec.LocalPort) {
			continue
		}
		holder, err := env.probe.PortHolder(spec.LocalPort)
		if err != nil {
			held = append(held, fmt.Sprintf("%s: %d is busy", name, spec.LocalPort))
			continue
		}
		held = append(held, fmt.Sprintf("%s: %d is held by %s (pid %d)", name, spec.LocalPort, holder.Command, holder.PID))
	}
	if len(held) > 0 {
		return finding{name: "ports", level: levelWarn, detail: strings.Join(held, "; ")}
	}
	return finding{name: "ports", level: levelOK, detail: "no saved tunnel's local port is held by something else"}
}

// checkCompletion looks for the eval line in ~/.zshrc. Only zsh is checked:
// it is the macOS default and the one the README walks through.
func checkCompletion(env *doctorEnv) (finding, bool) {
	if filepath.Base(env.shell) != "zsh" {
		return finding{}, false
	}
	body, err := os.ReadFile(filepath.Join(env.home, ".zshrc"))
	if err == nil && strings.Contains(string(body), "hop completion") {
		return finding{name: "completion", level: levelOK, detail: "zsh: ~/.zshrc sources hop completion"}, true
	}
	return finding{name: "completion", level: levelWarn,
		detail: `zsh: ~/.zshrc does not source it — add eval "$(hop completion zsh)"`}, true
}

// checkHosts runs `docker ps` on each host a saved tunnel uses, one at a
// time, each bounded by hostTimeout.
func checkHosts(ctx context.Context, env *doctorEnv, catalogue store.Catalogue) []finding {
	seen := map[string]bool{}
	var hosts []string
	for _, spec := range catalogue.Tunnels {
		if !seen[spec.Host] {
			seen[spec.Host] = true
			hosts = append(hosts, spec.Host)
		}
	}
	sort.Strings(hosts)

	var out []finding
	for _, host := range hosts {
		out = append(out, probeHost(ctx, env, host))
	}
	return out
}

func probeHost(ctx context.Context, env *doctorEnv, host string) finding {
	ctx, cancel := context.WithTimeout(ctx, env.hostTimeout)
	defer cancel()
	started := time.Now()
	result, err := env.exec.Run(ctx, host, "docker", "ps", "--format", complete.PSFormat)
	took := time.Since(started).Round(100 * time.Millisecond)
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return finding{name: host, kind: kindHost, level: levelFail,
			detail: fmt.Sprintf("ssh: no answer within %s", env.hostTimeout)}
	case err != nil:
		return finding{name: host, kind: kindHost, level: levelFail, detail: firstLine(err.Error())}
	case result.ExitCode != 0:
		return finding{name: host, kind: kindHost, level: levelFail,
			detail: "docker ps: " + firstLine(strings.TrimSpace(result.Stderr))}
	}
	containers := complete.ParsePS(result.Stdout)
	return finding{name: host, kind: kindHost, level: levelOK,
		detail: fmt.Sprintf("docker ps ok, %s (%s)", plural(len(containers), "container"), took)}
}

// reportFindings prints the report and turns problems into the exit code.
// Warnings are advice; only a failure makes doctor exit non-zero.
func reportFindings(w io.Writer, findings []finding, colour bool) error {
	var local, hosts []finding
	for _, f := range findings {
		if f.kind == kindHost {
			hosts = append(hosts, f)
		} else {
			local = append(local, f)
		}
	}
	problems, warnings := printBlock(w, local, colour)
	if len(hosts) > 0 {
		fmt.Fprintln(w)
		p, n := printBlock(w, hosts, colour)
		problems, warnings = problems+p, warnings+n
	}

	fmt.Fprintln(w)
	switch {
	case problems == 0 && warnings == 0:
		fmt.Fprintln(w, "all good")
		return nil
	case problems == 0:
		fmt.Fprintln(w, plural(warnings, "warning"))
		return nil
	default:
		summary := plural(problems, "problem")
		if warnings > 0 {
			summary += ", " + plural(warnings, "warning")
		}
		fmt.Fprintln(w, summary)
		return fail(exitFatal, "%s", summary)
	}
}

// printBlock writes one aligned group of findings and counts what it saw.
func printBlock(w io.Writer, findings []finding, colour bool) (problems, warnings int) {
	width := 0
	for _, f := range findings {
		if n := len(f.name); n > width {
			width = n
		}
	}
	for _, f := range findings {
		mark, tint := "✓", ansiGreen
		switch f.level {
		case levelWarn:
			mark, tint = "!", ansiYellow
			warnings++
		case levelFail:
			mark, tint = "✗", ansiRed
			problems++
		}
		fmt.Fprintf(w, "%s %-*s  %s\n", paint(mark, tint, colour), width, f.name, f.detail)
	}
	return problems, warnings
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func tildeify(home, path string) string {
	if home != "" && strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}

func sortedNames(specs map[string]tunnel.Spec) []string {
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the machine, the daemon and the saved hosts",
		Long: "Runs the checks that explain most \"why doesn't it work\" moments:\n" +
			"the binary on PATH, ssh and lsof, the ~/.hop files, the control\n" +
			"socket, the daemon, saved tunnels' ports, zsh completion, and one\n" +
			"docker ps on each host a saved tunnel uses.\n\n" +
			"Exits 1 when something is broken. Warnings are advice.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			env := &doctorEnv{
				paths:       paths,
				home:        home,
				version:     Version,
				shell:       os.Getenv("SHELL"),
				lookPath:    exec.LookPath,
				executable:  os.Executable,
				probe:       sysprobe.New(),
				hostTimeout: hostProbeTimeout,
			}
			if offline, _ := cmd.Flags().GetBool("offline"); !offline {
				ex := sshexec.New(paths.SocketDir)
				defer ex.Close(context.Background())
				env.exec = ex
			}
			return reportFindings(cmd.OutOrStdout(), diagnose(cmd.Context(), env), useColour(cmd))
		},
	}
	cmd.Flags().Bool("offline", false, "skip the docker ps probe of saved hosts")
	return cmd
}
