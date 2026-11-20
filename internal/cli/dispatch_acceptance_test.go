package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/controlrun"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// The acceptance tests of dispatch, tmux, early deaths and readiness: the
// real coordinator and control server against the fake fleet in
// opsfleet_test.go.

// current is the task's current attempt.
func (f *opsFleet) current(task control.Item) control.Attempt {
	f.t.Helper()
	id := f.get(task.ID).Attempt
	for _, at := range f.attempts(task.ID) {
		if at.ID == id {
			return at
		}
	}
	f.t.Fatalf("%s has no attempt %q", task.ID, id)
	return control.Attempt{}
}

// noticesOn are the bodies of the item's notes to the operator.
func (f *opsFleet) noticesOn(id string) []string {
	var out []string
	for _, u := range f.updates(id) {
		if u.Recipient == "operator" {
			out = append(out, u.Body)
		}
	}
	return out
}

// containing are the bodies that contain part.
func containing(bodies []string, part string) []string {
	var out []string
	for _, b := range bodies {
		if strings.Contains(b, part) {
			out = append(out, b)
		}
	}
	return out
}

// wantNothingLeft fails unless h holds nothing of the task's launch: no
// window of its name, no run directory, no worktree and no branch.
func wantNothingLeft(t *testing.T, h *opsHost, task control.Item, at control.Attempt) {
	t.Helper()
	name := opsWindow(task)
	if w := h.tmux.window(name); w != nil {
		t.Errorf("%s keeps the window %s (%s)", h.name, name, w.id)
	}
	dir, err := session.RunDir(h.home, at.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("%s keeps the run directory %s: %v", h.name, dir, err)
	}
	if _, err := os.Stat(h.clone() + "-worktrees/task-" + name); !os.IsNotExist(err) {
		t.Errorf("%s keeps the worktree of %s: %v", h.name, name, err)
	}
	if got := h.git(h.clone(), "branch", "--list", "task/"+name); got != "" {
		t.Errorf("%s keeps the branch %q", h.name, got)
	}
}

// breakTrust makes Claude Code's config a directory on h, so trusting a
// worktree fails after the spawn made it; mend puts the config back.
func breakTrust(t *testing.T, h *opsHost) (mend func()) {
	t.Helper()
	config := filepath.Join(h.home, ".claude.json")
	kept, err := os.ReadFile(config)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err = os.Remove(config); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err = os.MkdirAll(config, 0o755); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.RemoveAll(config); err != nil {
			t.Fatal(err)
		}
		if kept != nil {
			writeFile(t, config, string(kept))
		}
	}
}

// A launch that fails part-way, after its worktree and branch, and again
// after its run files and window, removes what it made each time, so the
// next pass relaunches the same attempt and it runs; each distinct error
// posts one notice however often it repeats. A dispatch the host never
// launched is ended by `engine attempt reconcile` with positional evidence,
// and the task's next attempt runs.
func TestAFailedLaunchLeavesNothingAndRelaunches(t *testing.T) {
	f := newOpsFleet(t, "mac")
	mac := f.hosts["mac"]
	f.pass() // readiness
	task := f.task("Add a flag")
	mend := breakTrust(t, mac)
	f.passes(3)
	stuck := f.current(task)
	if stuck.State != "dispatching" {
		t.Fatalf("a failed launch left the attempt %s", stuck.State)
	}
	wantNothingLeft(t, mac, task, stuck)
	unresolved := containing(f.noticesOn(task.ID), "unresolved outcome")
	if len(unresolved) != 1 || !strings.Contains(unresolved[0], "trust") {
		t.Fatalf("three failed launches posted %q; want one notice of the trust failure", unresolved)
	}
	mend()
	mac.tmux.mu.Lock()
	mac.tmux.respawnFails = 2
	mac.tmux.mu.Unlock()
	f.pass()
	wantNothingLeft(t, mac, task, stuck)
	unresolved = containing(f.noticesOn(task.ID), "unresolved outcome")
	if len(unresolved) != 2 || !strings.Contains(unresolved[1], "respawn-pane failed") {
		t.Fatalf("a second error posted %q; want a second notice of it", unresolved)
	}
	f.pass()
	if at := f.current(task); at.ID != stuck.ID || at.State != "running" || mac.tmux.window(opsWindow(task)) == nil {
		t.Fatalf("the next pass did not relaunch %s: %+v", stuck.ID, at)
	}

	other := f.task("Add a switch")
	f.task("Accept", task.ID, other.ID) // a plan of several tasks has one that needs them all
	mend = breakTrust(t, mac)
	f.passes(2)
	lost := f.current(other)
	if lost.State != "dispatching" {
		t.Fatalf("the second task's attempt is %s", lost.State)
	}
	if code, out, errOut := f.engine("attempt", "reconcile", lost.ID, "the host shows no window and no run record"); code != 0 {
		t.Fatalf("reconcile: exit %d: %s%s", code, errOut, out)
	}
	ended := f.attempts(other.ID)[0]
	if ended.State != "ended" || ended.Reason != control.EndVanished || f.get(other.ID).State != "todo" {
		t.Fatalf("the reconciled attempt %+v, task %s", ended, f.get(other.ID).State)
	}
	mend()
	f.pass()
	if next := f.current(other); next.ID == lost.ID || next.State != "running" {
		t.Fatalf("no new attempt ran after the reconcile: %+v", next)
	}
}

// The tmux every engine command drives runs tmux from the home directory,
// whatever directory engine runs in. A host whose older tmux server runs in
// a directory since removed is unready: placement puts the task on the
// other host, and one note names the server and the command that restarts
// it.
func TestTmuxRunsFromHomeAndADeadServerDirectoryIsSkipped(t *testing.T) {
	home, elsewhere := t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "tmux-dirs")
	t.Setenv("HOME", home)
	t.Setenv("FAKE_TMUX_LOG", log)
	t.Chdir(elsewhere)
	tm := System().Tmux
	tm.Bin, tm.Awake = filepath.Join(t.TempDir(), "tmux"), ""
	writeExec(t, tm.Bin, "#!/bin/sh\npwd -P >> \"$FAKE_TMUX_LOG\"\nexit 1\n")
	if _, err := tm.Windows(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	want, _ := filepath.EvalSymlinks(home)
	if err != nil || strings.TrimSpace(string(data)) != want {
		t.Fatalf("tmux ran in %q (%v); want the home directory %s", data, err, want)
	}

	f := newOpsFleet(t, "mac", "box")
	f.pass() // readiness
	mac := f.hosts["mac"]
	gone := filepath.Join(t.TempDir(), "removed-worktree")
	mac.tmux.mu.Lock()
	mac.tmux.up, mac.tmux.cwd = true, gone
	mac.tmux.mu.Unlock()
	task := f.task("Add a flag")
	f.passes(2)
	if at := f.current(task); at.Host != "box" || at.State != "running" {
		t.Fatalf("the task went to %s (%s); want box", at.Host, at.State)
	}
	if live := mac.tmux.live(); len(live) != 0 {
		t.Errorf("mac opened %q", live)
	}
	_, inbox, _ := f.engine("inbox", "--operator")
	if n := strings.Count(inbox, "Host mac is unready: tmux server 4100 runs in "+gone); n != 1 {
		t.Errorf("the operator's inbox names the dead server %d time(s):\n%s", n, inbox)
	}
	wantIn(t, inbox, "tmux kill-server")
}

// endEarly ends the task's window on h before its session wrote anything,
// with tail in its pane log, and runs the pass that sees it.
func (f *opsFleet) endEarly(h *opsHost, task control.Item, tail string) control.Attempt {
	f.t.Helper()
	at := f.current(task)
	name := opsWindow(task)
	h.tmux.end(name, 127, tail)
	if w := h.tmux.window(name); w != nil && w.log != "" {
		if err := os.Chtimes(w.log, f.clock.now(), f.clock.now()); err != nil {
			f.t.Fatal(err)
		}
	}
	f.pass()
	for _, a := range f.attempts(task.ID) {
		if a.ID == at.ID {
			return a
		}
	}
	return control.Attempt{}
}

// A window that dies before its session claims the task ends the attempt
// as an early death and the task is dispatched again; a second early death
// fails it, with both pane log tails in the notice.
func TestAnEarlyDeathRetriesOnceThenFails(t *testing.T) {
	f := newOpsFleet(t, "mac")
	mac := f.hosts["mac"]
	f.pass() // readiness
	task := f.task("Add a flag")
	f.pass()
	first := f.endEarly(mac, task, "tmux: server exited unexpectedly\n")
	if first.Reason != control.EndEarlyDeath || f.get(task.ID).State != "todo" {
		t.Fatalf("the first early death: %+v, task %s", first, f.get(task.ID).State)
	}
	f.clock.add(time.Minute) // the retry's run is the newer
	f.pass()
	second := f.current(task)
	if second.ID == first.ID || second.State != "running" || mac.tmux.window(opsWindow(task)) == nil || mac.tmux.window(opsWindow(task)).dead {
		t.Fatalf("the task was not dispatched again: %+v", second)
	}
	ended := f.endEarly(mac, task, "claude: command not found\n")
	if ended.Reason != control.EndFailed || !ended.Early || f.get(task.ID).State != "failed" {
		t.Fatalf("the second early death: %+v, task %s", ended, f.get(task.ID).State)
	}
	notices := f.noticesOn(task.ID)
	if len(containing(notices, "dispatched again once")) != 1 {
		t.Errorf("the first death's notice: %q", notices)
	}
	last := notices[len(notices)-1]
	wantIn(t, last, "second time", "tmux: server exited unexpectedly", "claude: command not found")
	f.passes(2)
	if n := len(f.attempts(task.ID)); n != 2 {
		t.Errorf("the failed task has %d attempts; want 2", n)
	}
}

// pushMain pushes a new commit to the origin's main, as a merge elsewhere
// does.
func (f *opsFleet) pushMain(message string) {
	f.t.Helper()
	h := f.hosts[f.names[0]]
	dir := filepath.Join(f.t.TempDir(), "elsewhere")
	h.git("", "clone", "--quiet", f.github.origin, dir)
	writeFile(f.t, filepath.Join(dir, "CHANGES"), message+"\n")
	h.git(dir, "add", "CHANGES")
	h.git(dir, "commit", "--quiet", "-m", message)
	h.git(dir, "push", "--quiet", "origin", "main")
}

// probes counts the readiness probe windows h opened.
func probes(h *opsHost) int {
	h.tmux.mu.Lock()
	defer h.tmux.mu.Unlock()
	n := 0
	for _, c := range h.tmux.calls {
		if (strings.HasPrefix(c, "new-session") || strings.HasPrefix(c, "new-window")) && strings.Contains(c, "-n engine-ready-") {
			n++
		}
	}
	return n
}

// A host whose ./dev doctor fails is skipped for the repo, with one note
// naming the host, the check and its output, however many passes see it;
// a fixed doctor is not probed again on the same head, and a new head on
// main probes it again. A session starts with the host's PATH prefix.
func TestAFailingReadinessProbeSkipsTheHostUntilANewHead(t *testing.T) {
	f := newOpsFleet(t, "mac", "box")
	mac, box := f.hosts["mac"], f.hosts["box"]
	doctor := filepath.Join(mac.home, "doctor.fail")
	writeFile(t, doctor, "go: go1.27.2 is not installed; run ./dev doctor --install\n")
	f.pass() // readiness
	task := f.task("Add a flag")
	f.passes(3)
	if at := f.current(task); at.Host != "box" || at.State != "running" {
		t.Fatalf("the task went to %s (%s); want box", at.Host, at.State)
	}
	notes := containing(f.noticesOn(f.repo.ID), "Host mac is unready")
	if len(notes) != 1 {
		t.Fatalf("readiness notes %q; want one", notes)
	}
	wantIn(t, notes[0], "Host mac is unready for fleet/app: `./dev doctor` failed", "go: go1.27.2 is not installed")
	if len(containing(f.noticesOn(f.repo.ID), "Host box")) != 0 {
		t.Errorf("box, which is ready, has a note")
	}
	export := hosts.PathExport([]string{filepath.Join(box.home, "fakebin")})
	if w := box.tmux.window(opsWindow(task)); w == nil || !strings.Contains(w.command, export) {
		t.Errorf("the session's command lacks the PATH prefix %q: %+v", export, w)
	}

	if err := os.Remove(doctor); err != nil {
		t.Fatal(err)
	}
	before := probes(mac)
	f.clock.add(controlrun.ReadyEvery + time.Minute)
	f.passes(2)
	if probes(mac) != before {
		t.Errorf("mac probed again on the same head")
	}
	_, status, _ := f.engine("host", "status", "--all")
	wantRow(t, status, "mac", "yes", "2 of 2 (heavy 2 of 2)", "-", "unready: fleet/app: ./dev doctor failed (the readiness note has the output)")
	f.pushMain("Change elsewhere")
	f.clock.add(controlrun.ReadyEvery + time.Minute)
	f.passes(2)
	if probes(mac) != before+1 {
		t.Fatalf("mac was probed %d time(s) on the new head; want 1", probes(mac)-before)
	}
	_, status, _ = f.engine("host", "status", "--all")
	wantRow(t, status, "mac", "yes", "2 of 2 (heavy 2 of 2)", "-", "ready")
}
