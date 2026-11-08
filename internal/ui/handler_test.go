package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// fakeControl is the control server's command endpoint with canned replies.
// It records each operation and can fail on demand.
type fakeControl struct {
	t     *testing.T
	token string

	mu     sync.Mutex
	ops    []string
	fail   int                                            // status to fail every call with; 0 answers
	reads  func(control.Request) (control.Response, bool) // false answers unknown item
	server *httptest.Server
}

func newFakeControl(t *testing.T) *fakeControl {
	f := &fakeControl{t: t, token: control.Secret()}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeControl) serve(w http.ResponseWriter, r *http.Request) {
	var req control.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("decode request: %v", err)
	}
	f.mu.Lock()
	f.ops = append(f.ops, req.Op)
	fail, token := f.fail, f.token
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "credential required"})
		return
	}
	if fail != 0 || req.ID == "p404" {
		status := fail
		if status == 0 {
			status = 404
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown plan"})
		return
	}
	var out control.Response
	if f.reads != nil && (req.Op == "get" || req.Op == "updates") {
		resp, ok := f.reads(req)
		if !ok {
			w.WriteHeader(404)
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	switch {
	case req.Op == "list" && req.State == "repo":
		out.Items = fixtureRepos()
	case req.Op == "list" && req.State == "plan":
		out.Items = fixturePlans()
	case req.Op == "plan-view":
		out.Items = fixtureView()
	case req.Op == "attempts":
		out.Attempts = fixtureAttempts()
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeControl) failWith(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = status
}

func (f *fakeControl) rotate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token = control.Secret()
}

func (f *fakeControl) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.ops...)
}

func (f *fakeControl) client() control.Client {
	return control.Client{URL: f.server.URL, Token: f.token}
}

type dashboard struct {
	h   http.Handler
	now time.Time
	log *strings.Builder
}

func newDashboard(c Caller) *dashboard {
	d := &dashboard{now: time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC), log: &strings.Builder{}}
	logf := func(format string, args ...any) { d.log.WriteString(format) }
	d.h = handler(newUpstream(c, func() time.Time { return d.now }, logf))
	return d
}

func (d *dashboard) get(t *testing.T, method, host, target string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Host = host
	w := httptest.NewRecorder()
	d.h.ServeHTTP(w, req)
	b, _ := io.ReadAll(w.Result().Body)
	return w.Result(), string(b)
}

func TestRoutesServePageAndReads(t *testing.T) {
	fc := newFakeControl(t)
	d := newDashboard(fc.client())
	for _, tc := range []struct{ target, kind, contains string }{
		{"/", "text/html", `src="/app.js"`},
		{"/app.js", "text/javascript", "textContent"},
		{"/app.css", "text/css", "prefers-color-scheme"},
		{"/dom.js", "text/javascript", "window.engine.dom"},
		{"/panel.js", "text/javascript", "noopener"},
		{"/api/plans?repo=r1&state=executing&q=rollout", "application/json", `"id":"p24"`},
		{"/api/plans/p24", "application/json", `"in_progress":2`},
	} {
		res, body := d.get(t, "GET", "127.0.0.1:7332", tc.target)
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), tc.kind) || !strings.Contains(body, tc.contains) {
			t.Fatalf("GET %s = %d %s, body %.200q", tc.target, res.StatusCode, res.Header.Get("Content-Type"), body)
		}
		if res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("GET %s headers = %v, want private, no-store, CSP and nosniff", tc.target, res.Header)
		}
		if strings.Contains(body, fc.token) {
			t.Fatalf("GET %s leaked the operator credential", tc.target)
		}
	}
	if res, _ := d.get(t, "HEAD", "localhost:9000", "/api/plans"); res.StatusCode != 200 {
		t.Fatalf("HEAD = %d, want 200", res.StatusCode)
	}
	want := []string{"list", "list", "plan-view", "attempts", "list", "list"}
	if got := fc.calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("forwarded %v, want %v", got, want)
	}
}

func TestRoutesFilterPlans(t *testing.T) {
	d := newDashboard(newFakeControl(t).client())
	for _, tc := range []struct{ query, want string }{
		{"?repo=r9&state=executing", `"plans":[{"id":"p11"`},
		{"?q=nothing+like+this", `"plans":[]`},
	} {
		_, body := d.get(t, "GET", "[::1]:7332", "/api/plans"+tc.query)
		if !strings.Contains(body, tc.want) || !strings.Contains(body, `"total":4`) {
			t.Fatalf("plans%s = %s, want %s", tc.query, body, tc.want)
		}
	}
}

func TestRoutesRejectHostsMethodsAndUnknownPaths(t *testing.T) {
	fc := newFakeControl(t)
	d := newDashboard(fc.client())
	for _, tc := range []struct {
		method, host, target string
		status               int
	}{
		{"GET", "evil.example:7332", "/api/plans", http.StatusMisdirectedRequest},
		{"GET", "127.0.0.1.evil.example", "/", http.StatusMisdirectedRequest},
		{"GET", "192.168.1.4:7332", "/", http.StatusMisdirectedRequest},
		{"GET", "", "/", http.StatusMisdirectedRequest},
		{"POST", "127.0.0.1:7332", "/api/plans", http.StatusMethodNotAllowed},
		{"DELETE", "127.0.0.1:7332", "/api/plans/p24", http.StatusMethodNotAllowed},
		{"GET", "127.0.0.1:7332", "/v1/command", 404},
		{"GET", "127.0.0.1:7332", "/api/tasks", 404},
		{"GET", "127.0.0.1:7332", "/api/plans/t5", 404},
		{"GET", "127.0.0.1:7332", "/api/plans/p", 404},
		{"GET", "127.0.0.1:7332", "/static/app.js", 404},
	} {
		res, _ := d.get(t, tc.method, tc.host, tc.target)
		if res.StatusCode != tc.status {
			t.Errorf("%s %s (Host %q) = %d, want %d", tc.method, tc.target, tc.host, res.StatusCode, tc.status)
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s is cacheable", tc.method, tc.target)
		}
	}
	if res, _ := d.get(t, "PUT", "127.0.0.1", "/"); res.Header.Get("Allow") != "GET, HEAD" {
		t.Fatalf("Allow = %q", res.Header.Get("Allow"))
	}
	if got := fc.calls(); len(got) != 0 {
		t.Fatalf("rejected requests reached the control server: %v", got)
	}
}

func decode(t *testing.T, body string) Envelope {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return env
}

func TestUpstreamFailureKeepsTheLastViewStale(t *testing.T) {
	fc := newFakeControl(t)
	d := newDashboard(fc.client())
	fc.failWith(503)
	res, body := d.get(t, "GET", "127.0.0.1", "/api/plans/p24")
	if env := decode(t, body); res.StatusCode != 502 || env.Data != nil || !strings.Contains(env.Error, "control server error") {
		t.Fatalf("first failure = %d %s, want 502 without data", res.StatusCode, body)
	}
	fc.failWith(0)
	_, body = d.get(t, "GET", "127.0.0.1", "/api/plans/p24")
	if env := decode(t, body); env.Stale || !env.Fetched.Equal(d.now) {
		t.Fatalf("recovered view = %s, want fresh", body)
	}
	fetched := d.now
	d.now = d.now.Add(time.Minute)
	fc.failWith(500)
	res, body = d.get(t, "GET", "127.0.0.1", "/api/plans/p24")
	env := decode(t, body)
	if res.StatusCode != 200 || !env.Stale || !env.Fetched.Equal(fetched) || env.Error == "" || !strings.Contains(body, `"host":"devbox"`) {
		t.Fatalf("failure after success = %d %s, want the last view marked stale", res.StatusCode, body)
	}
}

func TestUnreachableControlServer(t *testing.T) {
	fc := newFakeControl(t)
	d := newDashboard(fc.client())
	if res, _ := d.get(t, "GET", "127.0.0.1", "/api/plans"); res.StatusCode != 200 {
		t.Fatalf("plans = %d", res.StatusCode)
	}
	fc.server.Close()
	res, body := d.get(t, "GET", "127.0.0.1", "/api/plans?state=done")
	env := decode(t, body)
	if res.StatusCode != 200 || !env.Stale || env.Error != "control server unreachable" || !strings.Contains(body, `"plans":[{"id":"p10"`) {
		t.Fatalf("unreachable = %d %s, want the filtered last list, stale", res.StatusCode, body)
	}
	if strings.Contains(body, fc.server.URL) || !strings.Contains(d.log.String(), "control server") {
		t.Fatalf("transport detail should be logged, not shown: %s / %s", body, d.log)
	}
}

func TestUnknownPlanIsNotServedStale(t *testing.T) {
	d := newDashboard(newFakeControl(t).client())
	res, body := d.get(t, "GET", "127.0.0.1", "/api/plans/p404")
	if res.StatusCode != 404 || decode(t, body).Error != "unknown plan" {
		t.Fatalf("unknown plan = %d %s", res.StatusCode, body)
	}
}

type recordingCaller struct{ ops []string }

func (r *recordingCaller) Call(_ context.Context, req control.Request) (control.Response, error) {
	r.ops = append(r.ops, req.Op)
	return control.Response{}, errors.New("unused")
}

func TestUpstreamForwardsOnlyReads(t *testing.T) {
	rc := &recordingCaller{}
	up := newUpstream(rc, time.Now, nil)
	for _, op := range []string{"create", "state", "say", "ask", "lock-acquire", "approve", "run", "session-view", ""} {
		if _, err := up.call(context.Background(), control.Request{Op: op}); err == nil || !strings.Contains(err.Error(), "not a dashboard read") {
			t.Errorf("op %q: err = %v, want refusal", op, err)
		}
	}
	for _, op := range readOps {
		if _, err := up.call(context.Background(), control.Request{Op: op, ID: "t1"}); err == nil || err.Error() != "unused" {
			t.Errorf("op %q: err = %v, want it forwarded", op, err)
		}
	}
	if strings.Join(rc.ops, ",") != strings.Join(readOps, ",") {
		t.Fatalf("forwarded %v, want only %v", rc.ops, readOps)
	}
}

func TestUpstreamReadsOneItemAtATime(t *testing.T) {
	rc := &recordingCaller{}
	up := newUpstream(rc, time.Now, nil)
	for _, op := range []string{"get", "updates"} {
		if _, err := up.call(context.Background(), control.Request{Op: op}); err == nil || !strings.Contains(err.Error(), "reads one item") {
			t.Errorf("op %q without an item: err = %v, want refusal", op, err)
		}
	}
	if len(rc.ops) != 0 {
		t.Fatalf("forwarded %v, want nothing", rc.ops)
	}
}

func TestCachedFilterReusesTheLastList(t *testing.T) {
	fc := newFakeControl(t)
	d := newDashboard(fc.client())
	if res, _ := d.get(t, "GET", "127.0.0.1", "/api/plans?cached=1"); res.StatusCode != 200 || len(fc.calls()) != 2 {
		t.Fatalf("cached read without a list = %d after %v, want a load", res.StatusCode, fc.calls())
	}
	_, body := d.get(t, "GET", "127.0.0.1", "/api/plans?state=done&cached=1")
	if env := decode(t, body); env.Stale || !strings.Contains(body, `"plans":[{"id":"p10"`) || len(fc.calls()) != 2 {
		t.Fatalf("cached filter = %s after %v, want the last list filtered with no call", body, fc.calls())
	}
	fc.failWith(500)
	d.get(t, "GET", "127.0.0.1", "/api/plans")
	_, body = d.get(t, "GET", "127.0.0.1", "/api/plans?q=rollout&cached=1")
	if env := decode(t, body); !env.Stale || env.Error == "" || !strings.Contains(body, `"id":"p24"`) {
		t.Fatalf("cached filter after a failure = %s, want it still marked stale", body)
	}
	fc.failWith(0)
	_, body = d.get(t, "GET", "127.0.0.1", "/api/plans")
	if env := decode(t, body); env.Stale || env.Error != "" {
		t.Fatalf("recovered list = %s, want fresh", body)
	}
}

func TestRejectedCredentialKeepsTheLastViewStale(t *testing.T) {
	fc := newFakeControl(t)
	d := newDashboard(fc.client())
	d.get(t, "GET", "127.0.0.1", "/api/plans/p24")
	fc.rotate() // the operator rotated the server's credential
	res, body := d.get(t, "GET", "127.0.0.1", "/api/plans/p24")
	if env := decode(t, body); res.StatusCode != 200 || !env.Stale || env.Error != "credential required" {
		t.Fatalf("after a 401 = %d %s, want the last view marked stale", res.StatusCode, body)
	}
}
