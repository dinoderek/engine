package hostops

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// wsPeer is the server end of a WebSocket on a pipe: it answers the upgrade
// and then exchanges frames, unmasked as a server sends them.
type wsPeer struct {
	conn net.Conn
	r    *bufio.Reader
}

// newWSPair dials a client over a pipe. accept, when not nil, replaces the
// server's Sec-WebSocket-Accept, and status its status line.
func newWSPair(t *testing.T, status, accept string) (*wsConn, *wsPeer, error) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	peer := &wsPeer{conn: server, r: bufio.NewReader(server)}
	done := make(chan error, 1)
	go func() {
		req, err := http.ReadRequest(peer.r)
		if err != nil {
			done <- err
			return
		}
		if req.URL.Path != "/rpc" || req.Header.Get("Upgrade") != "websocket" {
			done <- fmt.Errorf("unexpected upgrade request %s %v", req.URL, req.Header)
			return
		}
		sum := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + wsGUID))
		if accept == "" {
			accept = base64.StdEncoding.EncodeToString(sum[:])
		}
		_, err = io.WriteString(server, "HTTP/1.1 "+status+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+accept+"\r\n\r\n")
		done <- err
	}()
	c, err := wsDial(client, client, "/rpc")
	if serr := <-done; serr != nil {
		t.Fatal(serr)
	}
	return c, peer, err
}

// send writes one unmasked frame.
func (p *wsPeer) send(t *testing.T, fin bool, op byte, payload []byte) {
	t.Helper()
	head := []byte{op}
	if fin {
		head[0] |= 0x80
	}
	switch n := len(payload); {
	case n < 126:
		head = append(head, byte(n))
	case n <= 0xffff:
		head = binary.BigEndian.AppendUint16(append(head, 126), uint16(n))
	default:
		head = binary.BigEndian.AppendUint64(append(head, 127), uint64(n))
	}
	if _, err := p.conn.Write(append(head, payload...)); err != nil {
		t.Error(err)
	}
}

// recv reads one client frame, which must be masked.
func (p *wsPeer) recv() (byte, []byte, error) {
	h, err := p.r.Peek(2)
	if err != nil {
		return 0, nil, err
	}
	if h[1]&0x80 == 0 {
		return 0, nil, errors.New("client frame is not masked")
	}
	_, op, payload, err := (&wsConn{r: p.r}).readFrame()
	return op, payload, err
}

func TestWSDialChecksTheUpgrade(t *testing.T) {
	if _, _, err := newWSPair(t, "101 Switching Protocols", ""); err != nil {
		t.Fatalf("a correct upgrade failed: %v", err)
	}
	for name, tt := range map[string]struct{ status, accept string }{
		"wrong status": {"200 OK", ""},
		"wrong accept": {"101 Switching Protocols", "bm9wZQ=="},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := newWSPair(t, tt.status, tt.accept)
			if err == nil || !strings.Contains(err.Error(), "refused") {
				t.Errorf("err = %v, want a refused upgrade", err)
			}
		})
	}
}

func TestWSDialFailsOnAnUnreadableReply(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	go func() {
		_, _ = http.ReadRequest(bufio.NewReader(server))
		_, _ = io.WriteString(server, "garbage\r\n\r\n")
		_ = server.Close()
	}()
	if _, err := wsDial(client, client, "/rpc"); err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("err = %v, want an upgrade error", err)
	}
}

func TestWSWritesMaskedFramesOfEveryLength(t *testing.T) {
	c, peer, err := newWSPair(t, "101 Switching Protocols", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 125, 126, 70000} {
		want := bytes.Repeat([]byte("x"), n)
		errc := make(chan error, 1)
		go func() { errc <- c.WriteText(want) }()
		op, got, err := peer.recv()
		if err != nil || op != 0x1 || !bytes.Equal(got, want) {
			t.Errorf("%d bytes: op %x, %d bytes back, err %v", n, op, len(got), err)
		}
		if err := <-errc; err != nil {
			t.Error(err)
		}
	}
}

func TestWSReadJoinsFragmentsAnswersPingsAndEndsOnClose(t *testing.T) {
	c, peer, err := newWSPair(t, "101 Switching Protocols", "")
	if err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte("y"), 300)
	go func() {
		peer.send(t, false, 0x1, []byte("hel"))
		peer.send(t, true, 0x9, []byte("ping!"))
		if op, p, err := peer.recv(); err != nil || op != 0xA || string(p) != "ping!" {
			t.Errorf("pong: op %x %q %v", op, p, err)
		}
		peer.send(t, true, 0xA, nil)
		peer.send(t, true, 0x0, []byte("lo"))
		peer.send(t, true, 0x2, big)
		peer.send(t, true, 0x8, nil)
	}()
	for _, want := range [][]byte{[]byte("hello"), big} {
		got, err := c.ReadMessage()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("message %.20q, err %v; want %.20q", got, err, want)
		}
	}
	if _, err := c.ReadMessage(); !errors.Is(err, io.EOF) {
		t.Errorf("after close err = %v, want EOF", err)
	}
}

func TestWSRefusesAnOversizedFrame(t *testing.T) {
	head := binary.BigEndian.AppendUint64([]byte{0x81, 127}, wsMaxFrame+1)
	c := &wsConn{r: bufio.NewReader(bytes.NewReader(head))}
	if _, err := c.ReadMessage(); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("err = %v, want too large", err)
	}
}

func TestWSReadFailsOnATruncatedFrame(t *testing.T) {
	for name, data := range map[string][]byte{
		"no header":      {0x81},
		"short length16": {0x81, 126, 0},
		"short length64": {0x81, 127, 0, 0},
		"short mask":     {0x81, 0x82, 1, 2},
		"short payload":  {0x81, 5, 'a'},
	} {
		c := &wsConn{r: bufio.NewReader(bytes.NewReader(data))}
		if _, err := c.ReadMessage(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestWSReadsAMaskedFrame(t *testing.T) {
	mask := []byte{1, 2, 3, 4}
	payload := []byte("hi")
	frame := []byte{0x81, 0x80 | 2}
	frame = append(frame, mask...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	c := &wsConn{r: bufio.NewReader(bytes.NewReader(frame))}
	if got, err := c.ReadMessage(); err != nil || string(got) != "hi" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestCodexDaemonRunsTheProxyInTheHome(t *testing.T) {
	dir := t.TempDir()
	bin := dir + "/codex"
	// The fake proxy records its arguments and home, then answers the upgrade
	// with garbage.
	write(t, bin, "#!/bin/sh\necho \"$@ $CODEX_HOME\" > \"$0.args\"\nprintf 'nope\\r\\n\\r\\n'\n", 0o755)
	err := codexDaemon(context.Background(), bin, "/the/home", func(*wsConn) error { return nil })
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
	err := codexDaemon(ctx, bin, "/h", func(*wsConn) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline", err)
	}
}
