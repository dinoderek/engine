package controlcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// outage is a server that refuses connections for a while, then answers with
// the given statuses, then with 200. It records the request key of every try.
type outage struct {
	mu       sync.Mutex
	refuse   int   // connection attempts still refused
	statuses []int // replies after that, one per try
	keys     []string
	server   *httptest.Server
}

func newOutage(t *testing.T, refuse int, statuses ...int) *outage {
	t.Helper()
	o := &outage{refuse: refuse, statuses: statuses}
	o.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		status := 200
		if len(o.statuses) > 0 {
			status, o.statuses = o.statuses[0], o.statuses[1:]
		}
		o.mu.Unlock()
		w.WriteHeader(status)
		if status == 200 {
			_, _ = io.WriteString(w, "{}")
			return
		}
		_, _ = io.WriteString(w, `{"error":"no"}`)
	}))
	t.Cleanup(o.server.Close)
	return o
}

// RoundTrip refuses the first connections, as a server that is down does.
func (o *outage) RoundTrip(r *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	var req struct{ Key string }
	_ = json.Unmarshal(body, &req)
	r.Body = io.NopCloser(bytes.NewReader(body))
	o.mu.Lock()
	o.keys = append(o.keys, req.Key)
	refuse := o.refuse > 0
	if refuse {
		o.refuse--
	}
	o.mu.Unlock()
	if refuse {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	}
	return http.DefaultTransport.RoundTrip(r)
}

// clock is a fake time: a sleep moves it, and nothing waits.
type clock struct {
	now    time.Time
	sleeps []time.Duration
	jitter time.Duration
}

func (c *clock) retry() *Retry {
	return &Retry{
		Now: func() time.Time { return c.now },
		Sleep: func(_ context.Context, d time.Duration) error {
			c.sleeps = append(c.sleeps, d)
			c.now = c.now.Add(d)
			return nil
		},
		Jitter: func(time.Duration) time.Duration { return c.jitter },
	}
}

func (c *clock) total() (sum time.Duration) {
	for _, d := range c.sleeps {
		sum += d
	}
	return sum
}

func retryEnv(o *outage, c *clock, env map[string]string) (Env, *bytes.Buffer) {
	errs := &bytes.Buffer{}
	return Env{
		In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: errs, Home: "/nonexistent",
		Getenv: func(k string) string { return env[k] },
		Client: &control.Client{URL: o.server.URL, Token: control.Secret(), HTTP: &http.Client{Transport: o}},
		Retry:  c.retry(),
	}, errs
}

var inSession = map[string]string{"ENGINE_ATTEMPT_ID": "a1"}

func start() *clock { return &clock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} }

func TestACommandRidesOutRefusedConnectionsAndServerErrors(t *testing.T) {
	o := newOutage(t, 3, 503, 502)
	c := start()
	e, errs := retryEnv(o, c, inSession)
	if code := Main([]string{"updates", "t1", "--request-key", "k1"}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if len(o.keys) < 5 {
		t.Fatalf("tries = %d, want the refusals and the 5xx replies before the 200", len(o.keys))
	}
	for _, k := range o.keys {
		if k != "k1" {
			t.Fatalf("a try carried key %q, want every try to carry k1: %v", k, o.keys)
		}
	}
	want := "engine server unreachable; retrying until 03:05:35 (request key k1)\n"
	if errs.String() != want {
		t.Fatalf("stderr = %q, want %q (one line, on the first retry)", errs, want)
	}
	if len(c.sleeps) < 2 || c.sleeps[0] != time.Second || c.sleeps[1] != 2*time.Second {
		t.Fatalf("waits = %v, want 1s then 2s", c.sleeps)
	}
}

func TestARetryNamesTheKeyItMade(t *testing.T) {
	o := newOutage(t, 2)
	e, errs := retryEnv(o, start(), inSession)
	if code := Main([]string{"updates", "t1"}, e); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	key := o.keys[0]
	if key == "" || !strings.Contains(errs.String(), "(request key "+key+")") {
		t.Fatalf("stderr %q does not name the key %q", errs, key)
	}
	for _, k := range o.keys {
		if k != key {
			t.Fatalf("keys differ: %v", o.keys)
		}
	}
}

func TestAClientErrorNeverRetries(t *testing.T) {
	o := newOutage(t, 0, 404)
	c := start()
	e, errs := retryEnv(o, c, inSession)
	if code := Main([]string{"updates", "t1"}, e); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(o.keys) != 1 || len(c.sleeps) != 0 || strings.Contains(errs.String(), "retrying") {
		t.Fatalf("a 4xx retried: %d tries, waits %v, stderr %q", len(o.keys), c.sleeps, errs)
	}
}

func TestAServerDownPastTheWindowReturnsItsError(t *testing.T) {
	o := newOutage(t, 1<<30)
	c := start()
	e, errs := retryEnv(o, c, inSession)
	if code := Main([]string{"updates", "t1", "--request-key", "k2"}, e); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if got := strings.Count(errs.String(), "retrying until"); got != 1 {
		t.Fatalf("the retry line printed %d times: %q", got, errs)
	}
	if !strings.Contains(errs.String(), "request key k2; retry with the same key") {
		t.Fatalf("the final error does not name the key: %q", errs)
	}
	if c.total() >= DefaultRetryFor {
		t.Fatalf("waited %v, the window is %v", c.total(), DefaultRetryFor)
	}
	for i, want := range []time.Duration{1, 2, 4, 8, 15, 15} {
		if c.sleeps[i] != want*time.Second {
			t.Fatalf("waits = %v, want doubling from 1s to a 15s cap", c.sleeps)
		}
	}
	for _, d := range c.sleeps {
		if d > 15*time.Second {
			t.Fatalf("a wait of %v passes the 15s cap", d)
		}
	}
}

func TestJitterLengthensAWaitButNeverPastTheCap(t *testing.T) {
	o := newOutage(t, 1<<30)
	c := start()
	c.jitter = 3 * time.Second
	e, _ := retryEnv(o, c, map[string]string{"ENGINE_RETRY_FOR": "30s"})
	Main([]string{"updates", "t1"}, e)
	if c.sleeps[0] != 4*time.Second || c.sleeps[1] != 5*time.Second {
		t.Fatalf("waits = %v, want 1s+3s then 2s+3s", c.sleeps)
	}
	for _, d := range c.sleeps {
		if d > 15*time.Second {
			t.Fatalf("a wait of %v passes the cap", d)
		}
	}
}

func TestEngineRetryForSetsTheWindow(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		max  time.Duration // waits sum to under this; 0 means none
	}{
		{"a short window", map[string]string{"ENGINE_ATTEMPT_ID": "a1", "ENGINE_RETRY_FOR": "10s"}, 10 * time.Second},
		{"zero turns it off", map[string]string{"ENGINE_ATTEMPT_ID": "a1", "ENGINE_RETRY_FOR": "0"}, 0},
		{"the operator's own", map[string]string{"ENGINE_RETRY_FOR": "5s"}, 5 * time.Second},
		{"the operator's default is none", map[string]string{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := newOutage(t, 1<<30)
			c := start()
			e, _ := retryEnv(o, c, tc.env)
			if code := Main([]string{"updates", "t1"}, e); code != 1 {
				t.Fatalf("exit %d, want 1", code)
			}
			if tc.max == 0 && len(c.sleeps) != 0 {
				t.Fatalf("retried: %v", c.sleeps)
			}
			if tc.max > 0 && (len(c.sleeps) == 0 || c.total() >= tc.max) {
				t.Fatalf("waited %v in %v, want some, under %v", c.sleeps, c.total(), tc.max)
			}
		})
	}
}

func TestABadRetryWindowIsAUsageError(t *testing.T) {
	for _, bad := range []string{"soon", "-5s", "90"} {
		o := newOutage(t, 0)
		e, errs := retryEnv(o, start(), map[string]string{"ENGINE_RETRY_FOR": bad})
		if code := Main([]string{"updates", "t1"}, e); code != 2 {
			t.Fatalf("%q: exit %d, want 2", bad, code)
		}
		if !strings.Contains(errs.String(), "ENGINE_RETRY_FOR takes a duration") || len(o.keys) != 0 {
			t.Fatalf("%q: stderr %q, %d requests", bad, errs, len(o.keys))
		}
	}
}

func TestHostStatusAndLintRetryToo(t *testing.T) {
	for _, args := range [][]string{{"host", "status", "--all"}, {"lint", "p1"}} {
		o := newOutage(t, 1<<30)
		c := start()
		e, errs := retryEnv(o, c, map[string]string{"ENGINE_RETRY_FOR": "5s"})
		if code := Main(args, e); code != 1 {
			t.Fatalf("%v: exit %d, want 1", args, code)
		}
		if len(c.sleeps) == 0 || !strings.Contains(errs.String(), "retrying until") {
			t.Fatalf("%v did not retry: waits %v, stderr %q", args, c.sleeps, errs)
		}
		e, errs = retryEnv(o, c, map[string]string{"ENGINE_RETRY_FOR": "soon"})
		if code := Main(args, e); code != 2 || !strings.Contains(errs.String(), "ENGINE_RETRY_FOR") {
			t.Fatalf("%v with a bad window: exit %d, stderr %q", args, code, errs)
		}
	}
}

func TestACancelledWaitEndsTheRetryWithTheServersError(t *testing.T) {
	o := newOutage(t, 1<<30)
	e, errs := retryEnv(o, start(), inSession)
	e.Retry.Sleep = func(context.Context, time.Duration) error { return context.Canceled }
	if code := Main([]string{"updates", "t1"}, e); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if strings.Contains(errs.String(), "canceled") || !strings.Contains(errs.String(), "connection refused") {
		t.Fatalf("stderr %q, want the server's error, not the cancellation", errs)
	}
}

func TestATryIsCutAtTheWindowsEnd(t *testing.T) {
	hung := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-hung }))
	t.Cleanup(func() { close(hung); server.Close() })
	e := Env{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Getenv: func(string) string { return "" }}
	client := control.Client{URL: server.URL, Token: control.Secret()}
	_, err := callRetrying(context.Background(), e, client, control.Request{Op: "list"}, 50*time.Millisecond)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline: a hung server must not outlast the window", err)
	}
}

func TestOnlyServerFailuresAreTransient(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{context.DeadlineExceeded, true},
		{&control.Problem{Status: 503}, true},
		{&control.Problem{Status: 500}, true},
		{&control.Problem{Status: 409}, false},
		{&control.Problem{Status: 404}, false},
	} {
		if got := transient(tc.err); got != tc.want {
			t.Errorf("transient(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
