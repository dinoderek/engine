package codex

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
)

// wsConn is a client WebSocket over a byte stream: the Codex daemon's
// control socket, which `codex app-server proxy` relays on stdio. It sends
// masked text frames and reads whole messages, answering pings.
type wsConn struct {
	r *bufio.Reader
	w io.Writer
}

// wsGUID is the key suffix RFC 6455 hashes into Sec-WebSocket-Accept.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsDial upgrades the stream to a WebSocket at path.
func wsDial(r io.Reader, w io.Writer, path string) (*wsConn, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(raw)
	req := "GET " + path + " HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(w, req); err != nil {
		return nil, err
	}
	br := bufio.NewReader(r)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		return nil, fmt.Errorf("websocket upgrade: %w", err)
	}
	_ = resp.Body.Close()
	sum := sha1.Sum([]byte(key + wsGUID))
	if resp.StatusCode != http.StatusSwitchingProtocols ||
		resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("websocket upgrade refused: %s", resp.Status)
	}
	return &wsConn{r: br, w: w}, nil
}

// WriteText sends one masked text frame.
func (c *wsConn) WriteText(p []byte) error { return c.writeFrame(0x1, p) }

func (c *wsConn) writeFrame(op byte, p []byte) error {
	head := []byte{0x80 | op}
	switch n := len(p); {
	case n < 126:
		head = append(head, 0x80|byte(n))
	case n <= 0xffff:
		head = append(head, 0x80|126)
		head = binary.BigEndian.AppendUint16(head, uint16(n))
	default:
		head = append(head, 0x80|127)
		head = binary.BigEndian.AppendUint64(head, uint64(n))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	frame := append(head, mask...)
	for i, b := range p {
		frame = append(frame, b^mask[i%4])
	}
	_, err := c.w.Write(frame)
	return err
}

// ReadMessage returns the next text or binary message, joining fragments.
// A close frame is io.EOF.
func (c *wsConn) ReadMessage() ([]byte, error) {
	var msg []byte
	for {
		fin, op, p, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch op {
		case 0x8:
			return nil, io.EOF
		case 0x9:
			if err := c.writeFrame(0xA, p); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		}
		msg = append(msg, p...)
		if fin {
			return msg, nil
		}
	}
}

// wsMaxFrame bounds one frame, so a bad length cannot exhaust memory.
const wsMaxFrame = 16 << 20

func (c *wsConn) readFrame() (bool, byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(c.r, h[:]); err != nil {
		return false, 0, nil, err
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(c.r, b[:]); err != nil {
			return false, 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(c.r, b[:]); err != nil {
			return false, 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > wsMaxFrame {
		return false, 0, nil, fmt.Errorf("websocket frame of %d bytes is too large", n)
	}
	var mask []byte
	if h[1]&0x80 != 0 {
		mask = make([]byte, 4)
		if _, err := io.ReadFull(c.r, mask); err != nil {
			return false, 0, nil, err
		}
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(c.r, p); err != nil {
		return false, 0, nil, err
	}
	if mask != nil {
		for i := range p {
			p[i] ^= mask[i%4]
		}
	}
	return h[0]&0x80 != 0, h[0] & 0x0f, p, nil
}

// codexDaemon runs exchange over a WebSocket to the daemon serving home,
// through `codex app-server proxy`. The process and its pipes close when
// exchange returns or ctx ends.
func codexDaemon(ctx context.Context, bin, home string, exchange func(*wsConn) error) error {
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
	stop := context.AfterFunc(ctx, func() { _ = out.Close() })
	defer stop()
	conn, err := wsDial(out, in, "/rpc")
	if err == nil {
		err = exchange(conn)
	}
	if err != nil && ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	return err
}
