package cli

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/pane"
)

// promptEntry is the prompt hook: `engine hook prompt --harness <name>` on a
// submitted prompt, and, with `<run-dir> <seq>` after it, the background
// post of the operator's prompt it queued.
const promptEntry = "prompt"

// promptHook records the session's submitted prompt in its run, and starts
// the post of the operator's in the background. It never blocks the prompt:
// a failure exits 1.
func promptHook(env Env, name string, hooks harness.Hooks, ev harness.Event) int {
	run := findHookRun(env, name, hooks, ev)
	if run.Dir == "" || otherHarness(run.Dir, name) {
		return 0
	}
	h := pane.Hook{Dir: run.Dir, Post: poster(env, hooks, 0, 0)}
	if detach := detachHook(env, name); detach != nil {
		h.Detach = func(dir string, seq int) error { return detach(promptEntry, dir, strconv.Itoa(seq)) }
	}
	if err := h.Submitted(ev); err != nil {
		fmt.Fprintln(env.Err, "engine hook prompt:", err)
		return 1
	}
	return 0
}

// postPrompt is the background post: args are the run directory and the
// prompt's sequence number.
func postPrompt(env Env, hooks harness.Hooks, args []string) int {
	seq, err := strconv.Atoi(args[len(args)-1])
	if len(args) != 2 || err != nil {
		fmt.Fprintf(env.Err, "engine hook prompt: want <run-dir> <seq>, got %q\n", args)
		return 1
	}
	if err := poster(env, hooks, pane.DefaultWait, pane.DefaultRetry).Post(context.Background(), args[0], seq); err != nil {
		fmt.Fprintln(env.Err, "engine hook prompt:", err)
		return 1
	}
	return 0
}

// poster posts with env's clock, waiting up to wait for the transcript and
// retrying a failing server for retry.
func poster(env Env, hooks harness.Hooks, wait, retry time.Duration) pane.Poster {
	return pane.Poster{Hooks: hooks, Now: env.Now, Sleep: env.Sleep, Wait: wait, RetryFor: retry}
}
