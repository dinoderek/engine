package codex

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// pipeListener serves one connection, the server end of a pipe, and then
// blocks until it is closed.
type pipeListener struct {
	conns chan net.Conn
	once  sync.Once
	done  chan struct{}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return streamAddr{} }

// serveWS runs handler as an HTTP server on the server end of a pipe, and
// returns the client end.
func serveWS(t *testing.T, handler http.HandlerFunc) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	l := &pipeListener{conns: make(chan net.Conn, 1), done: make(chan struct{})}
	l.conns <- server
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: time.Minute}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close(); _ = client.Close(); _ = server.Close() })
	return client
}

// newWSPair dials a client over a pipe to a server that accepts the upgrade
// at /rpc, and returns both ends.
func newWSPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	pipe := serveWS(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rpc" {
			http.NotFound(w, r)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		accepted <- c
	})
	c, err := dialDaemon(context.Background(), pipe, pipe)
	if err != nil {
		t.Fatal(err)
	}
	peer := <-accepted
	t.Cleanup(func() { _ = c.CloseNow(); _ = peer.CloseNow() })
	return c, peer
}

func TestDialDaemonExchangesMessagesAtRPC(t *testing.T) {
	c, peer := newWSPair(t)
	ctx := context.Background()
	big := bytes.Repeat([]byte("y"), 1<<20) // past the library's default limit
	go func() {
		_, p, err := peer.Read(ctx)
		if err != nil || string(p) != "hello" {
			t.Errorf("server read %q, %v", p, err)
		}
		_ = peer.Write(ctx, websocket.MessageText, big)
	}()
	if err := c.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, got, err := c.Read(ctx); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("read %d bytes, %v; want %d", len(got), err, len(big))
	}
}

func TestDialDaemonFailsOnARefusedUpgrade(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"not an upgrade": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") },
		"not found":      http.NotFound,
	} {
		t.Run(name, func(t *testing.T) {
			pipe := serveWS(t, handler)
			if _, err := dialDaemon(context.Background(), pipe, pipe); err == nil || !strings.Contains(err.Error(), "websocket upgrade") {
				t.Errorf("err = %v, want a refused upgrade", err)
			}
		})
	}
}

func TestDialDaemonFailsOnAnUnreadableReply(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	go func() {
		_, _ = http.ReadRequest(bufio.NewReader(server))
		_, _ = io.WriteString(server, "garbage\r\n\r\n")
		_ = server.Close()
	}()
	if _, err := dialDaemon(context.Background(), client, client); err == nil || !strings.Contains(err.Error(), "websocket upgrade") {
		t.Errorf("err = %v, want an upgrade error", err)
	}
}

// The daemon pings while a reply is pending; an unanswered ping would let it
// drop the connection.
func TestRPCAnswersPingsWhileWaiting(t *testing.T) {
	c, peer := newWSPair(t)
	ctx := context.Background()
	go func() {
		if _, _, err := peer.Read(ctx); err != nil {
			t.Error(err)
			return
		}
		// The server must keep reading to see the pong.
		_ = peer.CloseRead(ctx)
		if err := peer.Ping(ctx); err != nil {
			t.Errorf("ping: %v", err)
		}
		_ = peer.Write(ctx, websocket.MessageText, []byte(`{"id":1,"result":{"ok":true}}`))
	}()
	got, err := (&rpcClient{ctx: ctx, c: c}).call("x", nil)
	if err != nil || string(got) != `{"ok":true}` {
		t.Errorf("got %s, %v", got, err)
	}
}

func TestRPCEndsWithItsContext(t *testing.T) {
	c, peer := newWSPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, _, _ = peer.Read(context.Background())
		cancel()
	}()
	if _, err := (&rpcClient{ctx: ctx, c: c}).call("thread/name/set", nil); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "thread/name/set") {
		t.Errorf("err = %v, want the method and the cancellation", err)
	}
}

func TestStreamConnClosesBothEnds(t *testing.T) {
	r, w := &closeRecorder{}, &closeRecorder{}
	s := &streamConn{r: r, w: w}
	if err := s.Close(); err != nil || !r.closed || !w.closed {
		t.Errorf("close: %v, reader %v, writer %v", err, r.closed, w.closed)
	}
	if s.SetDeadline(time.Time{}) != nil || s.SetReadDeadline(time.Time{}) != nil || s.SetWriteDeadline(time.Time{}) != nil {
		t.Error("deadlines are not no-ops")
	}
	if s.LocalAddr().Network() != "stdio" || s.RemoteAddr().String() != "codex app-server proxy" {
		t.Errorf("addresses %v %v", s.LocalAddr(), s.RemoteAddr())
	}
}

type closeRecorder struct {
	bytes.Buffer
	closed bool
}

func (c *closeRecorder) Close() error { c.closed = true; return nil }

func TestCodexDaemonRunsTheProxyInTheHome(t *testing.T) {
	dir := t.TempDir()
	bin := dir + "/codex"
	// The fake proxy records its arguments and home, reads the upgrade
	// request's first line, so the request is written before it exits, and
	// answers with garbage.
	write(t, bin, "#!/bin/sh\necho \"$@ $CODEX_HOME\" > \"$0.args\"\nread -r line\nprintf 'nope\\r\\n\\r\\n'\n", 0o755)
	err := codexDaemon(context.Background(), bin, "/the/home", func(context.Context, *websocket.Conn) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("err = %v, want an upgrade error", err)
	}
	if args, _ := os.ReadFile(bin + ".args"); string(args) != "app-server proxy /the/home\n" {
		t.Errorf("ran with %q", args)
	}
	if err := codexDaemon(context.Background(), dir+"/missing", "/h", nil); err == nil {
		t.Error("a missing binary did not fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := codexDaemon(ctx, bin, "/h", nil); err == nil {
		t.Error("an ended context did not fail")
	}
}

func TestCodexDaemonEndsAtTheDeadline(t *testing.T) {
	dir := t.TempDir()
	bin := dir + "/codex"
	// The proxy never answers, and its child keeps the pipe open after the
	// proxy is killed: only closing the pipe at the deadline ends the read.
	write(t, bin, "#!/bin/sh\nsleep 600 &\necho $! > \"$0.pid\"\nwait\n", 0o755)
	t.Cleanup(func() {
		if pid, err := os.ReadFile(bin + ".pid"); err == nil {
			_ = exec.Command("kill", strings.TrimSpace(string(pid))).Run()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := codexDaemon(ctx, bin, "/h", func(context.Context, *websocket.Conn) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline", err)
	}
}
