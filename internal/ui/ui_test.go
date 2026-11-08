package ui

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a writer the serving goroutine and the test share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestMainRefusesBadArguments(t *testing.T) {
	connected := false
	connect := func() (Caller, error) { connected = true; return nil, errors.New("unused") }
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--port", "1"}, Usage},
		{[]string{"extra"}, Usage},
		{[]string{"--listen", "0.0.0.0:7332"}, "must be a loopback address"},
		{[]string{"--listen", ":7332"}, "must be a loopback address"},
		{[]string{"--listen", "devbox:7332"}, "must be a loopback address"},
	} {
		var stderr bytes.Buffer
		if code := Main(tc.args, Env{Out: io.Discard, Err: &stderr, Connect: connect}); code != 2 || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("Main(%v) = %d, %q; want 2 and %q", tc.args, code, stderr.String(), tc.want)
		}
	}
	if connected {
		t.Fatal("invalid arguments should not read the credential")
	}
}

func TestMainReportsConnectAndListenFailures(t *testing.T) {
	var stderr bytes.Buffer
	failing := func() (Caller, error) { return nil, errors.New("credential must be a regular file") }
	if code := Main(nil, Env{Out: io.Discard, Err: &stderr, Connect: failing}); code != 1 || !strings.Contains(stderr.String(), "credential must be") {
		t.Fatalf("connect failure = %d %q", code, stderr.String())
	}
	stderr.Reset()
	fc := newFakeControl(t)
	ok := func() (Caller, error) { return fc.client(), nil }
	if code := Main([]string{"--listen", "127.0.0.1:99999"}, Env{Out: io.Discard, Err: &stderr, Connect: ok}); code != 1 || !strings.Contains(stderr.String(), "engine ui:") {
		t.Fatalf("listen failure = %d %q", code, stderr.String())
	}
}

func TestMainServesLoopbackUntilCancelled(t *testing.T) {
	fc := newFakeControl(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	read, stdout := io.Pipe()
	stderr := &syncBuffer{}
	done := make(chan int, 1)
	go func() {
		done <- Main([]string{"--listen", "127.0.0.1:0"}, Env{Out: stdout, Err: stderr, Context: ctx,
			Connect: func() (Caller, error) { return fc.client(), nil }})
	}()
	line, err := bufio.NewReader(read).ReadString('\n')
	m := regexp.MustCompile(`^engine ui: serving (http://127\.0\.0\.1:\d+/)\n$`).FindStringSubmatch(line)
	if err != nil || m == nil {
		t.Fatalf("first line %q (%v), want the loopback address", line, err)
	}
	url := m[1]
	res, err := http.Get(url + "api/plans")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), `"id":"p24"`) || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("GET plans = %d %s", res.StatusCode, body)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("Main = %d after shutdown, stderr %q", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Main did not shut down")
	}
	if _, err = http.Get(url); err == nil {
		t.Fatal("the dashboard still answers after shutdown")
	}
}

func TestLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1:7332": true, "127.0.0.1": true, "127.8.0.1:1": true, "localhost": true, "LOCALHOST:80": true,
		"[::1]:7332": true, "::1": true,
		"": false, ":7332": false, "0.0.0.0:7332": false, "[::]:7332": false, "devbox:7332": false,
		"localhost.evil.example": false, "10.0.0.1": false,
	} {
		if got := LoopbackHost(host); got != want {
			t.Errorf("LoopbackHost(%q) = %t, want %t", host, got, want)
		}
	}
}
