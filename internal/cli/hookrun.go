package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/registry"
	"github.com/dinoderek/engine/internal/hook"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// adapters are the harness adapters env's commands run with.
func adapters(env Env) harness.Adapters {
	if env.Harnesses != nil {
		return env.Harnesses
	}
	return registry.New(harness.Env{Exec: env.Local, Home: env.Home, Getenv: env.Getenv, Now: env.Now})
}

// runHook runs `engine hook <entry> --harness <name> [args]`: a core entry
// on the named harness's event, or one of that harness's own entries. A
// command line it cannot run fails open (exit 1, which blocks nothing) rather
// than 2, which a harness reads as "block": a plugin can be newer than the
// engine binary it calls.
func runHook(args []string, env Env) int {
	if len(args) < 3 || args[1] != "--harness" || args[2] == "" {
		fmt.Fprintf(env.Err, "engine hook: want engine hook <entry> --harness <name> [args], got %q\n", strings.Join(args, " "))
		return 1
	}
	entry, name, rest := args[0], args[2], args[3:]
	set := adapters(env)
	a, ok := set.Get(name)
	if !ok {
		fmt.Fprintf(env.Err, "engine hook: unknown harness %q; want %s\n", name, strings.Join(set.Names(), " or "))
		return 1
	}
	hooks := a.Hooks()
	if slices.Contains(harness.CoreEntries, entry) && len(rest) == 0 {
		return coreHook(entry, name, hooks, env)
	}
	if run, ok := hooks.Entries()[entry]; ok {
		if err := run(harness.Call{In: env.In, Out: env.Out, Err: env.Err, RunDir: env.Getenv(session.RunDirVar), Args: rest}); err != nil {
			fmt.Fprintf(env.Err, "engine hook %s: %v\n", entry, err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(env.Err, "engine hook: %s has no hook %q (want %s); update engine (go install ./cmd/engine)\n",
		name, strings.Join(args, " "), strings.Join(entryNames(hooks), ", "))
	return 1
}

// entryNames are the entries a harness's hooks offer, the core's first.
func entryNames(hooks harness.Hooks) []string {
	var own []string
	for name := range hooks.Entries() {
		own = append(own, name)
	}
	sort.Strings(own)
	return append(slices.Clone(harness.CoreEntries), own...)
}

// coreHook reads the harness's event from env.In and runs the core entry on
// it.
func coreHook(entry, name string, hooks harness.Hooks, env Env) int {
	raw, err := io.ReadAll(env.In)
	var ev harness.Event
	if err == nil {
		ev, err = hooks.Event(raw)
	}
	if err != nil {
		fmt.Fprintf(env.Err, "engine hook: unreadable %s event: %v\n", entry, err)
		return 1
	}
	switch entry {
	case "pre-tool-use":
		return preToolUse(env, name, hooks, ev)
	case "permission":
		return permissionHook(env, name, hooks, ev)
	default:
		return stateHook(env, name, hooks, ev)
	}
}

// preToolUse is the merge guard: no unsafe merge anywhere, and no merge at
// all in an engine session.
func preToolUse(env Env, name string, hooks harness.Hooks, ev harness.Event) int {
	if code := hook.PreToolUse(ev, env.Err); code != 0 || !hook.IsMerge(ev.Command) {
		return code
	}
	// Every engine session is a server session, and never merges.
	if findHookRun(env, name, hooks, ev).Dir != "" || env.Getenv("ENGINE_SERVER_URL") != "" {
		fmt.Fprintln(env.Err, "engine merge guard: "+serverMergeRefusal)
		return 2
	}
	return 0
}

// findHookRun works out the run a hook event is for. With ENGINE_RUN_DIR set
// it takes the run from the environment the host's spawn exported. Without
// it, which is how a session that joined a daemon sees things, a harness
// whose hooks find their run takes the run of that harness whose worktree
// holds the event's cwd, and reads its mode from the run's record: a session
// of another harness opened by hand in that worktree is not the run's
// session. Where the cwd is in no such worktree the environment's own values
// stand.
func findHookRun(env Env, name string, hooks harness.Hooks, ev harness.Event) harness.HookRun {
	run := harness.HookRun{Dir: env.Getenv(session.RunDirVar), Unattended: env.Getenv(session.UnattendedVar) == "1"}
	if run.Dir != "" || !hooks.FindsRun() {
		return run
	}
	if dir, rec, ok := session.FindRun(env.Home, ev.Cwd); ok && rec.Harness == name {
		run.Dir, run.Joined, run.Repo = dir, true, rec.Repo
		run.Unattended = run.Unattended || rec.Mode == hostops.HandsOff
	}
	return run
}

// permissionHook runs the permission hook with the harness's unattended
// policy, logging to the run directory of the session's run, when there is
// one.
func permissionHook(env Env, name string, hooks harness.Hooks, ev harness.Event) int {
	run := findHookRun(env, name, hooks, ev)
	h := hook.Permission{Out: env.Out, Err: env.Err, Unattended: run.Unattended, Policy: hooks.Unattended(), Deny: hooks.Deny, Now: env.Now()}
	if run.Dir == "" {
		return h.Handle(ev)
	}
	f, err := os.OpenFile(filepath.Join(run.Dir, session.PermissionLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(env.Err, "engine hook: open the permission log:", err)
		return h.Handle(ev)
	}
	h.Log = f
	code := h.Handle(ev)
	if err := f.Close(); err != nil {
		fmt.Fprintln(env.Err, "engine hook: close the permission log:", err)
	}
	return code
}

// stateHook records the session's state in the run directory of the
// session's run, then tells the adapter. Outside an engine run there is
// none, and it does nothing. It records only a run of its own harness: a
// reviewer of another harness that a session starts inherits the session's
// run directory, and its turns are not that run's. A run whose record names
// no harness, or has none yet, is taken as its own.
func stateHook(env Env, name string, hooks harness.Hooks, ev harness.Event) int {
	run := findHookRun(env, name, hooks, ev)
	if run.Dir == "" {
		return 0
	}
	if rec, err := session.ReadRecord(run.Dir); err == nil && rec.Harness != "" && rec.Harness != name {
		return 0
	}
	h := hook.State{Err: env.Err, Unattended: run.Unattended}
	h.Write = func(sig hook.Signal) error {
		if err := session.WriteHarnessState(run.Dir, session.HarnessState{State: sig.State, Event: sig.Event, At: env.Now(), SessionID: sig.SessionID, TurnID: sig.TurnID}); err != nil {
			return err
		}
		hooks.Signalled(harness.HookContext{Run: run, Err: env.Err, Detach: detachHook(env, name)}, ev)
		return nil
	}
	return h.Handle(ev)
}

// detachHook starts `engine hook <entry> --harness <name> <args>` in the
// background, or is nil when env cannot.
func detachHook(env Env, name string) func(string, ...string) error {
	if env.Detach == nil {
		return nil
	}
	return func(entry string, args ...string) error {
		return env.Detach(append([]string{"hook", entry, "--harness", name}, args...)...)
	}
}

// detachSelf starts this binary with args in its own session, with no
// standard streams, and does not wait for it.
func detachSelf(args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
