// Package harnesstest holds what tests of harness adapters and their callers
// share: the contract every adapter passes, and a fake adapter for the core's
// tests. Only tests import it.
package harnesstest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// Contract checks what the core relies on from every adapter.
func Contract(t *testing.T, build New) {
	t.Helper()
	t.Run("name", func(t *testing.T) { checkName(t, build) })
	t.Run("launch line", func(t *testing.T) { checkArgs(t, build) })
	t.Run("release", func(t *testing.T) { checkRelease(t, build) })
	t.Run("observe unknown", func(t *testing.T) { checkObserve(t, build) })
	t.Run("check kickoff", func(t *testing.T) { checkKickoff(t, build) })
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
