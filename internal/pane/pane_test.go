package pane

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
	"github.com/dinoderek/engine/internal/session"
)

const (
	attempt = "a9"
	token   = "0123456789abcdef0123456789abcdef"
	kickoff = "Use the engine skill's server executor mode for t7."
)

// server is a fake control server that records each request and its
// credential.
type server struct {
	mu       sync.Mutex
	requests []control.Request
	auth     []string
	// statuses are the failures the next requests get, in turn.
	statuses []int
}

func (s *server) start(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req control.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("request: %v", err)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, req)
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		if len(s.statuses) > 0 {
			w.WriteHeader(s.statuses[0])
			s.statuses = s.statuses[1:]
			_, _ = w.Write([]byte(`{"error":"refused"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(control.Response{Updates: []control.Update{{ID: 41}, {ID: 42}}})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// run is a server run's directory, with its connection, pane key, a typed
// kickoff and a transcript; pub is the pane key's public half.
type run struct {
	dir, transcript, pub string
}

func newRun(t *testing.T, url string) run {
	t.Helper()
	r := run{dir: t.TempDir()}
	r.transcript = filepath.Join(r.dir, "transcript.jsonl")
	conn, _ := json.Marshal(struct{ URL, Attempt string }{url, attempt})
	must(t, os.WriteFile(filepath.Join(r.dir, sessionFile), conn, 0o600))
	must(t, os.WriteFile(filepath.Join(r.dir, tokenFile), []byte(token+"\n"), 0o600))
	pub, err := session.NewPaneKey(r.dir)
	must(t, err)
	r.pub = pub
	must(t, session.LogTyped(r.dir, session.TextHash(kickoff)))
	return r
}

// say adds prompts to the run's fake transcript.
func (r run) say(t *testing.T, prompts ...string) {
	t.Helper()
	f, err := os.OpenFile(r.transcript, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	must(t, err)
	for _, p := range prompts {
		line, _ := json.Marshal(p)
		_, err = f.Write(append(line, '\n'))
		must(t, err)
	}
	must(t, f.Close())
}

func (r run) event(prompt string) harness.Event {
	return harness.Event{Kind: harness.PromptSubmitted, SessionID: "s1", Prompt: prompt, Transcript: r.transcript}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var start = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

// clock is a fake clock that Sleep advances.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time        { return c.now }
func (c *clock) Sleep(d time.Duration) { c.now = c.now.Add(d) }
func fakeHooks() harness.Hooks         { return (&harnesstest.Fake{}).Hooks() }
func poster(c *clock, wait time.Duration) Poster {
	return Poster{Hooks: fakeHooks(), Now: c.Now, Sleep: c.Sleep, Wait: wait}
}

func TestTheOperatorsPromptIsPostedVerbatimWithAValidSignature(t *testing.T) {
	var srv server
	r := newRun(t, srv.start(t))
	c := &clock{now: start}
	var queued []int
	h := Hook{Dir: r.dir, Post: poster(c, DefaultWait), Detach: func(dir string, seq int) error {
		if dir != r.dir {
			t.Errorf("detached for %s", dir)
		}
		queued = append(queued, seq)
		return nil
	}}
	const answer = "Yes: drop the column.\n  Keep the index. "
	must(t, h.Submitted(r.event(kickoff)))
	must(t, h.Submitted(r.event(answer)))
	if len(queued) != 1 || queued[0] != 2 || len(srv.requests) != 0 {
		t.Fatalf("queued %v and posted %d at once; want the operator's prompt, 2, queued", queued, len(srv.requests))
	}
	r.say(t, kickoff, answer)
	must(t, h.Post.Post(context.Background(), r.dir, queued[0]))
	if len(srv.requests) != 1 {
		t.Fatalf("posted %d requests, want 1", len(srv.requests))
	}
	got := srv.requests[0]
	if got.Op != "pane-input" || got.Body != answer || got.Key != "pane/a9/2" || got.Pane == nil || !got.Pane.Transcript {
		t.Errorf("request = %+v, want the prompt verbatim, found in the transcript", got)
	}
	key, _ := hex.DecodeString(r.pub)
	sig, _ := hex.DecodeString(got.Pane.Signature)
	if !ed25519.Verify(key, control.PaneMessage(attempt, answer), sig) {
		t.Error("the signature does not verify with the run's pane key")
	}
	if srv.auth[0] != "Bearer "+token {
		t.Errorf("credential %q, want the run's", srv.auth[0])
	}
	if _, err := os.Stat(pendingFile(r.dir, 2)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the posted prompt stayed queued")
	}
	log, _ := os.ReadFile(filepath.Join(r.dir, OutcomeLog))
	if !strings.Contains(string(log), `"updates":[41,42]`) || !strings.Contains(string(log), `"signed":true`) {
		t.Errorf("outcome log %q", log)
	}
}

func TestPromptsThatAreNotTheOperatorsArePostedNowhere(t *testing.T) {
	var srv server
	r := newRun(t, srv.start(t))
	c := &clock{now: start}
	h := Hook{Dir: r.dir, Post: poster(c, 0), Detach: func(string, int) error { t.Error("detached"); return nil }}
	must(t, h.Submitted(r.event(kickoff)))
	must(t, h.Submitted(r.event("   ")))
	must(t, h.Submitted(harness.Event{Kind: harness.ToolStarting, Prompt: "x"}))
	reviewer := r.event("Review this diff")
	reviewer.SessionID = "reviewer"
	must(t, h.Submitted(reviewer))
	plain := t.TempDir() // a run with no server session
	must(t, Hook{Dir: plain, Post: poster(c, 0)}.Submitted(harness.Event{Kind: harness.PromptSubmitted, Prompt: "Approved"}))
	if _, err := os.Stat(filepath.Join(plain, session.PromptLog)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a run with no server recorded its prompt")
	}
	if len(srv.requests) != 0 {
		t.Errorf("posted %+v", srv.requests)
	}
}

func TestAPostWaitsForTheTranscriptAndSaysWhenItNeverCame(t *testing.T) {
	var srv server
	r := newRun(t, srv.start(t))
	c := &clock{now: start}
	h := Hook{Dir: r.dir, Post: poster(c, 5*time.Second), Detach: func(string, int) error { return nil }}
	must(t, h.Submitted(r.event("Approved")))
	must(t, h.Submitted(r.event("Rejected")))
	late := h.Post
	slept := 0
	late.Sleep = func(d time.Duration) {
		c.Sleep(d)
		if slept++; slept == 2 {
			r.say(t, "Approved")
		}
	}
	must(t, late.Post(context.Background(), r.dir, 1))
	must(t, h.Post.Post(context.Background(), r.dir, 2))
	if len(srv.requests) != 2 || !srv.requests[0].Pane.Transcript || srv.requests[1].Pane.Transcript {
		t.Fatalf("requests = %+v; want the first found once written, the second missing", srv.requests)
	}
	if want := start.Add(2*time.Second + 5*time.Second); !c.now.Equal(want) {
		t.Errorf("clock at %s, want %s: the second post waits out its bound, and no longer", c.now, want)
	}
}

func TestAPromptThatCannotGoToTheBackgroundIsPostedAtOnce(t *testing.T) {
	var srv server
	r := newRun(t, srv.start(t))
	c := &clock{now: start}
	h := Hook{Dir: r.dir, Post: poster(c, time.Hour), Detach: func(string, int) error { return errors.New("no fork") }}
	must(t, h.Submitted(r.event("Approved")))
	if len(srv.requests) != 1 || srv.requests[0].Pane.Transcript || !c.now.Equal(start) {
		t.Errorf("requests %+v, clock %s; want one post at once, not waiting for the transcript", srv.requests, c.now)
	}
}

func TestARefusedPostLeavesTheQueueWithItsError(t *testing.T) {
	srv := server{statuses: []int{http.StatusConflict}}
	r := newRun(t, srv.start(t))
	c := &clock{now: start}
	h := Hook{Dir: r.dir, Post: poster(c, 0), Detach: func(string, int) error { return nil }}
	h.Post.RetryFor = time.Hour
	must(t, h.Submitted(r.event("Approved")))
	if err := h.Post.Post(context.Background(), r.dir, 1); err == nil {
		t.Fatal("a refused post succeeded")
	}
	if len(srv.requests) != 1 || !c.now.Equal(start) {
		t.Errorf("%d requests by %s; want a refusal tried once", len(srv.requests), c.now)
	}
	if _, err := os.Stat(pendingFile(r.dir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused prompt stayed queued: %v", err)
	}
	log, _ := os.ReadFile(filepath.Join(r.dir, OutcomeLog))
	if !strings.Contains(string(log), `"error":"refused"`) {
		t.Errorf("outcome log %q, want the refusal", log)
	}
	if err := h.Post.Post(context.Background(), r.dir, 9); err == nil {
		t.Error("posted a prompt that was never queued")
	}
}

func TestAPostRetriesAFailingServerWithTheSameRequest(t *testing.T) {
	srv := server{statuses: []int{http.StatusBadGateway, http.StatusBadGateway, http.StatusBadGateway}}
	r := newRun(t, srv.start(t))
	c := &clock{now: start}
	h := Hook{Dir: r.dir, Post: poster(c, 0), Detach: func(string, int) error { return nil }}
	h.Post.RetryFor = time.Minute
	must(t, h.Submitted(r.event("Approved")))
	must(t, h.Post.Post(context.Background(), r.dir, 1))
	// The client tries twice per call: 502, 502; then, 5s later, 502 and 200.
	if len(srv.requests) != 4 || !c.now.Equal(start.Add(firstRetry)) {
		t.Fatalf("%d requests by %s; want four over one wait", len(srv.requests), c.now)
	}
	for _, got := range srv.requests[1:] {
		if !reflect.DeepEqual(got, srv.requests[0]) {
			t.Errorf("a retry sent %+v, want %+v", got, srv.requests[0])
		}
	}
	if !srv.requests[0].Pane.At.Equal(start) {
		t.Errorf("submitted at %s, want %s", srv.requests[0].Pane.At, start)
	}
	if _, err := os.Stat(pendingFile(r.dir, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Error("the posted prompt stayed queued")
	}
}

func TestAPromptAnOutageLeftIsPostedWithTheNextPrompt(t *testing.T) {
	srv := server{statuses: []int{http.StatusServiceUnavailable, http.StatusServiceUnavailable}}
	r := newRun(t, srv.start(t))
	c := &clock{now: start}
	var detached []int
	h := Hook{Dir: r.dir, Post: poster(c, 0), Detach: func(_ string, seq int) error { detached = append(detached, seq); return nil }}
	must(t, h.Submitted(r.event("Approved")))
	if err := h.Post.Post(context.Background(), r.dir, 1); err == nil {
		t.Fatal("a post the server failed succeeded")
	}
	first := srv.requests[0]
	must(t, h.Submitted(r.event(kickoff)))
	if !reflect.DeepEqual(detached, []int{1, 1}) {
		t.Fatalf("detached %v; want the queued prompt started again by the next one", detached)
	}
	must(t, h.Post.Post(context.Background(), r.dir, 1))
	if got := srv.requests[len(srv.requests)-1]; !reflect.DeepEqual(got, first) {
		t.Errorf("the later post sent %+v, want the first request again, %+v", got, first)
	}
}

func TestAPostOfAPromptAnotherPostHoldsDoesNothing(t *testing.T) {
	var srv server
	r := newRun(t, srv.start(t))
	c := &clock{now: start}
	h := Hook{Dir: r.dir, Post: poster(c, 0), Detach: func(string, int) error { return nil }}
	must(t, h.Submitted(r.event("Approved")))
	f, err := os.Open(pendingFile(r.dir, 1))
	must(t, err)
	defer func() { _ = f.Close() }()
	must(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX))
	must(t, h.Post.Post(context.Background(), r.dir, 1))
	if len(srv.requests) != 0 {
		t.Errorf("posted %d requests while another post held the prompt", len(srv.requests))
	}
}

func TestAPostWithNoCredentialFails(t *testing.T) {
	var srv server
	r := newRun(t, srv.start(t))
	c := &clock{now: start}
	h := Hook{Dir: r.dir, Post: poster(c, 0), Detach: func(string, int) error { return nil }}
	must(t, h.Submitted(r.event("Approved")))
	must(t, os.Remove(filepath.Join(r.dir, tokenFile)))
	if err := h.Post.Post(context.Background(), r.dir, 1); err == nil {
		t.Error("posted with no credential")
	}
	if _, err := os.Stat(pendingFile(r.dir, 1)); err != nil {
		t.Errorf("the unposted prompt left the queue: %v", err)
	}
}
