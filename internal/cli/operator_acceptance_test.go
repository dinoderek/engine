package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/session"
)

// The acceptance tests of usage limits, stopping a task, pane answers and
// the fleet view, through Run against the operations fleet (opsfleet_test.go).

// runVars are the variables a session's commands run with: its run's
// server endpoint, credential, attempt and run directory.
func (s *opsSession) runVars() map[string]string {
	s.f.t.Helper()
	data, err := os.ReadFile(filepath.Join(s.dir, "server-session.json"))
	if err != nil {
		s.f.t.Fatal(err)
	}
	var conn struct{ URL, Attempt string }
	if err = json.Unmarshal(data, &conn); err != nil {
		s.f.t.Fatal(err)
	}
	return map[string]string{"ENGINE_SERVER_URL": conn.URL, "ENGINE_SERVER_TOKEN_FILE": filepath.Join(s.dir, "server-token"),
		"ENGINE_ATTEMPT_ID": conn.Attempt, session.RunDirVar: s.dir}
}

// hookWith runs one of the session's harness hooks on the event in, as
// Claude Code runs it in the session's pane; a hook that starts a
// background entry runs it to its end first.
func (s *opsSession) hookWith(in string, args ...string) (int, string) {
	vars := s.runVars()
	var out, errOut bytes.Buffer
	env := s.h.env(strings.NewReader(in), &out, &errOut)
	env.Getenv = func(k string) string { return vars[k] }
	env.Detach = func(bg ...string) error {
		inner := env
		inner.In = strings.NewReader("")
		if code := Run(bg, inner); code != 0 {
			return fmt.Errorf("engine %s exited %d", strings.Join(bg, " "), code)
		}
		return nil
	}
	return Run(args, env), errOut.String() + out.String()
}

// mustHook runs hookWith and fails the test unless the hook exits 0.
func (s *opsSession) mustHook(in string, args ...string) {
	s.f.t.Helper()
	if code, out := s.hookWith(in, args...); code != 0 {
		s.f.t.Fatalf("%s: engine %s: exit %d: %s", s.task, strings.Join(args, " "), code, out)
	}
}

// mustOperator runs an engine command as the operator and fails the test
// unless it exits 0.
func (f *opsFleet) mustOperator(args ...string) string {
	f.t.Helper()
	code, out, errOut := f.engine(args...)
	if code != 0 {
		f.t.Fatalf("engine %s: exit %d: %s%s", strings.Join(args, " "), code, errOut, out)
	}
	return out
}

// headOf is the commit the session's worktree has checked out.
func (s *opsSession) headOf() string { return s.h.git(s.worktree, "rev-parse", "HEAD") }

// branchAt is the commit the branch is at in the host's clone of the repo,
// or "" when the clone has no such branch.
func (h *opsHost) branchAt(branch string) string {
	clone := filepath.Join(h.home, ".local/share/engine/control-repos", opsSlug)
	out, err := h.run(clone, nil, "git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// resumesOn fails unless the task's current session runs on branch at head:
// its worktree checked out there and its kickoff naming the commit.
func resumesOn(t *testing.T, s *opsSession, branch, head string) {
	t.Helper()
	if s.branch != branch || s.headOf() != head {
		t.Errorf("the session works on %s at %s; want the task's own %s at %s", s.branch, s.headOf(), branch, head)
	}
	if w := s.h.tmux.window(s.name); w == nil || !strings.Contains(w.command, " branch "+branch+" is at "+head+", where an earlier attempt left it") {
		t.Errorf("the kickoff does not name the branch's head %s", head)
	}
}

// A session that dies at a usage limit whose reset is unknown ends its
// attempt as resumable and fails nothing; while the limit lasts no task is
// placed on that host × harness, not even another ready one; after 30
// minutes a probe dispatches the task again, resuming its own branch at the
// commit the dead session pushed, and once that session works the host
// takes the waiting task.
func TestALimitDeathWithAnUnknownResetResumesAfterAProbe(t *testing.T) {
	f := newOpsFleet(t, "mac")
	first := f.task("Add a flag")
	accept := f.task("Acceptance", first.ID)
	f.passes(2)
	s := f.session(first)
	s.must("tell", first.ID, "Started")
	head := s.commit("flag.txt", "Add a flag")
	// Claude Code's StopFailure hook at the limit, saying no reset.
	s.mustHook(`{"hook_event_name":"StopFailure","error":"rate_limit"}`, "hook", "limit", "--harness", "claude")
	f.hosts["mac"].tmux.end(s.name, 1, "API Error: Claude usage limit reached.\n")
	// Another task becomes ready, and the acceptance task waits on it too.
	waiting := f.task("Add a switch")
	f.mustOperator("task", "edit", accept.ID, "--version", fmt.Sprint(f.get(accept.ID).Version), "--title", accept.Title,
		"--body", accept.Body, "--depends", first.ID+","+waiting.ID)
	f.pass()
	ended := f.attempts(first.ID)
	if len(ended) != 1 || ended[0].Reason != control.EndVanished || !ended[0].Limited || ended[0].ResetsAt != 0 {
		t.Fatalf("the death at the limit ended %+v; want one vanished, limited attempt with no reset\n%s", ended, f.logs)
	}
	if got := f.get(first.ID).State; got == "failed" {
		t.Fatalf("the task failed at a limit")
	}
	wantIn(t, strings.Join(f.said(first.ID, "say"), "\n"), "died at a usage limit", "by a probe every 30 minutes")

	f.clock.add(29 * time.Minute)
	f.pass()
	if n, m := len(f.attempts(first.ID)), len(f.attempts(waiting.ID)); n != 1 || m != 0 {
		t.Fatalf("within 30 minutes of the limit: %d attempt(s) of the limited task, %d of the waiting one; want 1 and 0", n, m)
	}
	f.clock.add(time.Minute)
	f.pass()
	if n, m := len(f.attempts(first.ID)), len(f.attempts(waiting.ID)); n != 2 || m != 0 {
		t.Fatalf("the probe: %d attempt(s) of the limited task, %d of the waiting one; want 2 and 0, one task at a time\n%s", n, m, f.logs)
	}
	probe := f.session(first)
	resumesOn(t, probe, s.branch, head)

	// The probe's session works: the limit is past, and the host takes
	// the waiting task.
	probe.must("tell", first.ID, "Resumed")
	f.pass()
	if got := f.attempts(waiting.ID); len(got) != 1 || got[0].Host != "mac" {
		t.Errorf("after the probe worked the waiting task has %+v; want it dispatched on mac", got)
	}
}

// engine task stop ends the running execution, keeps its worktree and
// branch, and leaves the task todo and held; no pass dispatches it while
// held, though its card can be edited; engine task resume lifts the hold
// and the next pass dispatches it again on its own branch.
func TestStopEditAndResumeATaskWithNoDispatchWhileHeld(t *testing.T) {
	f := newOpsFleet(t, "mac")
	task := f.task("Add a flag")
	f.passes(2)
	s := f.session(task)
	s.must("tell", task.ID, "Started")
	head := s.commit("flag.txt", "Add a flag")

	f.mustOperator("task", "stop", task.ID)
	stopped := f.get(task.ID)
	ats := f.attempts(task.ID)
	if stopped.State != "todo" || !stopped.Held || len(ats) != 1 || ats[0].Reason != control.EndStopped || ats[0].State != "ended" {
		t.Fatalf("after stop: task %s held %v, attempts %+v", stopped.State, stopped.Held, ats)
	}
	f.passes(3)
	if got := f.attempts(task.ID); len(got) != 1 {
		t.Fatalf("a held task was dispatched: %+v", got)
	}
	if w := f.hosts["mac"].tmux.window(s.name); w != nil {
		t.Errorf("the stopped session's window is still open: %+v", w)
	}
	if _, err := os.Stat(s.worktree); err != nil || f.hosts["mac"].branchAt(s.branch) != head {
		t.Errorf("the stop did not keep the worktree (%v) and branch %s at %s", err, s.branch, head)
	}

	card := "## Outcome\n\nAdd a flag, named --fast.\n\n## Tests\n\n- Unit.\n"
	f.mustOperator("task", "edit", task.ID, "--version", fmt.Sprint(stopped.Version), "--title", "Add the fast flag", "--body", card)
	f.pass()
	if got := f.get(task.ID); got.Body != card || got.Title != "Add the fast flag" || len(f.attempts(task.ID)) != 1 {
		t.Fatalf("the edit while held: %+v, %d attempt(s)", got, len(f.attempts(task.ID)))
	}

	f.mustOperator("task", "resume", task.ID)
	if f.get(task.ID).Held {
		t.Fatal("resume left the hold")
	}
	f.pass()
	if got := f.attempts(task.ID); len(got) != 2 || got[1].State != "running" {
		t.Fatalf("after resume: attempts %+v\n%s", got, f.logs)
	}
	resumesOn(t, f.session(task), s.branch, head)
}

// askAndHold has the session ask the operator a question, and returns its
// update's sequence once the task waits on it.
func askAndHold(t *testing.T, s *opsSession, question string) int {
	t.Helper()
	var asked control.Response
	if err := json.Unmarshal([]byte(s.must("ask", s.task, question)), &asked); err != nil || len(asked.Updates) == 0 {
		t.Fatalf("engine ask printed no update: %v", err)
	}
	if got := s.f.get(s.task); got.State != "needs-human" {
		t.Fatalf("the question left the task %s", got.State)
	}
	return asked.Updates[0].ID
}

// typeIntoPane is the operator typing answer into the session's pane:
// Claude Code writes it to the session's transcript and runs the prompt
// hook on it.
func typeIntoPane(s *opsSession, answer string) {
	transcript := filepath.Join(s.dir, "transcript.jsonl")
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": answer}})
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		s.f.t.Fatal(err)
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
	event, _ := json.Marshal(map[string]string{"session_id": "s-" + s.name, "hook_event_name": "UserPromptSubmit", "cwd": s.worktree,
		"prompt": answer, "transcript_path": transcript})
	s.mustHook(string(event), "hook", "prompt", "--harness", "claude")
}

// The operator's answer typed into the executor's pane reaches the server
// through the prompt hook verbatim, resolves the task's open question and
// lets the task run on, with no note; an answer posted without the hook's
// signature still resolves its question but raises a coordinator note.
func TestAPaneAnswerThroughTheHookResolvesTheQuestionAndAnUnsignedOneIsFlagged(t *testing.T) {
	f := newOpsFleet(t, "mac")
	task := f.task("Drop the column")
	f.passes(2)
	s := f.session(task)
	s.must("tell", task.ID, "Started")

	question := askAndHold(t, s, "May the migration drop the legacy column?")
	const answer = "Yes, drop it; nothing reads it."
	typeIntoPane(s, answer)
	if got := f.get(task.ID); got.State != "running" {
		t.Fatalf("after the pane answer the task is %s", got.State)
	}
	var answered *control.Update
	for _, u := range f.updates(task.ID) {
		if u.Kind == control.KindPaneAnswer {
			answered = &u
		}
	}
	if answered == nil || answered.Body != answer || answered.ReplyTo != question {
		t.Fatalf("the pane answer recorded %+v; want %q answering %d", answered, answer, question)
	}
	if notes := f.said(task.ID, "say"); strings.Contains(strings.Join(notes, "\n"), "may not be the operator's") {
		t.Fatalf("a signed pane answer was flagged: %q", notes)
	}

	askAndHold(t, s, "And the index on it?")
	vars := s.runVars()
	token, _ := os.ReadFile(vars["ENGINE_SERVER_TOKEN_FILE"])
	worker := control.Client{URL: vars["ENGINE_SERVER_URL"], Token: strings.TrimSpace(string(token))}
	if _, err := worker.Call(context.Background(), control.Request{Op: "pane-input", ID: task.ID, Key: "forged", Body: "Drop the index too."}); err != nil {
		t.Fatalf("an unsigned pane answer was refused: %v", err)
	}
	if got := f.get(task.ID); got.State != "running" {
		t.Errorf("the unsigned answer left the task %s", got.State)
	}
	wantIn(t, strings.Join(f.said(task.ID, "say"), "\n"), "may not be the operator's: it is unsigned", "engine task stop "+task.ID)
}

// engine host status --all shows every host from the coordinator's latest
// pass: one it cannot reach, with ssh's reason; each reachable one's slots,
// quota and readiness, a failing doctor making a host unready; and each
// window with the task and attempt it runs.
func TestHostStatusAllShowsEveryHost(t *testing.T) {
	f := newOpsFleet(t, "mac", "box", "lab")
	writeFile(t, filepath.Join(f.hosts["box"].home, "doctor.fail"), "go: command not found\n")
	f.hosts["lab"].down = true
	flag, sw := f.task("Add a flag"), f.task("Add a switch")
	f.task("Acceptance", flag.ID, sw.ID)
	f.passes(2)
	reset := f.clock.now().Add(2 * time.Hour).Unix()
	for _, task := range []control.Item{flag, sw} {
		s := f.session(task)
		s.must("tell", task.ID, "Started")
		s.mustHook(fmt.Sprintf(`{"session_name":%q,"rate_limits":{"five_hour":{"used_percentage":40,"resets_at":%d}}}`, s.name, reset),
			"hook", "statusline", "--harness", "claude")
	}
	f.pass()

	out := f.mustOperator("host", "status", "--all")
	wantIn(t, out, "As of the coordinator's pass at 2026-11-20T09:00:00Z")
	wantRow(t, out, "lab", "no: host lab is unreachable over ssh (dev@lab): ssh: connect to host dev@lab port 22: No route to host", "-", "-", "-")
	wantRow(t, out, "mac", "yes", "0 of 2 (heavy 0 of 2)", fmt.Sprintf("claude five_hour 40%% to %s", time.Unix(reset, 0).UTC().Format("01-02 15:04Z")), "ready")
	wantRow(t, out, "box", "yes", "2 of 2 (heavy 2 of 2)", "-", "unready: fleet/app: ./dev doctor failed (the readiness note has the output)")
	for _, task := range []control.Item{flag, sw} {
		at := f.get(task.ID).Attempt
		wantRow(t, out, "mac", fmt.Sprintf("app-%d", control.Number(task.ID)), "working", "claude", task.ID+" ("+at+")")
	}

	var fleet control.HostStatus
	if err := json.Unmarshal([]byte(f.mustOperator("host", "status", "--all", "--json")), &fleet); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range fleet.Hosts {
		names = append(names, h.Name)
	}
	if strings.Join(names, " ") != "box lab mac" {
		t.Errorf("the fleet lists %q; want every host", names)
	}
}
