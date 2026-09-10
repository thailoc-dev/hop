package control

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
)

// Serve accepts connections until the listener is closed.
func Serve(l net.Listener, h Handler) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go serveConn(conn, h)
	}
}

func serveConn(conn net.Conn, h Handler) {
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}

	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{OK: false, Error: "malformed request: " + err.Error()})
		return
	}

	_ = json.NewEncoder(conn).Encode(h.Handle(req))
}
