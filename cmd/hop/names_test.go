package main

import (
	"strings"
	"testing"
)

func TestValidateNameAccepts(t *testing.T) {
	for _, name := range []string{
		"redis-stg", "mongo_dev", "a", "x1", "1a", "pg-prod-replica",
		strings.Repeat("a", 40),
	} {
		if err := validateName(name); err != nil {
			t.Fatalf("validateName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateNameRejects(t *testing.T) {
	tests := []struct {
		name string
		want string // substring of the error
	}{
		{"", "empty"},
		{"Redis", "lower-case"},
		{"redis stg", "lower-case"},
		{"-redis", "start with"},
		{"redis.stg", "lower-case"},
		{strings.Repeat("a", 41), "40"},
		{"ls", "reserved"},
		{"down", "reserved"},
		{"up", "reserved"},
		{"save", "reserved"},
		{"forget", "reserved"},
		// The rule that keeps `hop down 46379` unambiguous.
		{"46379", "port"},
		{"0", "port"},
	}
	for _, tc := range tests {
		err := validateName(tc.name)
		if err == nil {
			t.Fatalf("validateName(%q) accepted", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("validateName(%q) = %q, want it to mention %q", tc.name, err, tc.want)
		}
	}
}

func TestIsPort(t *testing.T) {
	for arg, want := range map[string]bool{
		"46379": true, "1": true, "0": true,
		"redis-stg": false, "": false, "46379a": false, "-1": false,
	} {
		if got := isPort(arg); got != want {
			t.Fatalf("isPort(%q) = %v, want %v", arg, got, want)
		}
	}
}
