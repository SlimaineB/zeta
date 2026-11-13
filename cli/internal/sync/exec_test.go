package sync

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/websocket"
)

func testWSURL(srv *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

func TestRunExecStreamsOutputAndExitCode(t *testing.T) {
	srv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		_ = websocket.Message.Send(ws, []byte("ohello\n"))
		_ = websocket.Message.Send(ws, []byte("oworld\n"))
		_ = websocket.Message.Send(ws, append([]byte("e"), []byte(`{"code":3}`)...))
	}))
	defer srv.Close()

	conn, err := DialAuth(context.Background(), testWSURL(srv, "/exec"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var out bytes.Buffer
	code, err := RunExec(context.Background(), conn, nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
	if out.String() != "hello\nworld\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunExecReturnsRemoteError(t *testing.T) {
	srv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		_ = websocket.Message.Send(ws, append([]byte("e"), []byte(`{"code":1,"error":"boom"}`)...))
	}))
	defer srv.Close()

	conn, err := DialAuth(context.Background(), testWSURL(srv, "/exec"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	code, err := RunExec(context.Background(), conn, nil, &bytes.Buffer{})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if err == nil || err.Error() != "boom" {
		t.Fatalf("error = %v", err)
	}
}

func TestDialTunnelReadyAndError(t *testing.T) {
	ready := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		_ = websocket.Message.Send(ws, []byte{'o'})
	}))
	defer ready.Close()
	conn, err := DialTunnel(context.Background(), testWSURL(ready, "/pf"), "")
	if err != nil {
		t.Fatalf("ready tunnel: %v", err)
	}
	_ = conn.Close()

	failing := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		_ = websocket.Message.Send(ws, append([]byte("e"), []byte(`{"error":"no pod"}`)...))
	}))
	defer failing.Close()
	if _, err := DialTunnel(context.Background(), testWSURL(failing, "/pf"), ""); err == nil || !strings.Contains(err.Error(), "no pod") {
		t.Fatalf("error tunnel = %v", err)
	}
}
