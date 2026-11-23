package control

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var everyWindowAlive = func(*Tx, Attempt) bool { return true }

// unreachableFor has every read of devbox fail, at its start and then after
// each of the waits, on a clock the service reads.
func unreachableFor(t *testing.T, s *Service, start time.Time, waits ...time.Duration) {
	t.Helper()
	now := start
	s.Now = func() time.Time { return now }
	for i, at := range append([]time.Duration{0}, waits...) {
		now = start.Add(at)
		if err := s.SawReach("devbox", false, everyWindowAlive); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
}

// fleetQuestions are the questions about a host, oldest first.
func fleetQuestions(st State) []Update {
	var out []Update
	for _, u := range st.Updates {
		if u.Host != "" && u.RequiresReply {
			out = append(out, u)
		}
	}
	return out
}

// coordinatorNotes are the coordinator's messages to the operator, each
// about devbox.
func coordinatorNotes(st State) []string {
	var out []string
	for _, u := range st.Updates {
		if u.Author == "coordinator" && u.Kind == "say" && u.Recipient == "operator" {
			out = append(out, u.Body)
			if u.Host != "devbox" || u.Item != "" {
				out = append(out, "a note not about devbox alone: "+u.Host+u.Item)
			}
		}
	}
	return out
}

// An outage keeps a stretch already asked about, and restarts one that was
// not.
func TestAServerOutageKeepsOnlyAStretchAskedAbout(t *testing.T) {
	s, _ := onHosts(t)
	unreachableFor(t, &s, stretch, UnreachableAsk)
	now := stretch
	s.Now = func() time.Time { return now }
	if err := s.SawReach("mac", false, everyWindowAlive); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Store.StartPass(stretch, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, gap, err := s.Store.StartPass(stretch.Add(time.Hour), 5*time.Minute); err != nil || !gap {
		t.Fatalf("no outage: %v", err)
	}
	st := mustSnapshot(t, s)
	if rec := st.Hosts["devbox"]; rec.Question == 0 || !rec.UnreachableSince.Equal(stretch) {
		t.Errorf("the asked stretch was restarted: %+v", rec)
	}
	if rec := st.Hosts["mac"]; !rec.UnreachableSince.IsZero() {
		t.Errorf("the unasked stretch was kept: %+v", rec)
	}
}

var stretch = time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)

func TestAnUnreachableHostIsAskedAboutOnceItsTasksWaitedFifteenMinutes(t *testing.T) {
	s, tasks := onHosts(t)
	unreachableFor(t, &s, stretch, UnreachableAsk-time.Second)
	if got := fleetQuestions(mustSnapshot(t, s)); len(got) != 0 {
		t.Fatalf("asked before %s: %+v", UnreachableAsk, got)
	}
	unreachableFor(t, &s, stretch.Add(UnreachableAsk), 0, time.Minute, time.Hour)
	st := mustSnapshot(t, s)
	got := fleetQuestions(st)
	if len(got) != 1 {
		t.Fatalf("questions %+v", got)
	}
	q := got[0]
	want := "devbox has been unreachable since 2026-11-22 09:00 UTC. 2 tasks run there: " + tasks[0].ID + ", " + tasks[1].ID + "."
	if q.Item != "" || q.Host != "devbox" || q.Kind != "ask" || q.Recipient != "operator" || q.Class != "environment" || !strings.HasPrefix(q.Body, want) ||
		!strings.Contains(q.Body, "`engine host move devbox`") || !slices.Equal(q.Choices, []string{ChoiceWait, ChoiceMove, ChoiceStop}) || q.Recommended == nil || *q.Recommended != 0 {
		t.Fatalf("question %+v", q)
	}
	if rec := st.Hosts["devbox"]; rec.Question != q.ID || !rec.UnreachableSince.Equal(stretch) || !slices.Equal(rec.Tasks, []string{tasks[0].ID, tasks[1].ID}) {
		t.Fatalf("host record %+v", rec)
	}
	// It holds no task: each keeps its state, and can still change it.
	for _, task := range tasks {
		if st.Items[task.ID].State != "running" || OwnQuestion(st.LegacyTx(), task.ID) != 0 {
			t.Fatalf("%s is held: %+v", task.ID, st.Items[task.ID])
		}
	}
	inbox := call(t, s, op, Request{Op: "inbox"}).Updates
	if !slices.ContainsFunc(inbox, func(u Update) bool { return u.ID == q.ID }) {
		t.Fatal("the question is not in the inbox")
	}
}

func TestAnUnreachableHostWithNoTasksIsNotAsked(t *testing.T) {
	s, _ := onHosts(t)
	now := stretch
	s.Now = func() time.Time { return now }
	for _, at := range []time.Duration{0, UnreachableAsk, time.Hour} {
		now = stretch.Add(at)
		if err := s.SawReach("mac", false, everyWindowAlive); err != nil {
			t.Fatal(err)
		}
	}
	st := mustSnapshot(t, s)
	if got := fleetQuestions(st); len(got) != 0 || !st.Hosts["mac"].UnreachableSince.Equal(stretch) {
		t.Fatalf("questions %+v, record %+v", got, st.Hosts["mac"])
	}
}

// waited checks that a wait answer changed nothing.
func waited(t *testing.T, st State, tasks []Item) {
	if st.Hosts["devbox"].Paused || st.Items[tasks[0].ID].State != "running" || st.Attempts[st.Items[tasks[0].ID].Attempt].State == "ended" {
		t.Errorf("a wait changed something: %+v", st.Items[tasks[0].ID])
	}
}

// moved checks that a move answer paused devbox and moved its two tasks,
// and only those.
func moved(t *testing.T, st State, tasks []Item) {
	for _, task := range tasks[:2] {
		if at := st.Attempts[st.Items[task.ID].Attempt]; at.Reason != EndMoved || st.Items[task.ID].State != "running" {
			t.Errorf("%s was not moved: %+v", task.ID, at)
		}
	}
	if !st.Hosts["devbox"].Paused || st.Attempts[st.Items[tasks[2].ID].Attempt].State == "ended" {
		t.Errorf("the host is not paused, or spare's task moved: %+v", st.Hosts["devbox"])
	}
}

// stopped checks that a stop answer held devbox's two tasks, leaving the
// host unpaused.
func stopped(t *testing.T, st State, tasks []Item) {
	for _, task := range tasks[:2] {
		if at := st.Attempts[st.Items[task.ID].Attempt]; at.Reason != EndStopped || !st.Items[task.ID].Held || st.Items[task.ID].State != "todo" {
			t.Errorf("%s was not stopped: %+v %+v", task.ID, at, st.Items[task.ID])
		}
	}
	if st.Hosts["devbox"].Paused {
		t.Error("a stop paused the host")
	}
}

func TestEachAnswerToAFleetQuestionRunsOnce(t *testing.T) {
	for name, c := range map[string]struct {
		answer string
		check  func(*testing.T, State, []Item)
		// note is the operator's note, given the answer's update and the
		// moved or stopped tasks; empty is none.
		note string
	}{
		"wait": {answer: ChoiceWait, check: waited},
		"move": {answer: ChoiceMove + "\nthey are urgent", check: moved,
			note: "As answered in update %d, the coordinator moved %s on devbox. devbox is paused until `engine host resume devbox`."},
		"stop": {answer: ChoiceStop, check: stopped,
			note: "As answered in update %d, the coordinator stopped %s on devbox. Each is held until `engine task resume`."},
	} {
		t.Run(name, func(t *testing.T) {
			s, tasks := onHosts(t)
			unreachableFor(t, &s, stretch, UnreachableAsk)
			q := fleetQuestions(mustSnapshot(t, s))[0]
			out := call(t, s, op, Request{Op: "resolve", ReplyTo: q.ID, Body: c.answer})
			if len(out.Updates) != 1 {
				t.Fatalf("the resolution released something: %+v", out.Updates)
			}
			by := out.Updates[0].ID
			// The next reads run it once; the host stays unreachable and is
			// not asked about again.
			unreachableFor(t, &s, stretch.Add(UnreachableAsk+time.Minute), time.Minute, time.Hour)
			st := mustSnapshot(t, s)
			c.check(t, st, tasks)
			notes := coordinatorNotes(st)
			want := []string{fmt.Sprintf(c.note, by, tasks[0].ID+", "+tasks[1].ID)}
			if c.note == "" {
				want = nil
			}
			if !slices.Equal(notes, want) {
				t.Errorf("notes %q", notes)
			}
			if got := fleetQuestions(st); len(got) != 1 || !st.Hosts["devbox"].Acted {
				t.Errorf("questions %d, record %+v", len(got), st.Hosts["devbox"])
			}
		})
	}
}

func TestAHostThatAnswersFirstResolvesItsQuestionAsReturned(t *testing.T) {
	s, tasks := onHosts(t)
	unreachableFor(t, &s, stretch, UnreachableAsk)
	q := fleetQuestions(mustSnapshot(t, s))[0]
	first := tasks[0].ID
	alive := func(_ *Tx, at Attempt) bool { return at.Item == first }
	for range 2 {
		if err := s.SawReach("devbox", true, alive); err != nil {
			t.Fatal(err)
		}
	}
	st := mustSnapshot(t, s)
	by := st.LegacyTx().Resolution(q.ID)
	if by == 0 || st.Updates[by-1].Body != ResolutionReturned || st.Updates[by-1].Author != "coordinator" {
		t.Fatalf("question %d is not resolved as returned", q.ID)
	}
	want := "devbox answers again, before question " + strconv.Itoa(q.ID) + " was answered.\n" +
		first + ": still runs there.\n" + tasks[1].ID + ": its session there is gone; it resumes, or ends, by the vanish rules."
	if notes := coordinatorNotes(st); !slices.Equal(notes, []string{want}) {
		t.Fatalf("notes %q", notes)
	}
	if rec := st.Hosts["devbox"]; !rec.UnreachableSince.IsZero() || rec.Question != 0 || rec.Tasks != nil {
		t.Fatalf("the stretch did not end: %+v", rec)
	}
	// A new stretch gets its own question.
	unreachableFor(t, &s, stretch.Add(2*time.Hour), UnreachableAsk)
	if got := fleetQuestions(mustSnapshot(t, s)); len(got) != 2 {
		t.Fatalf("questions %d", len(got))
	}
}

func TestTheReturnedNoteSaysWhatBecameOfATaskNoLongerThere(t *testing.T) {
	s, tasks := onHosts(t)
	unreachableFor(t, &s, stretch, UnreachableAsk)
	call(t, s, op, Request{Op: "task-stop", ID: tasks[1].ID})
	if err := s.SawReach("devbox", true, everyWindowAlive); err != nil {
		t.Fatal(err)
	}
	notes := coordinatorNotes(mustSnapshot(t, s))
	if len(notes) != 1 || !strings.Contains(notes[0], tasks[1].ID+": no longer runs there; it is todo.") {
		t.Fatalf("notes %q", notes)
	}
}

// A task whose dispatch is still in flight has no session to stop: the
// answer leaves it, saying so, and the note on the host's return says its
// dispatch is settled next.
func TestAnAnswerLeavesATaskWhoseDispatchIsInFlight(t *testing.T) {
	s, tasks := onHosts(t)
	extra := newTask(t, s, tasks[0].Repo, tasks[0].Plan)
	call(t, s, op, Request{Op: "claim", ID: extra.ID, Body: "devbox", State: "claude"})
	unreachableFor(t, &s, stretch, UnreachableAsk)
	q := fleetQuestions(mustSnapshot(t, s))[0]
	if !strings.Contains(q.Body, "3 tasks run there: "+tasks[0].ID+", "+tasks[1].ID+", "+extra.ID+".") {
		t.Fatalf("question %q", q.Body)
	}
	call(t, s, op, Request{Op: "resolve", ReplyTo: q.ID, Body: ChoiceStop})
	if err := s.SawReach("devbox", true, everyWindowAlive); err != nil {
		t.Fatal(err)
	}
	st := mustSnapshot(t, s)
	notes := coordinatorNotes(st)
	if len(notes) != 1 || !strings.HasSuffix(notes[0], "Left, their dispatch still in flight: "+extra.ID+"; repeat once they start.") {
		t.Fatalf("notes %q", notes)
	}
	if st.Items[extra.ID].Held || st.Attempts[st.Items[extra.ID].Attempt].State != "dispatching" {
		t.Fatalf("the dispatch in flight was stopped: %+v", st.Items[extra.ID])
	}
}

func TestTheReturnedNoteSaysADispatchInFlightIsSettledNext(t *testing.T) {
	s, tasks := onHosts(t)
	extra := newTask(t, s, tasks[0].Repo, tasks[0].Plan)
	call(t, s, op, Request{Op: "claim", ID: extra.ID, Body: "devbox", State: "claude"})
	unreachableFor(t, &s, stretch, UnreachableAsk)
	if err := s.SawReach("devbox", true, everyWindowAlive); err != nil {
		t.Fatal(err)
	}
	notes := coordinatorNotes(mustSnapshot(t, s))
	if len(notes) != 1 || !strings.HasSuffix(notes[0], extra.ID+": its dispatch there is settled by the next pass.") {
		t.Fatalf("notes %q", notes)
	}
}
