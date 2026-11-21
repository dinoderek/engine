package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// recorder is a fake Caller: it records every request and answers from reply.
type recorder struct {
	mu    sync.Mutex
	calls []control.Request
	reply func(control.Request) (control.Response, error)
}

func (c *recorder) Call(_ context.Context, r control.Request) (control.Response, error) {
	c.mu.Lock()
	c.calls = append(c.calls, r)
	c.mu.Unlock()
	if c.reply != nil {
		return c.reply(r)
	}
	return control.Response{}, nil
}

func (c *recorder) sent() []control.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]control.Request{}, c.calls...)
}

const dashHost = "devbox.tail.ts.net"

func newWriter(c Caller) http.Handler {
	return handler(newUpstream(c, time.Now, nil), dashHost)
}

// send posts a write as the page does: JSON, from the dashboard's own origin.
func send(t *testing.T, h http.Handler, path, body string) (*http.Response, Envelope) {
	t.Helper()
	return post(t, h, attempt{host: dashHost, origin: "https://" + dashHost, kind: "application/json"}, path, body)
}

// attempt is who a POST claims to be: the Host, Origin and Content-Type it sends.
type attempt struct{ host, origin, kind string }

func post(t *testing.T, h http.Handler, a attempt, path, body string) (*http.Response, Envelope) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Host = a.host
	if a.origin != "" {
		req.Header.Set("Origin", a.origin)
	}
	if a.kind != "" {
		req.Header.Set("Content-Type", a.kind)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	raw, _ := io.ReadAll(w.Result().Body)
	var env Envelope
	_ = json.Unmarshal(raw, &env) // a refusal before the routes is plain text
	return w.Result(), env
}

func question(n int, choices ...string) control.Response {
	return control.Response{Updates: []control.Update{{ID: n, Choices: choices}}}
}

func TestEveryWriteBecomesOneControlRequest(t *testing.T) {
	inbox := func(r control.Request) (control.Response, error) {
		if r.Op == "updates" {
			if r.After != 39 || r.ID != "" {
				t.Errorf("question read = %+v, want the updates after 39", r)
			}
			return question(40, "keep", "drop"), nil
		}
		return control.Response{Item: &control.Item{Version: 7}, Updates: []control.Update{{ID: 91}}}, nil
	}
	for _, tc := range []struct {
		path, body string
		want       control.Request
		reads      int
	}{
		{"/api/updates/40/resolve", `{"key":"k","choice":1,"note":"soon"}`, control.Request{Op: "resolve", ReplyTo: 40, Body: "drop\nsoon", Key: "k"}, 1},
		{"/api/updates/40/resolve", `{"key":"k","choice":0}`, control.Request{Op: "resolve", ReplyTo: 40, Body: "keep", Key: "k"}, 1},
		{"/api/updates/40/resolve", `{"key":"k","note":"just so"}`, control.Request{Op: "resolve", ReplyTo: 40, Body: "just so", Key: "k"}, 0},
		{"/api/updates/40/reply", `{"key":"k","body":"why?"}`, control.Request{Op: "reply", ReplyTo: 40, Body: "why?", Key: "k"}, 0},
		{"/api/updates/40/ack", `{"key":"k"}`, control.Request{Op: "ack", ReplyTo: 40, Key: "k"}, 0},
		{"/api/items/t5/say", `{"key":"k","body":"hello"}`, control.Request{Op: "say", ID: "t5", Body: "hello", Key: "k"}, 0},
		{"/api/items/p2/say", `{"key":"k","body":"hello"}`, control.Request{Op: "say", ID: "p2", Body: "hello", Key: "k"}, 0},
		{"/api/plans/p2/approve", `{"key":"k","version":3}`, control.Request{Op: "approve", ID: "p2", Version: 3, Key: "k"}, 0},
		{"/api/plans/p2/run", `{"key":"k"}`, control.Request{Op: "run", ID: "p2", Key: "k"}, 0},
		{"/api/plans/p2/stop", `{"key":"k"}`, control.Request{Op: "stop", ID: "p2", Key: "k"}, 0},
		{"/api/tasks/t5/stop", `{"key":"k"}`, control.Request{Op: "task-stop", ID: "t5", Key: "k"}, 0},
		{"/api/tasks/t5/resume", `{"key":"k"}`, control.Request{Op: "task-resume", ID: "t5", Key: "k"}, 0},
		{"/api/tasks/t5/retry", `{"key":"k","version":4}`, control.Request{Op: "state", State: "todo", ID: "t5", Version: 4, Key: "k"}, 0},
		{"/api/tasks/t5/complete-external", `{"key":"k","version":4,"pr":"https://example.test/change"}`,
			control.Request{Op: "complete-external", ID: "t5", Version: 4, Key: "k", Item: control.Item{Kind: "task", PR: "https://example.test/change"}}, 0},
	} {
		c := &recorder{reply: inbox}
		res, env := send(t, newWriter(c), tc.path, tc.body)
		sent := c.sent()
		if res.StatusCode != 200 || env.Error != "" || len(sent) != tc.reads+1 {
			t.Errorf("POST %s %s = %d %+v after %d calls, want one write and %d read", tc.path, tc.body, res.StatusCode, env, len(sent), tc.reads)
			continue
		}
		if got := sent[len(sent)-1]; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("POST %s sent %+v, want %+v", tc.path, got, tc.want)
		}
		if !reflect.DeepEqual(env.Data, map[string]any{"version": float64(7), "update": float64(91)}) {
			t.Errorf("POST %s returned %v, want the new version and update", tc.path, env.Data)
		}
	}
}

func TestOnlyTheWriteAllowlistIsForwarded(t *testing.T) {
	c := &recorder{}
	up := newUpstream(c, time.Now, nil)
	for _, r := range []control.Request{
		{Op: "edit", ID: "t5"}, {Op: "create"}, {Op: "claim"}, {Op: "ask", ID: "t5", Body: "x"}, {Op: "list"}, {Op: "get", ID: "t5"},
		{Op: "merge-request", ID: "t5"}, {Op: "attempt-reconcile", ID: "t5"}, {Op: "state", ID: "t5", State: "done"},
		{Op: "state", ID: "t5", State: "failed"}, {Op: "pane-input"},
	} {
		if _, err := up.forward(context.Background(), r); err == nil {
			t.Errorf("forward(%+v) succeeded, want a refusal", r)
		}
	}
	if len(c.sent()) != 0 {
		t.Fatalf("refused operations reached the control server: %v", c.sent())
	}
	for _, op := range writeOps {
		if op == "state" {
			continue
		}
		if _, err := up.forward(context.Background(), control.Request{Op: op}); err != nil {
			t.Errorf("forward(%s) = %v, want it allowed", op, err)
		}
	}
	if slicesOverlap(readOps, writeOps) {
		t.Fatalf("readOps %v and writeOps %v share an operation", readOps, writeOps)
	}
}

func slicesOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func TestControlRefusalsPassThrough(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		reason string
	}{
		{&control.Problem{Status: 400, Message: "message body is required"}, 400, ""},
		{&control.Problem{Status: 404, Message: "unknown update"}, 404, ""},
		{&control.Problem{Status: 409, Message: "version is 9", Reason: "stale"}, 409, "stale"},
		{&control.Problem{Status: 500, Message: "boom", Reason: "hidden"}, 502, ""},
		{errors.New("dial tcp 10.0.0.1: refused"), 502, ""},
	} {
		c := &recorder{reply: func(control.Request) (control.Response, error) { return control.Response{}, tc.err }}
		res, env := send(t, newWriter(c), "/api/plans/p2/approve", `{"key":"k","version":1}`)
		if res.StatusCode != tc.status || env.Reason != tc.reason || env.Error == "" || strings.Contains(env.Error, "10.0.0.1") {
			t.Errorf("control said %v: got %d %+v, want %d with reason %q", tc.err, res.StatusCode, env, tc.status, tc.reason)
		}
		if env.Error != "" && tc.status < 500 && !strings.Contains(tc.err.Error(), env.Error) {
			t.Errorf("control said %v: page got message %q", tc.err, env.Error)
		}
	}
}

func TestAResendSendsTheSameKey(t *testing.T) {
	c := &recorder{}
	h := newWriter(c)
	for range 2 {
		if res, _ := send(t, h, "/api/tasks/t5/stop", `{"key":"7f1c"}`); res.StatusCode != 200 {
			t.Fatalf("stop = %d", res.StatusCode)
		}
	}
	sent := c.sent()
	if len(sent) != 2 || sent[0].Key != "7f1c" || !reflect.DeepEqual(sent[0], sent[1]) {
		t.Fatalf("sent %+v, want two identical requests with the page's key", sent)
	}
}

func TestChoiceNeedsAnOpenQuestionWithThatChoice(t *testing.T) {
	for name, tc := range map[string]struct {
		reply  control.Response
		choice string
		status int
	}{
		"no such question": {control.Response{}, "0", 404},
		"another update":   {question(41, "a"), "0", 404},
		"index too big":    {question(40, "a"), "1", 400},
		"negative index":   {question(40, "a"), "-1", 400},
		"no choices":       {question(40), "0", 400},
	} {
		c := &recorder{reply: func(control.Request) (control.Response, error) { return tc.reply, nil }}
		res, env := send(t, newWriter(c), "/api/updates/40/resolve", `{"key":"k","choice":`+tc.choice+`}`)
		if res.StatusCode != tc.status || env.Error == "" {
			t.Errorf("%s: %d %+v, want %d", name, res.StatusCode, env, tc.status)
		}
		for _, r := range c.sent() {
			if r.Op != "updates" {
				t.Errorf("%s: sent %s, want only the question read", name, r.Op)
			}
		}
	}
	c := &recorder{}
	if res, _ := send(t, newWriter(c), "/api/updates/40/resolve", `{"key":"k"}`); res.StatusCode != 400 || len(c.sent()) != 0 {
		t.Fatalf("an empty resolution = %d with %d calls, want 400 and none", res.StatusCode, len(c.sent()))
	}
}

// A resolve whose reply was lost is resent with its key: the question read
// still finds the question, and the request is the same, so the server replays.
func TestAResentResolveStillReadsItsQuestion(t *testing.T) {
	applied := false
	c := &recorder{}
	c.reply = func(r control.Request) (control.Response, error) {
		if r.Op == "updates" {
			return question(40, "keep", "drop"), nil // listed whether or not resolved
		}
		applied = true
		return control.Response{Updates: []control.Update{{ID: 91}}}, nil
	}
	h := newWriter(c)
	for range 2 {
		if res, env := send(t, h, "/api/updates/40/resolve", `{"key":"k","choice":1}`); res.StatusCode != 200 || env.Error != "" {
			t.Fatalf("resolve = %d %+v, want 200 each time", res.StatusCode, env)
		}
	}
	var writes []control.Request
	for _, r := range c.sent() {
		if r.Op == "resolve" {
			writes = append(writes, r)
		}
	}
	if !applied || len(writes) != 2 || !reflect.DeepEqual(writes[0], writes[1]) || writes[0].Key != "k" {
		t.Fatalf("resolves sent %+v, want the same request twice", writes)
	}
}

func TestRefusedWritesNeverReachTheControlServer(t *testing.T) {
	const good = `{"key":"k"}`
	big := `{"key":"k","body":"` + strings.Repeat("x", writeLimit) + `"}`
	for name, tc := range map[string]struct {
		a          attempt
		path, body string
		status     int
	}{
		"other origin":      {attempt{dashHost, "https://evil.example", "application/json"}, "/api/tasks/t5/stop", good, 403},
		"no origin":         {attempt{dashHost, "", "application/json"}, "/api/tasks/t5/stop", good, 403},
		"opaque origin":     {attempt{dashHost, "null", "application/json"}, "/api/tasks/t5/stop", good, 403},
		"origin with path":  {attempt{dashHost, "https://" + dashHost + "/x", "application/json"}, "/api/tasks/t5/stop", good, 403},
		"origin other port": {attempt{dashHost + ":7332", "https://" + dashHost, "application/json"}, "/api/tasks/t5/stop", good, 403},
		"scheme not http":   {attempt{dashHost, "ftp://" + dashHost, "application/json"}, "/api/tasks/t5/stop", good, 403},
		"form post":         {attempt{dashHost, "https://" + dashHost, "application/x-www-form-urlencoded"}, "/api/tasks/t5/stop", "key=k", 415},
		"no content type":   {attempt{dashHost, "https://" + dashHost, ""}, "/api/tasks/t5/stop", good, 415},
		"text body":         {attempt{dashHost, "https://" + dashHost, "text/plain"}, "/api/tasks/t5/stop", good, 415},
		"not json":          {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5/stop", `key=k`, 400},
		"json array":        {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5/stop", `["k"]`, 400},
		"json null":         {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5/stop", `null`, 400},
		"trailing data":     {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5/stop", good + `{}`, 400},
		"unknown field":     {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5/stop", `{"key":"k","op":"edit"}`, 400},
		"another route's":   {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/updates/4/ack", `{"key":"k","body":"x"}`, 400},
		"missing key":       {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5/stop", `{}`, 400},
		"long key":          {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5/stop", `{"key":"` + strings.Repeat("k", 201) + `"}`, 400},
		"missing version":   {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5/retry", good, 400},
		"zero version":      {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5/retry", `{"key":"k","version":0}`, 400},
		"missing body":      {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/items/t5/say", good, 400},
		"wrong type":        {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/items/t5/say", `{"key":"k","body":5}`, 400},
		"oversized":         {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/items/t5/say", big, 413},
		"task on plan path": {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/plans/t5/run", good, 404},
		"plan on task path": {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/p2/stop", good, 404},
		"repo to say":       {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/items/r1/say", `{"key":"k","body":"x"}`, 404},
		"bad id":            {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5x/stop", good, 404},
		"zero id":           {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t0/stop", good, 404},
		"bad update":        {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/updates/abc/ack", good, 404},
		"signed update":     {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/updates/+5/ack", good, 404},
		"zero update":       {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/updates/0/ack", good, 404},
		"huge update":       {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/updates/99999999999/ack", good, 404},
		"other host":        {attempt{"evil.example", "https://evil.example", "application/json"}, "/api/tasks/t5/stop", good, 421},
		"unlisted route":    {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/tasks/t5/fail", good, 404},
		"a read as a post":  {attempt{dashHost, "https://" + dashHost, "application/json"}, "/api/plans", good, 405},
		"the command api":   {attempt{dashHost, "https://" + dashHost, "application/json"}, "/v1/command", `{"op":"edit"}`, 404},
	} {
		c := &recorder{}
		if res, _ := post(t, newWriter(c), tc.a, tc.path, tc.body); res.StatusCode != tc.status {
			t.Errorf("%s: %d, want %d", name, res.StatusCode, tc.status)
		}
		if got := c.sent(); len(got) != 0 {
			t.Errorf("%s reached the control server: %+v", name, got)
		}
	}
}

func TestHostFlagAdmitsItsNameAndLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		dashHost: true, "DEVBOX.tail.ts.net:443": true, "127.0.0.1:7332": true, "localhost": true,
		"evil.example": false, "x." + dashHost: false, dashHost + ".evil.example": false, "": false,
	} {
		if got := AllowedHost(host, dashHost); got != want {
			t.Errorf("AllowedHost(%q) = %t, want %t", host, got, want)
		}
	}
	if AllowedHost("devbox", "") || !AllowedHost("localhost", "") {
		t.Error("without --host only loopback names are allowed")
	}
	h := newWriter(&recorder{})
	for host, want := range map[string]int{dashHost: 200, "localhost:7332": 200, "evil.example": 421} {
		req := httptest.NewRequest("GET", "/api/sessions", nil)
		req.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("GET %s = %d, want %d", host, w.Code, want)
		}
	}
	if res, _ := post(t, h, attempt{"127.0.0.1:7332", "http://127.0.0.1:7332", "application/json"}, "/api/tasks/t5/stop", `{"key":"k"}`); res.StatusCode != 200 {
		t.Fatalf("a loopback write = %d, want 200", res.StatusCode)
	}
}

func TestMainParsesHost(t *testing.T) {
	for host, want := range map[string]bool{
		"devbox": true, "devbox.tail.ts.net": true, "Dev-Box.example": true,
		"dev box": false, "devbox:7332": false, "-x": false, "a..b": false, "http://devbox": false, "devbox/": false,
	} {
		if got := hostName.MatchString(host); got != want {
			t.Errorf("--host %q valid = %t, want %t", host, got, want)
		}
	}
	var stderr strings.Builder
	connect := func() (Caller, error) { return nil, errors.New("stop here") }
	if code := Main([]string{"--host", "devbox.tail.ts.net"}, Env{Out: io.Discard, Err: &stderr, Connect: connect}); code != 1 || !strings.Contains(stderr.String(), "stop here") {
		t.Fatalf("--host with a name = %d %q, want it to go on and connect", code, stderr.String())
	}
	stderr.Reset()
	if code := Main([]string{"--host", "dev box"}, Env{Out: io.Discard, Err: &stderr, Connect: connect}); code != 2 || !strings.Contains(stderr.String(), "--host must be a host name") {
		t.Fatalf("--host with a bad name = %d %q", code, stderr.String())
	}
}

// A resolve is resent after its reply is lost, against a real control
// service: the question is still readable, so the request is the same and the
// server replays the first result; a new key for it gets the server's refusal.
func TestAResentResolveReplaysAgainstTheRealServer(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is absent")
	}
	store, err := control.Open(filepath.Join(t.TempDir(), "control.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	token := control.Secret()
	server := httptest.NewServer(control.Handler(control.Service{Store: store, Now: time.Now}, token, nil))
	t.Cleanup(server.Close)
	client := control.Client{URL: server.URL, Token: token}
	call := func(r control.Request) control.Response {
		t.Helper()
		out, err := client.Call(context.Background(), r)
		if err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
		return out
	}
	repo := call(control.Request{Op: "create", Item: control.Item{Kind: "repo", Title: "r", Slug: "o/r", Remote: "git@example.test:o/r.git"}})
	plan := call(control.Request{Op: "create", Item: control.Item{Kind: "plan", Title: "p", Repo: repo.Item.ID}})
	asked := call(control.Request{Op: "ask", ID: plan.Item.ID, Body: "Which?", Choices: []string{"keep", "drop"}}).Updates[0].ID

	h := handler(newUpstream(client, time.Now, nil), "")
	path := "/api/updates/" + strconv.Itoa(asked) + "/resolve"
	a := attempt{"127.0.0.1:7332", "http://127.0.0.1:7332", "application/json"}
	first, env := post(t, h, a, path, `{"key":"k1","choice":1,"note":"later"}`)
	if first.StatusCode != 200 || env.Error != "" {
		t.Fatalf("resolve = %d %+v", first.StatusCode, env)
	}
	resent, again := post(t, h, a, path, `{"key":"k1","choice":1,"note":"later"}`)
	if resent.StatusCode != 200 || !reflect.DeepEqual(again.Data, env.Data) {
		t.Fatalf("resend = %d %+v, want the first result %+v", resent.StatusCode, again, env)
	}
	if res, other := post(t, h, a, path, `{"key":"k2","choice":0}`); res.StatusCode != 409 || other.Error == "" {
		t.Fatalf("another key = %d %+v, want the server's 409", res.StatusCode, other)
	}
	got := call(control.Request{Op: "updates", ID: plan.Item.ID})
	last := got.Updates[len(got.Updates)-1]
	if last.Kind != "resolved" || last.Body != "drop\nlater" || last.ReplyTo != asked {
		t.Fatalf("the question was resolved with %+v, want the choice's text then the note, once", last)
	}
}
