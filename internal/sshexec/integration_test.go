package sshexec

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

// requireVPS skips unless a real host is configured.
func requireVPS(t *testing.T) (host, container string, remotePort int) {
	t.Helper()
	host = os.Getenv("HOP_TEST_HOST")
	container = os.Getenv("HOP_TEST_CONTAINER")
	portText := os.Getenv("HOP_TEST_REMOTE_PORT")
	if host == "" || container == "" || portText == "" {
		t.Skip("set HOP_TEST_HOST, HOP_TEST_CONTAINER and HOP_TEST_REMOTE_PORT to run integration tests")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("HOP_TEST_REMOTE_PORT: %v", err)
	}
	return host, container, port
}

func TestIntegrationContainerIPResolves(t *testing.T) {
	host, container, _ := requireVPS(t)
	e := New(t.TempDir())
	defer e.Close(context.Background())

	ip, err := e.ContainerIP(context.Background(), host, container)
	if err != nil {
		t.Fatalf("ContainerIP: %v", err)
	}
	if net.ParseIP(ip) == nil {
		t.Fatalf("ContainerIP returned %q, which is not an IP", ip)
	}
}

func TestIntegrationForwardAcceptsAConnection(t *testing.T) {
	host, container, remotePort := requireVPS(t)
	e := New(t.TempDir())
	defer e.Close(context.Background())

	ip, err := e.ContainerIP(context.Background(), host, container)
	if err != nil {
		t.Fatalf("ContainerIP: %v", err)
	}

	const localPort = 45917 // unlikely to be in use
	proc, err := e.StartForward(context.Background(), ForwardSpec{
		Host: host, RemoteAddr: ip, RemotePort: remotePort, LocalPort: localPort,
	})
	if err != nil {
		t.Fatalf("StartForward: %v", err)
	}
	defer func() { _ = proc.Terminate() }()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(localPort), time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("forward never accepted a connection; stderr: %s", proc.Stderr())
}
