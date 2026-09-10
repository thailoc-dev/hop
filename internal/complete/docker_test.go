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

// Regression: the exact `docker ps` output from example-webapp-dev, which was
// reported as completing nothing. The parser handles it; the report was a cold
// cache. Kept so a future parser change cannot quietly break this shape --
// note the IPv6 [::] forms and the multi-port nginx line.
func TestParsePSHandlesKegelWebappOutput(t *testing.T) {
	stdout := "webapp_nextjs_staging\t3000/tcp\n" +
		"webapp_nginx_staging\t0.0.0.0:80->80/tcp, [::]:80->80/tcp, " +
		"0.0.0.0:443->443/tcp, [::]:443->443/tcp\n"

	got := ParsePS(stdout)

	if len(got) != 2 {
		t.Fatalf("got %d containers, want 2: %+v", len(got), got)
	}
	if got[0].Name != "webapp_nextjs_staging" ||
		!reflect.DeepEqual(got[0].Ports, []int{3000}) {
		t.Fatalf("nextjs = %+v", got[0])
	}
	// Both published ports, deduplicated across their IPv4 and IPv6 entries.
	if got[1].Name != "webapp_nginx_staging" ||
		!reflect.DeepEqual(got[1].Ports, []int{80, 443}) {
		t.Fatalf("nginx = %+v, want ports [80 443] deduplicated across [::] entries", got[1])
	}
}

func TestContainerPortsDeduplicatesIPv4AndIPv6Entries(t *testing.T) {
	// Docker lists each published port twice, once per address family. A
	// completion offering "80 80 443 443" would be a bug.
	got := ContainerPorts("0.0.0.0:80->80/tcp, [::]:80->80/tcp, 0.0.0.0:443->443/tcp, [::]:443->443/tcp")

	if !reflect.DeepEqual(got, []int{80, 443}) {
		t.Fatalf("got %v, want [80 443]", got)
	}
}

// Docker prints the IPv6 mapping two different ways depending on version:
// "[::]:80->80/tcp" on some hosts and ":::80->80/tcp" on others. Both appear
// across this fleet, so both are pinned here.
func TestContainerPortsHandlesBothIPv6Spellings(t *testing.T) {
	bracketed := ContainerPorts("0.0.0.0:80->80/tcp, [::]:80->80/tcp")
	bare := ContainerPorts("0.0.0.0:80->80/tcp, :::80->80/tcp")

	if !reflect.DeepEqual(bracketed, []int{80}) {
		t.Fatalf("bracketed form = %v, want [80]", bracketed)
	}
	if !reflect.DeepEqual(bare, []int{80}) {
		t.Fatalf("bare form = %v, want [80]", bare)
	}
}

// Regression: the exact `docker ps` output from example-filter-dev, reported as
// completing nothing. The cause was a stale installed binary, not the parser --
// this pins the shape so it stays that way.
func TestParsePSHandlesSpamDetectorOutput(t *testing.T) {
	stdout := "filter_nginx_staging\t0.0.0.0:80->80/tcp, :::80->80/tcp\n" +
		"filter_node_staging\t3000/tcp\n" +
		"filter_gateway_staging\t9000/tcp\n" +
		"filter_lookup_staging\t8080/tcp\n" +
		"filter_mongo_staging\t27017/tcp\n" +
		"filter_redis_staging\t6379/tcp\n"

	got := ParsePS(stdout)

	if len(got) != 6 {
		t.Fatalf("got %d containers, want 6: %+v", len(got), got)
	}
	want := map[string]int{
		"filter_nginx_staging":   80,
		"filter_node_staging":    3000,
		"filter_gateway_staging": 9000,
		"filter_lookup_staging":  8080,
		"filter_mongo_staging":   27017,
		"filter_redis_staging":   6379,
	}
	for _, container := range got {
		wantPort, known := want[container.Name]
		if !known {
			t.Fatalf("unexpected container %q", container.Name)
		}
		if len(container.Ports) != 1 || container.Ports[0] != wantPort {
			t.Fatalf("%s ports = %v, want [%d]", container.Name, container.Ports, wantPort)
		}
	}
}
