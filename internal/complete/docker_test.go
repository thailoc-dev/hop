package complete

import (
	"reflect"
	"testing"
)

func TestContainerPorts(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  []int
	}{
		{"unpublished", "27017/tcp", []int{27017}},
		{"published", "0.0.0.0:27018->27017/tcp, :::27018->27017/tcp", []int{27017}},
		{"several", "0.0.0.0:8080->80/tcp, 0.0.0.0:8443->443/tcp", []int{80, 443}},
		{"none", "", nil},
		{"udp", "0.0.0.0:5353->5353/udp", []int{5353}},
		{"mixed", "9000/tcp, 0.0.0.0:5432->5432/tcp", []int{5432, 9000}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContainerPorts(tc.field); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ContainerPorts(%q) = %v, want %v", tc.field, got, tc.want)
			}
		})
	}
}

func TestParsePS(t *testing.T) {
	stdout := "app_mongo_staging\t0.0.0.0:27018->27017/tcp\n" +
		"app_redis\t6379/tcp\n" +
		"app_api\t\n"

	got := ParsePS(stdout)

	if len(got) != 3 {
		t.Fatalf("got %d containers, want 3: %+v", len(got), got)
	}
	if got[0].Name != "app_mongo_staging" || !reflect.DeepEqual(got[0].Ports, []int{27017}) {
		t.Fatalf("first container = %+v", got[0])
	}
	if got[2].Name != "app_api" || len(got[2].Ports) != 0 {
		t.Fatalf("a container with no ports should still be offered: %+v", got[2])
	}
}

func TestParsePSIgnoresBlankAndMalformedLines(t *testing.T) {
	got := ParsePS("\n\nno-tab-here\napp_mongo\t27017/tcp\n\n")

	if len(got) != 1 || got[0].Name != "app_mongo" {
		t.Fatalf("got %+v, want just app_mongo", got)
	}
}
