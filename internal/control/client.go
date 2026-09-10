package control

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// ErrNoDaemon means nothing is listening on the control socket. The CLI
// treats it as "spawn one", which is why a stale socket file must produce it
// too rather than some other connection error.
var ErrNoDaemon = errors.New("no hop daemon is listening")

const dialTimeout = 2 * time.Second

// Client is a single-request connection to the supervisor.
type Client struct {
	conn net.Conn
}

func Dial(socketPath string) (*Client, error) {
	conn, err := net.DialTimeout("unix", socketPath, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoDaemon, err)
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Send writes one request and reads one response.
func (c *Client) Send(req Request) (Response, error) {
	if err := c.conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return Response{}, fmt.Errorf("set deadline: %w", err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("encode request: %w", err)
	}
	if _, err := c.conn.Write(append(data, '\n')); err != nil {
		return Response{}, fmt.Errorf("write request: %w", err)
	}

	var resp Response
	if err := json.NewDecoder(bufio.NewReader(c.conn)).Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}
