package run

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/tracker"
)

// How the loop reacts when GitHub refuses the host's sign-in, and when it
// only rate-limits it.

func TestLoopStopsWhenGitHubRefusesTheSignIn(t *testing.T) {
	w := finishedFeature(t, behaviour{waitAt: 5})
	refused := &tracker.AuthError{Account: "some-dev", Detail: "gh: Bad credentials (HTTP 401)"}
	reads := 0
	w.readFail = func(*world) error {
		reads++
		return refused
	}
	err := Loop(w, ref(1), false, w.sleep)
	if !errors.Is(err, refused) || reads != 1 || !w.now.Equal(t0) {
		t.Fatalf("err %v after %d reads and %s", err, reads, w.now.Sub(t0))
	}
	if len(w.log) != 0 || len(w.starts) != 0 {
		t.Errorf("log %q, starts %v", w.log, w.starts)
	}
}

func TestLoopRetriesARateLimit(t *testing.T) {
	w := finishedFeature(t, behaviour{waitAt: 5})
	limited := 0
	w.readFail = func(*world) error {
		if limited < 2 {
			limited++
			return errors.New("gh: API rate limit exceeded for user ID 1234. (HTTP 403)")
		}
		return nil
	}
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatal(err)
	}
	if !w.closed || !slices.ContainsFunc(w.log, func(l string) bool { return strings.HasPrefix(l, "pass: gh: API rate limit") }) {
		t.Errorf("closed %v\n%s", w.closed, strings.Join(w.log, "\n"))
	}
}

func TestWindDownStopsWhenGitHubRefusesTheSignIn(t *testing.T) {
	w := finishedFeature(t, behaviour{waitAt: 30})
	w.readFail = func(w *world) error {
		if w.closed {
			return &tracker.AuthError{Detail: "gh: Sorry. Your account was suspended. (HTTP 403)"}
		}
		return nil
	}
	if err := Loop(w, ref(1), false, w.sleep); err != nil {
		t.Fatal(err)
	}
	if len(w.killed) != 0 || !strings.Contains(w.log[len(w.log)-1], "wind down: GitHub refuses") {
		t.Errorf("killed %v\n%s", w.killed, strings.Join(w.log, "\n"))
	}
}
