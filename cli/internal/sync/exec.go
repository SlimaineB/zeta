package sync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"golang.org/x/net/websocket"
	"golang.org/x/term"
)

func (c *WSConn) SendBytes(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return websocket.Message.Send(c.ws, data)
}

func (c *WSConn) ReceiveBytes(data *[]byte) error {
	return websocket.Message.Receive(c.ws, data)
}

func DialExec(ctx context.Context, rawURL, cookie string) (*WSConn, error) {
	return DialAuth(ctx, rawURL, cookie, "")
}

type resizeMessage struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// RunShell attaches the local terminal to the remote PTY. It puts the local
// terminal in raw mode, forwards keystrokes, terminal resizes and prints the
// remote output. It returns when the remote shell exits or the context is done.
func RunShell(ctx context.Context, conn *WSConn, in, out *os.File) error {
	var restore func()
	if term.IsTerminal(int(in.Fd())) {
		if state, err := term.MakeRaw(int(in.Fd())); err == nil {
			restore = func() { _ = term.Restore(int(in.Fd()), state) }
		}
	}
	if restore != nil {
		defer restore()
	}

	sendSize := func() {
		width, height, err := term.GetSize(int(out.Fd()))
		if err != nil {
			return
		}
		payload, err := json.Marshal(resizeMessage{Cols: uint16(width), Rows: uint16(height)})
		if err != nil {
			return
		}
		_ = conn.SendBytes(append([]byte{'r'}, payload...))
	}
	sendSize()

	go watchResize(ctx, sendSize)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				frame := make([]byte, n+1)
				frame[0] = 'i'
				copy(frame[1:], buf[:n])
				if sendErr := conn.SendBytes(frame); sendErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		var frame []byte
		if err := conn.ReceiveBytes(&frame); err != nil {
			if err == io.EOF {
				return nil
			}
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if len(frame) == 0 || frame[0] != 'o' {
			continue
		}
		if _, err := out.Write(frame[1:]); err != nil {
			return err
		}
	}
}

// RunExec runs a non-interactive command, forwarding optional stdin and
// streaming stdout/stderr. It returns the remote exit code.
func RunExec(ctx context.Context, conn *WSConn, in io.Reader, out io.Writer) (int, error) {
	if in != nil {
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := in.Read(buf)
				if n > 0 {
					frame := make([]byte, n+1)
					frame[0] = 'i'
					copy(frame[1:], buf[:n])
					if conn.SendBytes(frame) != nil {
						return
					}
				}
				if err != nil {
					_ = conn.SendBytes([]byte{'c'})
					return
				}
			}
		}()
	}
	code := 0
	var runErr error
	for {
		var frame []byte
		if err := conn.ReceiveBytes(&frame); err != nil {
			if err == io.EOF || ctx.Err() != nil {
				break
			}
			return code, err
		}
		if len(frame) == 0 {
			continue
		}
		switch frame[0] {
		case 'o':
			if _, err := out.Write(frame[1:]); err != nil {
				return code, err
			}
		case 'e':
			var exit struct {
				Code  int    `json:"code"`
				Error string `json:"error"`
			}
			if json.Unmarshal(frame[1:], &exit) == nil {
				code = exit.Code
				if exit.Error != "" {
					runErr = errors.New(exit.Error)
				}
			}
		}
	}
	return code, runErr
}
