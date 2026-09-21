package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/hopfs"
	"github.com/thailoc-dev/hop/internal/sshexec"
	"github.com/thailoc-dev/hop/internal/store"
	"github.com/thailoc-dev/hop/internal/sysprobe"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// healthyEnv is a doctorEnv in which every check passes: tools on PATH, the
// running binary is the one on PATH, no daemon, nothing saved.
func healthyEnv(t *testing.T) (*doctorEnv, hopfs.Paths) {
	t.Helper()
	home, p := tempHome(t)
	self := filepath.Join(home, "bin", "hop")
	if err := os.MkdirAll(filepath.Dir(self), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(self, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSSHConfig(t, home, "Host example-backend-dev\nHost example-tracker-dev\nHost *\n")
	t.Setenv("SHELL", "/bin/zsh")
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(`eval "$(hop completion zsh)"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	onPath := map[string]string{"hop": self, "ssh": "/usr/bin/ssh", "lsof": "/usr/sbin/lsof"}
	return &doctorEnv{
		paths:       p,
		home:        home,
		version:     "test",
		shell:       "/bin/zsh",
		lookPath:    func(name string) (string, error) { return lookIn(onPath, name) },
		executable:  func() (string, error) { return self, nil },
		probe:       sysprobe.NewFake(),
		exec:        sshexec.NewFake(),
		hostTimeout: time.Second,
	}, p
}

func lookIn(table map[string]string, name string) (string, error) {
	if path, ok := table[name]; ok {
		return path, nil
	}
	return "", errors.New("not found")
}

func byName(findings []finding) map[string]finding {
	out := map[string]finding{}
	for _, f := range findings {
		out[f.name] = f
	}
	return out
}

func TestDoctorPassesInAHealthyEnvironment(t *testing.T) {
	env, _ := healthyEnv(t)

	findings := diagnose(context.Background(), env)

	for _, f := range findings {
		if f.level != levelOK {
			t.Errorf("%s: %v %q", f.name, f.level, f.detail)
		}
	}
	got := byName(findings)
	if !strings.Contains(got["ssh"].detail, "2 hosts") {
		t.Errorf("ssh detail = %q", got["ssh"].detail)
	}
	if !strings.Contains(got["daemon"].detail, "not running") {
		t.Errorf("daemon detail = %q", got["daemon"].detail)
	}
}

func TestDoctorFailsWhenSSHIsMissing(t *testing.T) {
	env, _ := healthyEnv(t)
	env.lookPath = func(name string) (string, error) {
		if name == "ssh" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}

	got := byName(diagnose(context.Background(), env))

	if got["ssh"].level != levelFail {
		t.Fatalf("ssh = %+v", got["ssh"])
	}
}

func TestDoctorWarnsWhenLsofIsMissing(t *testing.T) {
	env, _ := healthyEnv(t)
	env.lookPath = func(name string) (string, error) {
		if name == "lsof" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}

	got := byName(diagnose(context.Background(), env))

	if got["lsof"].level != levelWarn {
		t.Fatalf("lsof = %+v", got["lsof"])
	}
}

// The trap this suite fell into three times: a rebuilt binary that is not
// the one PATH resolves.
func TestDoctorWarnsWhenPathResolvesAnotherHop(t *testing.T) {
	env, _ := healthyEnv(t)
	other := filepath.Join(env.home, "other-hop")
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.lookPath = func(name string) (string, error) {
		if name == "hop" {
			return other, nil
		}
		return "/usr/bin/" + name, nil
	}

	got := byName(diagnose(context.Background(), env))

	if got["hop"].level != levelWarn || !strings.Contains(got["hop"].detail, other) {
		t.Fatalf("hop = %+v", got["hop"])
	}
}

func TestDoctorWarnsWhenHopIsNotOnPath(t *testing.T) {
	env, _ := healthyEnv(t)
	env.lookPath = func(name string) (string, error) {
		if name == "hop" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}

	got := byName(diagnose(context.Background(), env))

	if got["hop"].level != levelWarn || !strings.Contains(got["hop"].detail, "not on PATH") {
		t.Fatalf("hop = %+v", got["hop"])
	}
}

// The store sets a file it cannot parse aside as *.corrupt and carries on
// with an empty one, silently. Doctor is where that becomes visible.
func TestDoctorWarnsAboutAFileSetAsideAsCorrupt(t *testing.T) {
	env, p := healthyEnv(t)
	if err := os.WriteFile(p.CatalogueFile, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := byName(diagnose(context.Background(), env))

	if got["~/.hop"].level != levelWarn || !strings.Contains(got["~/.hop"].detail, "tunnels.json.corrupt") {
		t.Fatalf("~/.hop = %+v", got["~/.hop"])
	}
}

func TestDoctorReportsTheSocketBudget(t *testing.T) {
	env, p := healthyEnv(t)

	got := byName(diagnose(context.Background(), env))

	if got["socket"].level != levelOK || !strings.Contains(got["socket"].detail, p.ControlSock) {
		t.Fatalf("socket = %+v", got["socket"])
	}
}

func TestDoctorCountsTheDaemonsTunnels(t *testing.T) {
	env, p := healthyEnv(t)
	redis := redisSpec()
	degraded := tunnel.Status{Spec: tunnel.Spec{Host: "h", Container: "c", RemotePort: 1, LocalPort: 2},
		State: tunnel.StateDegraded, Since: time.Now(), CreatedAt: time.Now()}
	serveFakeDaemon(t, p, control.Response{OK: true, Statuses: []tunnel.Status{healthy(redis), degraded}})

	got := byName(diagnose(context.Background(), env))

	if got["daemon"].level != levelOK {
		t.Fatalf("daemon = %+v", got["daemon"])
	}
	if !strings.Contains(got["daemon"].detail, "1 healthy") || !strings.Contains(got["daemon"].detail, "1 degraded") {
		t.Fatalf("daemon detail = %q", got["daemon"].detail)
	}
}

func TestDoctorFlagsASocketNobodyAnswers(t *testing.T) {
	env, p := healthyEnv(t)
	// A socket file left behind by a daemon that died without cleaning up.
	if err := os.WriteFile(p.ControlSock, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got := byName(diagnose(context.Background(), env))

	if got["daemon"].level != levelWarn || !strings.Contains(got["daemon"].detail, "stale") {
		t.Fatalf("daemon = %+v", got["daemon"])
	}
}

func TestDoctorWarnsWhenASavedPortIsHeldBySomethingElse(t *testing.T) {
	env, p := healthyEnv(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	probe := sysprobe.NewFake()
	probe.SetPortBusy(46379, sysprobe.Holder{Command: "redis-server", PID: 4242})
	env.probe = probe

	got := byName(diagnose(context.Background(), env))

	if got["ports"].level != levelWarn {
		t.Fatalf("ports = %+v", got["ports"])
	}
	for _, want := range []string{"redis-stg", "46379", "redis-server", "4242"} {
		if !strings.Contains(got["ports"].detail, want) {
			t.Errorf("ports detail %q lacks %q", got["ports"].detail, want)
		}
	}
}

func TestDoctorIgnoresPortsHeldByRunningTunnels(t *testing.T) {
	env, p := healthyEnv(t)
	redis := redisSpec()
	saveNamed(t, p, "redis-stg", redis)
	serveFakeDaemon(t, p, control.Response{OK: true, Statuses: []tunnel.Status{healthy(redis)}})
	probe := sysprobe.NewFake()
	probe.SetPortBusy(46379, sysprobe.Holder{Command: "ssh", PID: 99})
	env.probe = probe

	got := byName(diagnose(context.Background(), env))

	if got["ports"].level != levelOK {
		t.Fatalf("ports = %+v", got["ports"])
	}
}

func TestDoctorWarnsWhenZshrcLacksCompletion(t *testing.T) {
	env, _ := healthyEnv(t)
	if err := os.WriteFile(filepath.Join(env.home, ".zshrc"), []byte("# nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := byName(diagnose(context.Background(), env))

	if got["completion"].level != levelWarn || !strings.Contains(got["completion"].detail, "hop completion zsh") {
		t.Fatalf("completion = %+v", got["completion"])
	}
}

func TestDoctorSkipsCompletionForOtherShells(t *testing.T) {
	env, _ := healthyEnv(t)
	env.shell = "/bin/bash"

	got := byName(diagnose(context.Background(), env))

	if _, present := got["completion"]; present {
		t.Fatalf("completion should not be checked for bash: %+v", got["completion"])
	}
}

func TestDoctorProbesEachSavedHostOnce(t *testing.T) {
	env, p := healthyEnv(t)
	redis := redisSpec()
	mongo := redisSpec()
	mongo.Container, mongo.LocalPort = "tracker_mongo_staging", 47017
	if err := store.SaveCatalogue(p.CatalogueFile, store.Catalogue{
		Tunnels: map[string]tunnel.Spec{"redis-stg": redis, "mongo-stg": mongo},
	}); err != nil {
		t.Fatal(err)
	}
	fake := sshexec.NewFake()
	fake.SetRunResult(sshexec.Result{Stdout: "tracker_redis_staging\t6379/tcp\ntracker_mongo_staging\t27017/tcp\n"}, nil)
	env.exec = fake

	findings := diagnose(context.Background(), env)

	var hosts []finding
	for _, f := range findings {
		if f.kind == kindHost {
			hosts = append(hosts, f)
		}
	}
	if len(hosts) != 1 || hosts[0].name != "example-tracker-dev" {
		t.Fatalf("host findings = %+v", hosts)
	}
	if hosts[0].level != levelOK || !strings.Contains(hosts[0].detail, "2 containers") {
		t.Fatalf("host = %+v", hosts[0])
	}
}

func TestDoctorFailsAHostThatDoesNotAnswer(t *testing.T) {
	env, p := healthyEnv(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	fake := sshexec.NewFake()
	fake.SetRunResult(sshexec.Result{}, errors.New("ssh: connect to host example-tracker-dev port 22: Operation timed out"))
	env.exec = fake

	got := byName(diagnose(context.Background(), env))

	if got["example-tracker-dev"].level != levelFail || !strings.Contains(got["example-tracker-dev"].detail, "timed out") {
		t.Fatalf("host = %+v", got["example-tracker-dev"])
	}
}

func TestDoctorOfflineSkipsHosts(t *testing.T) {
	env, p := healthyEnv(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	env.exec = nil

	got := byName(diagnose(context.Background(), env))

	if _, present := got["example-tracker-dev"]; present {
		t.Fatalf("offline doctor still probed a host: %+v", got["example-tracker-dev"])
	}
}

func TestRenderFindingsSummarisesAndExitsOne(t *testing.T) {
	findings := []finding{
		{name: "hop", level: levelOK, detail: "v1"},
		{name: "ssh", level: levelFail, detail: "not found"},
		{name: "lsof", level: levelWarn, detail: "missing"},
	}
	var out bytes.Buffer

	err := reportFindings(&out, findings, false)

	text := out.String()
	for _, want := range []string{"✓ hop", "✗ ssh", "! lsof", "1 problem, 1 warning"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	var ce codedError
	if !errors.As(err, &ce) || ce.code != exitFatal {
		t.Fatalf("err = %v, want exit %d", err, exitFatal)
	}
}

func TestRenderFindingsAllGoodReturnsNil(t *testing.T) {
	var out bytes.Buffer

	err := reportFindings(&out, []finding{{name: "hop", level: levelOK, detail: "v1"}}, false)

	if err != nil || !strings.Contains(out.String(), "all good") {
		t.Fatalf("err = %v, out = %q", err, out.String())
	}
}

func TestRenderFindingsWarningsOnlyReturnNil(t *testing.T) {
	var out bytes.Buffer

	err := reportFindings(&out, []finding{{name: "lsof", level: levelWarn, detail: "missing"}}, false)

	if err != nil || !strings.Contains(out.String(), "1 warning") {
		t.Fatalf("err = %v, out = %q", err, out.String())
	}
}
