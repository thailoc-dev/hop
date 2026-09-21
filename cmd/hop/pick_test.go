package main

import (
	"context"
	"slices"
	"testing"
)

func TestRealSourcesTunnelsMergeRunningAndSaved(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	saveNamed(t, p, "redis-stg", redisSpec())

	src := newRealSources(p)
	got := src.Tunnels()

	if len(got) != 1 || got[0].Spec.Name != "redis-stg" {
		t.Fatalf("tunnels = %+v", got)
	}
}

func TestRealSourcesHostsComeFromSSHConfig(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	writeSSHConfig(t, home, "Host example-backend-dev\nHost *\n")

	got := newRealSources(p).Hosts()

	if !slices.Contains(got, "example-backend-dev") || slices.Contains(got, "*") {
		t.Fatalf("hosts = %v", got)
	}
}

func TestRealSourcesContainersUseTheCache(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	seedCache(t, home, "example-backend-dev", "app_mongo_staging")

	got, err := newRealSources(p).Containers(context.Background(), "example-backend-dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "app_mongo_staging" {
		t.Fatalf("containers = %+v", got)
	}
}

func TestRealSourcesFreePortIsActuallyFree(t *testing.T) {
	_, p := tempHome(t)
	src := newRealSources(p)
	port, err := src.FreePort()
	if err != nil || port < 1024 {
		t.Fatalf("port=%d err=%v", port, err)
	}
	if !src.PortFree(port) {
		t.Fatalf("FreePort returned %d, which PortFree says is busy", port)
	}
}

func TestRealSourcesValidateNameIsHops(t *testing.T) {
	_, p := tempHome(t)
	if err := newRealSources(p).ValidateName("46379"); err == nil {
		t.Fatal("all-digit name accepted")
	}
}
