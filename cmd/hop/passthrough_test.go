package main

import (
	"reflect"
	"testing"
)

func TestRunArgv(t *testing.T) {
	got, err := sshArgvFor("run", []string{"example-backend-dev", "docker", "ps"})
	if err != nil {
		t.Fatalf("sshArgvFor: %v", err)
	}
	want := []string{"ssh", "example-backend-dev", "docker", "ps"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestShellArgv(t *testing.T) {
	got, err := sshArgvFor("shell", []string{"example-backend-dev"})
	if err != nil {
		t.Fatalf("sshArgvFor: %v", err)
	}
	want := []string{"ssh", "example-backend-dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPushArgvPutsTheRemoteSecond(t *testing.T) {
	got, err := sshArgvFor("push", []string{"host", "./app.log", "/tmp/app.log"})
	if err != nil {
		t.Fatalf("sshArgvFor: %v", err)
	}
	want := []string{"scp", "./app.log", "host:/tmp/app.log"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPullArgvPutsTheRemoteFirst(t *testing.T) {
	got, err := sshArgvFor("pull", []string{"host", "/var/log/app.log", "./app.log"})
	if err != nil {
		t.Fatalf("sshArgvFor: %v", err)
	}
	want := []string{"scp", "host:/var/log/app.log", "./app.log"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The bash script accepted scp options before the host, and `vps push -r ...`
// is in its own documented examples. Dropping that would break real usage.
func TestPushForwardsLeadingScpOptions(t *testing.T) {
	got, err := sshArgvFor("push", []string{"-r", "-C", "host", "./dist", "/tmp/dist"})
	if err != nil {
		t.Fatalf("sshArgvFor: %v", err)
	}
	want := []string{"scp", "-r", "-C", "./dist", "host:/tmp/dist"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPullForwardsLeadingScpOptions(t *testing.T) {
	got, err := sshArgvFor("pull", []string{"-r", "host", "/tmp/dist", "./dist"})
	if err != nil {
		t.Fatalf("sshArgvFor: %v", err)
	}
	want := []string{"scp", "-r", "host:/tmp/dist", "./dist"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPassthroughArgumentCounts(t *testing.T) {
	tooFew := map[string][]string{
		"run":   {"host"},
		"shell": {},
		"push":  {"host", "only-one"},
		"pull":  {"host", "only-one"},
	}
	for name, args := range tooFew {
		if _, err := sshArgvFor(name, args); err == nil {
			t.Fatalf("%s accepted %d arguments", name, len(args))
		}
	}
}

func TestShellRejectsExtraArguments(t *testing.T) {
	if _, err := sshArgvFor("shell", []string{"host", "extra"}); err == nil {
		t.Fatal("shell accepted a second argument")
	}
}
