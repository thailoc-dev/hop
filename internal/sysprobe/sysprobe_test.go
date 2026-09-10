package sysprobe

import (
	"net"
	"testing"
)

func TestPortFreeIsTrueForAnUnusedPort(t *testing.T) {
	// Ask the kernel for a free port, then release it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	if !New().PortFree(port) {
		t.Fatalf("port %d reported busy immediately after being released", port)
	}
}

func TestPortFreeIsFalseWhileSomethingListens(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port

	if New().PortFree(port) {
		t.Fatalf("port %d reported free while this test holds it", port)
	}
}

func TestPortHolderFindsThisProcess(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port

	holder, err := New().PortHolder(port)
	if err != nil {
		t.Skipf("lsof unavailable: %v", err)
	}
	if holder.PID == 0 {
		t.Fatal("PortHolder returned no PID")
	}
	if holder.Command == "" {
		t.Fatal("PortHolder returned no command name")
	}
}

func TestFakeReportsConfiguredBusyPorts(t *testing.T) {
	f := NewFake()
	f.SetPortBusy(27018, Holder{Command: "mongod", PID: 4242})

	if f.PortFree(27018) {
		t.Fatal("configured busy port reported free")
	}
	if !f.PortFree(27019) {
		t.Fatal("unconfigured port reported busy")
	}

	holder, err := f.PortHolder(27018)
	if err != nil {
		t.Fatalf("PortHolder: %v", err)
	}
	if holder.Command != "mongod" || holder.PID != 4242 {
		t.Fatalf("PortHolder = %+v", holder)
	}
}

func TestFakeDefaultRouteIsConfigurable(t *testing.T) {
	f := NewFake()
	f.SetDefaultRoute("192.168.1.1")

	got, err := f.DefaultRoute()
	if err != nil {
		t.Fatalf("DefaultRoute: %v", err)
	}
	if got != "192.168.1.1" {
		t.Fatalf("got %q", got)
	}
}
