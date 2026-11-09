// Package harnesstest holds what tests of harness adapters and their callers
// share: the contract every adapter passes, and a fake adapter for the core's
// tests. Only tests import it.
package harnesstest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// New builds the adapter under test on env. An adapter that reads more than
// env offers (an app-server, say) replaces those reads so they fail at once.
type New func(env harness.Env) harness.Adapter

// Fixtures are what an adapter's harness sends, for the contract to check
// the adapter reads it.
type Fixtures struct {
	// Events are hook events as the harness sends them, with the kind each
	// means; they should cover every kind the adapter reads.
	Events []Event
}

// Event is one hook event fixture: Raw, the harness's JSON, is the event
// called Name, of Kind.
type Event struct {
	Raw  string
	Name string
	Kind harness.EventKind
}

// Contract checks what the core relies on from every adapter.
func Contract(t *testing.T, build New, fx Fixtures) {
	t.Helper()
	t.Run("name", func(t *testing.T) { checkName(t, build) })
	t.Run("launch line", func(t *testing.T) { checkArgs(t, build) })
	t.Run("release", func(t *testing.T) { checkRelease(t, build) })
	t.Run("observe unknown", func(t *testing.T) { checkObserve(t, build) })
	t.Run("check kickoff", func(t *testing.T) { checkKickoff(t, build) })
	t.Run("hook events", func(t *testing.T) { checkEvents(t, build, fx) })
	t.Run("hook replies", func(t *testing.T) { checkReplies(t, build) })
	t.Run("hook entries", func(t *testing.T) { checkEntries(t, build) })
	t.Run("setup", func(t *testing.T) { checkSetup(t, build) })
}

func env(t *testing.T, exec session.Runner) harness.Env {
	return harness.Env{Exec: exec, Home: t.TempDir(), Getenv: func(string) string { return "" }, Now: time.Now}
}

// failing is a host whose every CLI fails to run.
func failing(name string, _ ...string) (string, error) {
	return "", errors.New("exec: " + name + ": not found")
}

func checkName(t *testing.T, build New) {
	a := build(env(t, failing))
	if a.Name() == "" || a.Name() != strings.ToLower(a.Name()) || strings.ContainsAny(a.Name(), " \t") {
		t.Errorf("name %q: want one lowercase word", a.Name())
	}
	set := harness.NewAdapters(a)
	if got, ok := set.Get(a.Name()); !ok || got.Name() != a.Name() {
		t.Errorf("the adapters do not find %q by name", a.Name())
	}
}

// checkArgs: whether joined or not, the arguments end in the kickoff, as one
// shell word after --, and the environment ends ready for the next command.
func checkArgs(t *testing.T, build New) {
	a := build(env(t, failing))
	l := a.Session("r-7")
	for _, joined := range []bool{false, true} {
		kickoff := "do it's thing"
		args := l.Args(harness.Launch{Line: "harness", Dir: "/w", RunDir: "/r", Kickoff: kickoff, Unattended: true, Joined: joined})
		if !strings.HasSuffix(args, " -- "+hosts.ShellWord(kickoff)) {
			t.Errorf("joined %v: args %q do not end in the kickoff", joined, args)
		}
		if e := l.Env(joined); !strings.HasSuffix(e, " && ") {
			t.Errorf("joined %v: env %q does not end in \" && \"", joined, e)
		}
	}
}

// checkRelease: what Trust writes for one session alone, Release deletes,
// leaving no file named after the session; Release with no folder, or with
// nothing to delete, succeeds.
func checkRelease(t *testing.T, build New) {
	e := env(t, failing)
	a := build(e)
	name := "contract-r-7"
	if err := a.Session(name).Release(""); err != nil {
		t.Fatalf("release with nothing to delete: %v", err)
	}
	dir := filepath.Join(e.Home, "clone", "wt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, joined := range []bool{false, true} {
		l := a.Session(name)
		if err := l.Trust(dir, filepath.Dir(dir), joined); err != nil {
			t.Fatalf("joined %v: trust: %v", joined, err)
		}
		if err := l.Release(dir); err != nil {
			t.Fatalf("joined %v: release: %v", joined, err)
		}
		if left := named(t, e.Home, name); len(left) > 0 {
			t.Errorf("joined %v: release left %v", joined, left)
		}
	}
}

// named lists the files under root whose name holds name.
func named(t *testing.T, root, name string) []string {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(d.Name(), name) {
			out = append(out, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// checkObserve: what an adapter cannot read is unknown, never a state or a
// sign-out, on a host that runs the harness or one that does not.
func checkObserve(t *testing.T, build New) {
	e := env(t, failing)
	a := build(e)
	run := session.Run{Dir: t.TempDir(), Record: session.Record{Harness: a.Name(), Name: "r-7"}}
	runs := map[string]session.Run{"r-7": run}
	host := hosts.Host{Name: "h", Harnesses: []string{a.Name()}, Bin: map[string]string{a.Name(): "~/bin/" + a.Name()}}
	snap := a.Observe(context.Background(), host, runs)
	if snap.SignedOut {
		t.Error("a sign-in check that cannot run says signed out")
	}
	if st, ok := snap.Sessions["r-7"]; ok {
		t.Errorf("an unreadable session has state %+v", st)
	}
	other := a.Observe(context.Background(), hosts.Host{Name: "h"}, runs)
	if _, ok := other.Sessions["r-7"]; other.SignedOut || ok {
		t.Errorf("a host that does not run %s: %+v", a.Name(), other)
	}
}

func checkKickoff(t *testing.T, build New) {
	a := build(env(t, failing))
	marker := "/tmp/engine-contract-marker"
	k := a.CheckKickoff(marker)
	if !strings.Contains(k, marker) || !strings.Contains(k, "tmux kill-window") {
		t.Errorf("kickoff %q: want the marker and the window's own close", k)
	}
}

// checkEvents: every fixture reads as its kind and keeps the harness's own
// name; JSON that is not an event is an error, never an event.
func checkEvents(t *testing.T, build New, fx Fixtures) {
	h := build(env(t, failing)).Hooks()
	if len(fx.Events) == 0 {
		t.Fatal("no event fixtures")
	}
	for _, f := range fx.Events {
		ev, err := h.Event([]byte(f.Raw))
		if err != nil || ev.Kind != f.Kind || ev.Name != f.Name {
			t.Errorf("%s: read as %+v, %v; want kind %s", f.Name, ev, err, f.Kind)
		}
	}
	if _, err := h.Event([]byte("not json")); err == nil {
		t.Error("JSON that is not an event read as one")
	}
	if ev, err := h.Event([]byte(`{"hook_event_name":"NoSuchEvent"}`)); err != nil || ev.Kind != harness.Other {
		t.Errorf("an unknown event: %+v, %v; want kind %s", ev, err, harness.Other)
	}
}

// checkReplies: a harness whose unattended requests are denied writes a
// denial that is JSON and carries the message; Signalled, on a run that is
// none of its business, neither fails nor starts anything.
func checkReplies(t *testing.T, build New) {
	h := build(env(t, failing)).Hooks()
	if p := h.Unattended(); p != harness.Deny && p != harness.LeaveToReview {
		t.Fatalf("unattended policy %v", p)
	}
	if h.Unattended() == harness.Deny {
		var out strings.Builder
		const message = "contract: denied"
		if err := h.Deny(&out, message); err != nil || !json.Valid([]byte(out.String())) || !strings.Contains(out.String(), message) {
			t.Errorf("deny wrote %q, %v; want JSON with the message", out.String(), err)
		}
	}
	var errs strings.Builder
	detach := func(entry string, args ...string) error {
		t.Errorf("a run outside engine started %s %v", entry, args)
		return nil
	}
	for _, kind := range []harness.EventKind{harness.PromptSubmitted, harness.ToolFinished, harness.TurnStopped} {
		h.Signalled(harness.HookContext{Run: harness.HookRun{Dir: t.TempDir()}, Err: &errs, Detach: detach}, harness.Event{Kind: kind, SessionID: "s"})
	}
	if errs.Len() != 0 {
		t.Errorf("signalled reported %q", errs.String())
	}
}

// checkEntries: an adapter's own entries are one word each, never a core
// entry's name, and run.
func checkEntries(t *testing.T, build New) {
	for name, run := range build(env(t, failing)).Hooks().Entries() {
		if name == "" || strings.ContainsAny(name, " \t") || slices.Contains(harness.CoreEntries, name) {
			t.Errorf("entry %q: want one word that is not a core entry", name)
		}
		if run == nil {
			t.Errorf("entry %q runs nothing", name)
		}
	}
}
