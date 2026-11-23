package controlrun

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// limitedRunning is the setup's task, claimed and running claude on
// devbox, whose window then stops on a usage limit resetting reset after the
// setup's time. With codex set, devbox also runs codex, which has room.
func limitedRunning(t *testing.T, reset time.Duration, codex bool) (*Coordinator, *host, control.Item) {
	t.Helper()
	c, h, _, _, task := setup(t)
	if codex {
		c.Config.HarnessRatio["codex"] = 1
		c.Config.Harnesses["codex"] = c.Config.Harnesses["claude"]
		c.Config.Hosts["devbox"] = hosts.Host{Name: "devbox", Harnesses: []string{"claude", "codex"}, MaxSessions: 2}
		c.Host = c.Config.Hosts["devbox"]
	}
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
	pass(t, c)
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	for i, w := range h.status.Windows {
		if w.Name == "demo-"+task.ID[1:] {
			h.status.Windows[i].State, h.status.Windows[i].ResetsAt = session.StateLimited, start.Add(reset).Unix()
		}
	}
	return c, h, task
}

func TestALimitedSessionWithRoomIsAskedOnceAndAMoveRunsOnce(t *testing.T) {
	c, h, task := limitedRunning(t, time.Hour, true)
	pass(t, c)
	pass(t, c)
	qs := hostQuestions(c)
	if len(qs) != 1 || qs[0].Item != "" || qs[0].Choices[1] != "Move to codex on devbox" || *qs[0].Recommended != 0 ||
		!strings.HasPrefix(qs[0].Body, task.ID+" is at claude's limit on devbox until 2026-11-07 10:00 UTC.") {
		t.Fatalf("questions %+v", qs)
	}
	st, _ := c.Service.Store.Snapshot()
	first := st.Items[task.ID].Attempt
	if st.Items[task.ID].State != "running" || st.Attempts[first].State == "ended" {
		t.Fatalf("the question held or ended the task: %+v", st.Items[task.ID])
	}
	execute(t, c.Service, control.Request{Op: "resolve", ReplyTo: qs[0].ID, Body: qs[0].Choices[1]})
	pass(t, c)
	pass(t, c)
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if at := st.Attempts[first]; at.Reason != control.EndMoved || at.MoveHost != "devbox" || at.MoveHarness != "codex" {
		t.Fatalf("the answer did not move the task: %+v", at)
	}
	moves, notes := 0, 0
	for _, u := range st.Updates {
		if u.Item == task.ID && strings.HasPrefix(u.Body, control.EndMoved+":") {
			moves++
		}
		if u.Item == "" && strings.HasPrefix(u.Body, "As answered in update") {
			notes++
		}
	}
	if moves != 1 || notes != 1 {
		t.Fatalf("the move ran %d times, with %d notes", moves, notes)
	}
	// The old window is released and the task resumes with codex.
	at := st.Attempts[st.Items[task.ID].Attempt]
	if at.ID == first || at.Harness != "codex" || !h.launches[at.RunID].Spawn.Resume {
		t.Fatalf("the moved task's next attempt: %+v", at)
	}
}

func TestALimitedSessionWithNoRoomKeepsTodaysSteps(t *testing.T) {
	c, _, task := limitedRunning(t, 48*time.Hour, false)
	pass(t, c)
	pass(t, c)
	if qs := hostQuestions(c); len(qs) != 0 {
		t.Fatalf("questions %+v", qs)
	}
	if notices := taskNotices(c, task); len(notices) != 1 || !strings.Contains(notices[0], "more than 24 hours away") {
		t.Fatalf("notices %q", notices)
	}
	// With room, the question stands in for the far notice.
	c, _, task = limitedRunning(t, 48*time.Hour, true)
	pass(t, c)
	pass(t, c)
	if qs := hostQuestions(c); len(qs) != 1 || *qs[0].Recommended != 1 {
		t.Fatalf("questions %+v", qs)
	}
	if notices := taskNotices(c, task); len(notices) != 0 {
		t.Fatalf("notices %q", notices)
	}
}

func TestALimitQuestionResolvesItselfOnceTheSessionResumes(t *testing.T) {
	c, h, task := limitedRunning(t, time.Hour, true)
	pass(t, c)
	q := hostQuestions(c)[0]
	for i, w := range h.status.Windows {
		if w.Name == "demo-"+task.ID[1:] {
			h.status.Windows[i].State, h.status.Windows[i].ResetsAt = session.StateWorking, 0
		}
	}
	pass(t, c)
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	var answers []string
	for _, u := range st.Updates {
		if u.ReplyTo == q.ID && u.Kind == "resolved" {
			answers = append(answers, u.Author+": "+u.Body)
		}
	}
	if len(answers) != 1 || answers[0] != "coordinator: "+control.ResolutionNoLimit {
		t.Fatalf("answers %q", answers)
	}
	if st.Items[task.ID].State != "running" || st.Attempts[st.Items[task.ID].Attempt].State != "running" {
		t.Fatalf("the task: %+v", st.Items[task.ID])
	}
}
