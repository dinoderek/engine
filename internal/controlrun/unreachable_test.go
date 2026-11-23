package controlrun

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// clocked gives the coordinator a clock the test moves, from start.
func clocked(c *Coordinator, start time.Time) *time.Time {
	now := start
	c.Service.Now = func() time.Time { return now }
	return &now
}

// passAt runs one pass at each time after start, in order.
func passAt(t *testing.T, c *Coordinator, now *time.Time, start time.Time, at ...time.Duration) {
	t.Helper()
	for _, d := range at {
		*now = start.Add(d)
		pass(t, c)
	}
}

// hostQuestions are the questions about a host.
func hostQuestions(c *Coordinator) []control.Update {
	st, _ := snapshotOf(c.Service.Store)
	var out []control.Update
	for _, u := range st.Updates {
		if u.Host != "" && u.RequiresReply {
			out = append(out, u)
		}
	}
	return out
}

// runningUnreachable is the setup's task, claimed and running on devbox,
// which then stops answering at start.
func runningUnreachable(t *testing.T) (*Coordinator, *host, control.Item, *time.Time, time.Time) {
	t.Helper()
	c, h, _, _, task := setup(t)
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	now := clocked(c, start)
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
	pass(t, c)
	h.outage = true
	return c, h, task, now, start
}

func TestAnUnreachableHostIsAskedAboutOnceAfterFifteenMinutes(t *testing.T) {
	c, _, task, now, start := runningUnreachable(t)
	// Passes every few minutes, so none is taken for a server outage.
	passAt(t, c, now, start, 0, 4*time.Minute, 8*time.Minute, 12*time.Minute, 14*time.Minute+59*time.Second)
	if got := hostQuestions(c); len(got) != 0 {
		t.Fatalf("asked before 15 minutes: %+v", got)
	}
	passAt(t, c, now, start, 15*time.Minute, 19*time.Minute, 23*time.Minute)
	got := hostQuestions(c)
	if len(got) != 1 || got[0].Host != "devbox" || got[0].Item != "" || len(got[0].Choices) != 3 ||
		!strings.HasPrefix(got[0].Body, "devbox has been unreachable since 2026-11-07 09:00 UTC. 1 task runs there: "+task.ID+".") {
		t.Fatalf("questions %+v", got)
	}
	st, _ := snapshotOf(c.Service.Store)
	if st.Items[task.ID].State != "running" || st.Attempts[st.Items[task.ID].Attempt].State == "ended" {
		t.Fatalf("the question held or ended the task: %+v", st.Items[task.ID])
	}
}

func TestAnUnreachableHostWithNoLiveAttemptsIsNotAsked(t *testing.T) {
	c, h, _, _, _ := setup(t)
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	now := clocked(c, start)
	h.outage = true
	passAt(t, c, now, start, 0, 4*time.Minute, 8*time.Minute, 12*time.Minute, 16*time.Minute, 20*time.Minute)
	if got := hostQuestions(c); len(got) != 0 || len(h.launches) != 0 {
		t.Fatalf("questions %+v, launches %d", got, len(h.launches))
	}
}

func TestAHostReturningFirstResolvesItsQuestionWithOneNote(t *testing.T) {
	c, h, task, now, start := runningUnreachable(t)
	passAt(t, c, now, start, 0, 4*time.Minute, 8*time.Minute, 12*time.Minute, 15*time.Minute)
	q := hostQuestions(c)[0]
	h.outage = false
	passAt(t, c, now, start, 17*time.Minute, 19*time.Minute)
	st, _ := snapshotOf(c.Service.Store)
	var answers []string
	for _, u := range st.Updates {
		if u.ReplyTo == q.ID && u.Kind == "resolved" {
			answers = append(answers, u.Author+": "+u.Body)
		}
	}
	if len(answers) != 1 || answers[0] != "coordinator: returned" {
		t.Fatalf("answers %q", answers)
	}
	var returned []string
	for _, n := range hostNotices(c) {
		if strings.HasPrefix(n, "devbox answers again") {
			returned = append(returned, n)
		}
	}
	want := "devbox answers again, before question " + strconv.Itoa(q.ID) + " was answered.\n" + task.ID + ": still runs there."
	if len(returned) != 1 || returned[0] != want {
		t.Fatalf("notes %q", returned)
	}
	if st.Items[task.ID].State != "running" || st.Attempts[st.Items[task.ID].Attempt].State != "running" {
		t.Fatalf("the returned task: %+v", st.Attempts[st.Items[task.ID].Attempt])
	}
}

func TestTheOperatorsAnswerRunsOnTheNextPassAndOnce(t *testing.T) {
	c, _, task, now, start := runningUnreachable(t)
	passAt(t, c, now, start, 0, 4*time.Minute, 8*time.Minute, 12*time.Minute, 15*time.Minute)
	q := hostQuestions(c)[0]
	execute(t, c.Service, control.Request{Op: "resolve", ReplyTo: q.ID, Body: control.ChoiceMove})
	before, _ := snapshotOf(c.Service.Store)
	first := before.Items[task.ID].Attempt
	passAt(t, c, now, start, 17*time.Minute, 19*time.Minute, 21*time.Minute)
	st, _ := snapshotOf(c.Service.Store)
	if at := st.Attempts[first]; at.Reason != control.EndMoved || !at.MovedBlind || !st.Hosts["devbox"].Paused {
		t.Fatalf("the answer did not move the task off devbox: %+v, %+v", at, st.Hosts["devbox"])
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
}

// Nobody reads the host while the server is down: its stretch counts again
// from the first failed read after the outage.
func TestAServerOutageRestartsTheUnreachableCount(t *testing.T) {
	c, _, _, now, start := runningUnreachable(t)
	passAt(t, c, now, start, 0, 4*time.Minute, 8*time.Minute, 20*time.Minute, 24*time.Minute, 28*time.Minute, 32*time.Minute, 34*time.Minute+59*time.Second)
	if got := hostQuestions(c); len(got) != 0 {
		t.Fatalf("asked within 15 minutes of passes after the outage: %+v", got)
	}
	passAt(t, c, now, start, 35*time.Minute)
	got := hostQuestions(c)
	if len(got) != 1 || !strings.Contains(got[0].Body, "unreachable since 2026-11-07 09:20 UTC") {
		t.Fatalf("questions %+v", got)
	}
}
