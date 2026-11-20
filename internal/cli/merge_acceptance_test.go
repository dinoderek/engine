package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// The acceptance tests of plan questions, attempt reconcile and merges, one
// per objective, on the operations fleet in opsfleet_test.go.

// runningSession is task's running session, once it wrote its first
// update.
func runningSession(f *opsFleet, task control.Item) *opsSession {
	f.t.Helper()
	s := f.session(task)
	s.must("tell", task.ID, "Started.")
	return s
}

// idleAfterCleanup reports the cleanup as the session does after its merge
// and leaves it idle, so the coordinator releases it.
func idleAfterCleanup(f *opsFleet, s *opsSession) {
	f.t.Helper()
	s.must("task", "cleaned", s.task)
	s.h.setAgent(s.name, "idle")
	f.passes(2)
}

// updateKinds are the kinds of the item's updates, oldest first.
func updateKinds(f *opsFleet, id string) []string {
	var out []string
	for _, u := range f.updates(id) {
		out = append(out, u.Kind)
	}
	return out
}

// A question a session asks on its plan holds only what it must: a task
// already running moves to in-review and merges, while a task that depends
// on it, ready once the merge is done, gets no attempt until the operator
// resolves the question.
func TestAPlanQuestionBlocksOnlyNewDispatches(t *testing.T) {
	f := newOpsFleet(t, "mac", "box")
	asker, other := f.task("Pick a schema"), f.task("Add a flag")
	waiting := f.task("Document the flag", other.ID)
	f.task("Accept", asker.ID, waiting.ID)
	f.passes(2)
	a, b := runningSession(f, asker), runningSession(f, other)
	a.must("ask", f.plan.ID, "Should the schema keep the old column?")
	n := b.review(b.commit("flag.txt", "Add a flag"), "Add a flag", "Adds the flag.")
	f.passes(3)
	if got := f.github.pr(n).State; got != "MERGED" {
		t.Fatalf("the running task's PR is %s with the plan question open; want MERGED\n%s", got, f.logs)
	}
	if got := f.get(other.ID).State; got != "done" {
		t.Errorf("the running task is %s; want done", got)
	}
	if got := f.attempts(waiting.ID); len(got) != 0 {
		t.Errorf("a new task was dispatched while the plan question was open: %+v", got)
	}
	question := f.get(f.plan.ID).Question
	if question == nil {
		t.Fatal("the plan shows no open question")
	}
	f.call(control.Request{Op: "resolve", ReplyTo: question.Update, Body: "Keep it."})
	f.passes(2)
	if got := f.attempts(waiting.ID); len(got) != 1 {
		t.Errorf("after the answer the new task has %d attempt(s); want 1\n%s", len(got), f.logs)
	}
}

// engine attempt reconcile takes its evidence as positional words, as its
// usage line says. A host that shows the session alive refuses it with a
// 409 carrying its own reason, which the CLI prints once and never retries;
// once the window is dead the same command ends the attempt.
func TestAttemptReconcileTakesPositionalEvidenceAndAHostRefusalIs409(t *testing.T) {
	f := newOpsFleet(t, "mac", "box")
	task := f.task("Add a flag")
	f.passes(2)
	s := runningSession(f, task)
	at := f.get(task.ID).Attempt
	_, out, usage := f.engine("attempt")
	wantIn(t, out+usage, "engine attempt reconcile <attempt-id> <evidence> | --body <evidence>")
	code, _, errOut := f.engine("attempt", "reconcile", at, "host", "rebooted")
	want := fmt.Sprintf("engine: reconcile-refused: execution is still alive in window %s on %s\n", s.name, s.h.name)
	if code != 1 || errOut != want {
		t.Errorf("a live session: exit %d, stderr %q; want 1 and %q", code, errOut, want)
	}
	_, err := f.operator().Call(context.Background(), control.Request{Op: "attempt-reconcile", ID: at, Body: "host rebooted"})
	var p *control.Problem
	if !errors.As(err, &p) || p.Status != 409 || p.Reason != control.ReasonReconcileRefused || strings.Contains(err.Error(), "retry") {
		t.Errorf("the refusal is %v; want a 409 %s problem, not a retryable failure", err, control.ReasonReconcileRefused)
	}
	if got := f.attempts(task.ID)[0].State; got != "running" {
		t.Errorf("the refused reconcile left the attempt %s", got)
	}
	s.h.tmux.end(s.name, 1, "killed\n")
	if code, out, errOut := f.engine("attempt", "reconcile", at, "the", "host", "rebooted"); code != 0 {
		t.Fatalf("a dead window: exit %d: %s%s", code, errOut, out)
	}
	if got := f.attempts(task.ID)[0]; got.State != "ended" {
		t.Errorf("the reconciled attempt is %s; want ended", got.State)
	}
}

// A check whose older run failed and whose newer run is still pending
// waits: the merge is neither made nor refused, and the task stays in
// review. Once the newer run passes, the PR merges.
func TestAMergeWaitsForTheNewestCheckRunInsteadOfRefusing(t *testing.T) {
	f := newOpsFleet(t, "mac", "box")
	task := f.task("Add a flag")
	f.passes(2)
	s := runningSession(f, task)
	head := s.commit("flag.txt", "Add a flag")
	n := f.github.open(s.branch, head, "Add a flag", "Adds the flag.")
	f.github.check(n, "test", "FAILURE", f.clock.now())
	f.github.check(n, "test", "", f.clock.now().Add(time.Minute))
	s.inReview(n)
	s.must("task", "merge-ready", task.ID, "--pr", f.github.url(n), "--head", head, "--method", "squash", "--verdict", "APPROVED")
	f.passes(3)
	if got := f.github.pr(n).State; got != "OPEN" {
		t.Fatalf("with the newest run pending the PR is %s; want OPEN", got)
	}
	for _, k := range updateKinds(f, task.ID) {
		if k == "merge-refused" || k == "merged" {
			t.Fatalf("with the newest run pending the queue posted %s: %v", k, updateKinds(f, task.ID))
		}
	}
	if got := f.get(task.ID).State; got != "in-review" {
		t.Errorf("the task is %s while its check runs; want in-review", got)
	}
	f.github.finish(n, "test", "SUCCESS")
	f.clock.add(3 * time.Minute)
	f.passes(2)
	if got := f.github.pr(n).State; got != "MERGED" {
		t.Errorf("once the newest run passed the PR is %s; want MERGED\n%s", got, f.logs)
	}
}

// The merge commit on the default branch carries the PR's title with its
// number as the subject and the PR's body as the body; the branch's own
// commit messages never reach it.
func TestTheMergeCarriesThePRTitleAndBody(t *testing.T) {
	f := newOpsFleet(t, "mac", "box")
	task := f.task("Add a flag")
	f.passes(2)
	s := runningSession(f, task)
	s.commit("flag.txt", "wip first try")
	head := s.commit("flag.txt", "fixup review round")
	const body = "Adds the --flag option.\n\nGates: ./dev test green."
	n := s.review(head, "Add a flag option", body)
	f.passes(2)
	got := f.github.originGit("log", "-1", "--format=%B", "main")
	if want := fmt.Sprintf("Add a flag option (#%d)\n\n%s", n, body); got != want {
		t.Errorf("main's merge commit says:\n%s\nwant:\n%s", got, want)
	}
	for _, branchMessage := range []string{"wip first try", "fixup review round"} {
		if strings.Contains(got, branchMessage) {
			t.Errorf("the branch's commit message %q reached main", branchMessage)
		}
	}
}

// Releasing a merged task removes its worktree and deletes its local
// branch; a branch holding a commit that never reached the PR is kept, and
// the operator gets one note naming it.
func TestReleaseDeletesTheMergedBranchAndKeepsAnUnmergedOne(t *testing.T) {
	f := newOpsFleet(t, "mac")
	merged, extra := f.task("Add a flag"), f.task("Add a switch")
	f.task("Accept", merged.ID, extra.ID)
	f.passes(2)
	m, x := runningSession(f, merged), runningSession(f, extra)
	m.review(m.commit("flag.txt", "Add a flag"), "Add a flag", "Adds the flag.")
	f.passes(2)
	x.commit("switch.txt", "Add a switch")
	x.h.git(x.worktree, "pull", "--quiet", "--no-rebase", "--no-edit", "origin", "main")
	x.h.git(x.worktree, "push", "--quiet", "origin", "HEAD:refs/heads/"+x.branch)
	x.review(x.h.git(x.worktree, "rev-parse", "HEAD"), "Add a switch", "Adds the switch.")
	f.passes(2)
	writeFile(t, filepath.Join(x.worktree, "later.txt"), "later\n")
	x.h.git(x.worktree, "add", "later.txt")
	x.h.git(x.worktree, "commit", "--quiet", "-m", "A commit after the merge")
	idleAfterCleanup(f, m)
	idleAfterCleanup(f, x)
	for _, s := range []*opsSession{m, x} {
		if s.h.tmux.window(s.name) != nil {
			t.Errorf("%s's window is still open after its release\n%s", s.name, f.logs)
		}
		if _, err := s.h.run(s.worktree, nil, "git", "status"); err == nil {
			t.Errorf("%s's worktree %s is still there", s.name, s.worktree)
		}
	}
	if m.h.branchAt(m.branch) != "" {
		t.Errorf("the merged branch %s is still in the clone", m.branch)
	}
	if x.h.branchAt(x.branch) == "" {
		t.Fatalf("the branch %s with an unmerged commit was deleted", x.branch)
	}
	var notes []string
	for _, u := range f.updates(extra.ID) {
		if strings.Contains(u.Body, "left a note") {
			notes = append(notes, u.Body)
		}
	}
	if len(notes) != 1 {
		t.Fatalf("notes on the kept branch: %q; want one\n%s", notes, f.logs)
	}
	wantIn(t, notes[0], "kept the branch "+x.branch, "commits that never reached main")
	for _, u := range f.updates(merged.ID) {
		if strings.Contains(u.Body, "left a note") {
			t.Errorf("the merged task's release left a note: %s", u.Body)
		}
	}
}
