package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// wsReadLimit bounds one message from the daemon, so a bad length cannot
// exhaust memory. A project/list page can exceed the library's default.
const wsReadLimit = 16 << 20

// dialDaemon upgrades a byte stream, the daemon's control socket that `codex
// app-server proxy` relays on stdio, to a WebSocket at /rpc. The library
// answers the daemon's pings while a read waits. Closing the connection
// closes r and w.
func dialDaemon(ctx context.Context, r io.ReadCloser, w io.WriteCloser) (*websocket.Conn, error) {
	stream := &streamConn{r: r, w: w}
	var dialed atomic.Bool
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			if !dialed.CompareAndSwap(false, true) {
				return nil, errors.New("the daemon's stream is already in use")
			}
			return stream, nil
		},
	}}
	c, resp, err := websocket.Dial(ctx, "ws://localhost/rpc", &websocket.DialOptions{HTTPClient: client})
	if resp != nil && resp.Body != nil && err != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("websocket upgrade: %w", err)
	}
	c.SetReadLimit(wsReadLimit)
	return c, nil
}

// streamConn is a net.Conn over a reader and a writer, which carry no
// addresses or deadlines: the caller's context bounds every exchange.
type streamConn struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (s *streamConn) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *streamConn) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *streamConn) Close() error                { return errors.Join(s.w.Close(), s.r.Close()) }
func (s *streamConn) LocalAddr() net.Addr         { return streamAddr{} }
func (s *streamConn) RemoteAddr() net.Addr        { return streamAddr{} }
func (s *streamConn) SetDeadline(time.Time) error { return nil }

func (s *streamConn) SetReadDeadline(time.Time) error  { return nil }
func (s *streamConn) SetWriteDeadline(time.Time) error { return nil }

type streamAddr struct{}

func (streamAddr) Network() string { return "stdio" }
func (streamAddr) String() string  { return "codex app-server proxy" }

// codexDaemon runs exchange over a WebSocket to the daemon serving home,
// through `codex app-server proxy`. The process and its pipes close when
// exchange returns or ctx ends.
func codexDaemon(ctx context.Context, bin, home string, exchange func(context.Context, *websocket.Conn) error) error {
	cmd := exec.CommandContext(ctx, bin, "app-server", "proxy")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	// A read the library is not watching, such as the upgrade reply, ends
	// when the pipe closes, even if a child of the proxy holds it open.
	stop := context.AfterFunc(ctx, func() { _ = out.Close() })
	defer stop()
	conn, err := dialDaemon(ctx, out, in)
	if err == nil {
		err = exchange(ctx, conn)
		_ = conn.CloseNow()
	}
	if err != nil && ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	return err
}
