package controlrun

import (
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/reconcile"
)

// merged is a task whose PR the coordinator has just merged, its session
// waiting in its window, and that session's credential.
func merged(t *testing.T) (*Coordinator, *merger, control.Item, control.Actor) {
	t.Helper()
	c, m, task, worker := requested(t, reply(hostops.CodeMerged, "merged at "+reviewedHead))
	c.Wake = func(reconcile.Session, string) error { return nil }
	m.status.Windows[0].State = "waiting"
	m.status.Runs[0].Record.Worktree, m.status.Runs[0].Record.Branch = "/w/demo-3", "task/demo-3"
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "done" || len(m.released) != 0 {
		t.Fatalf("task %s, released %v: want merged and kept for its cleanup turn", st.Items[task.ID].State, m.released)
	}
	return c, m, task, worker
}

func reportCleaned(t *testing.T, c *Coordinator, worker control.Actor, id, left string) {
	t.Helper()
	if _, err := c.Service.Execute(worker, control.Request{Op: "cleaned", ID: id, Body: left, Key: control.Secret()}); err != nil {
		t.Fatal(err)
	}
}

func releaseTexts(t *testing.T, c *Coordinator, id string) []string {
	t.Helper()
	var out []string
	for _, u := range operatorNotices(t, c, id) {
		if !strings.Contains(u.Body, "merged outside engine") {
			out = append(out, u.Body)
		}
	}
	return out
}

// A session that reports its cleanup is released on the next pass, as a done
// task's release, and the operator is not told.
func TestACleanedSessionIsReleasedAtOnce(t *testing.T) {
	c, m, task, worker := merged(t)
	reportCleaned(t, c, worker, task.ID, "")
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if !slices.Equal(m.outcomes, []hostops.Outcome{hostops.RunDone}) || st.Attempts[st.Items[task.ID].Attempt].State != "ended" {
		t.Fatalf("outcomes %v, attempt %s", m.outcomes, st.Attempts[st.Items[task.ID].Attempt].State)
	}
	if n := releaseTexts(t, c, task.ID); len(n) != 0 {
		t.Fatalf("notices %q, want none", n)
	}
}

// What the session left, and the host's note on the release, such as the
// repo's own worktree release failing, each reach the operator once.
func TestLeftoversAndReleaseNotesAreToldOnce(t *testing.T) {
	c, m, task, worker := merged(t)
	m.note = "the repo's worktree release for task/demo-3: no answer within 2m0s"
	reportCleaned(t, c, worker, task.ID, "a docker volume")
	pass(t, c)
	pass(t, c)
	want := []string{
		"demo-3 cleaned up after " + task.ID + " merged, but left on host devbox: a docker volume",
		"Releasing demo-3 on host devbox left a note: the repo's worktree release for task/demo-3: no answer within 2m0s",
	}
	if got := releaseTexts(t, c, task.ID); !slices.Equal(got, want) {
		t.Fatalf("notices %q, want %q", got, want)
	}
}

// A session whose window is gone without a report is released at once, and
// the operator is told where to look.
func TestAGoneSessionWithoutAReportIsReleasedAndTold(t *testing.T) {
	c, m, task, _ := merged(t)
	m.status.Windows = nil
	pass(t, c)
	pass(t, c)
	want := []string{"demo-3 was released without confirming cleanup after " + task.ID + " merged: host devbox, worktree /w/demo-3, branch task/demo-3"}
	if got := releaseTexts(t, c, task.ID); !slices.Equal(got, want) || !slices.Equal(m.released, []string{"demo-3"}) {
		t.Fatalf("notices %q, released %v; want %q", got, m.released, want)
	}
}
