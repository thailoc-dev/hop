package main

import (
	"net"
	"testing"
)

func TestProbeLocalPortIsTrueWhenSomethingAccepts(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	port := l.Addr().(*net.TCPAddr).Port
	if !probeLocalPort(port) {
		t.Fatalf("probe failed against a listening port %d", port)
	}
}

func TestProbeLocalPortIsFalseWhenNothingListens(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	if probeLocalPort(port) {
		t.Fatalf("probe succeeded against closed port %d", port)
	}
}
