package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net"
)

// DialTunnel opens the port-forward websocket and waits for the portal's ready
// control frame so connection failures surface before the local socket is used.
func DialTunnel(ctx context.Context, rawURL, cookie string) (*WSConn, error) {
	conn, err := DialAuth(ctx, rawURL, cookie, "")
	if err != nil {
		return nil, err
	}
	var first []byte
	if err := conn.ReceiveBytes(&first); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if len(first) > 0 && first[0] == 'e' {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(first[1:], &payload)
		_ = conn.Close()
		if payload.Error == "" {
			payload.Error = "port forward failed"
		}
		return nil, errors.New(payload.Error)
	}
	return conn, nil
}

// Tunnel copies bytes in both directions between the websocket and the local
// TCP connection until one side closes or the context is cancelled.
func Tunnel(ctx context.Context, conn *WSConn, c net.Conn) error {
	errs := make(chan error, 2)
	go func() { errs <- copyLocalToRemote(conn, c) }()
	go func() { errs <- copyRemoteToLocal(conn, c) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errs:
		return err
	}
}

func copyLocalToRemote(conn *WSConn, c net.Conn) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			if sendErr := conn.SendBytes(buf[:n]); sendErr != nil {
				return sendErr
			}
		}
		if err != nil {
			return err
		}
	}
}

func copyRemoteToLocal(conn *WSConn, c net.Conn) error {
	for {
		var data []byte
		if err := conn.ReceiveBytes(&data); err != nil {
			return err
		}
		if len(data) == 0 {
			continue
		}
		if _, err := c.Write(data); err != nil {
			return err
		}
	}
}
