package control

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// resetIn is a reset d after stretch, in epoch seconds.
func resetIn(d time.Duration) int64 { return stretch.Add(d).Unix() }

// one is a read of task id alone limited on devbox's claude, with room on
// spare's claude.
func one(id string, wait bool) LimitRead {
	return LimitRead{Host: "devbox", Harness: "claude", Tasks: []string{id}, ResetsAt: resetIn(time.Hour), Room: true,
		MoveHost: "spare", MoveHarness: "claude", Wait: wait}
}

// several is a read of ids limited on devbox's claude, with room elsewhere.
func several(ids ...string) LimitRead {
	return LimitRead{Host: "devbox", Harness: "claude", Tasks: ids, ResetsAt: resetIn(3 * time.Hour), Room: true}
}

func sawLimits(t *testing.T, s Service, reads ...LimitRead) {
	t.Helper()
	if err := s.SawLimits("devbox", reads); err != nil {
		t.Fatal(err)
	}
}

// resolutions are the bodies of the updates resolving question q.
func resolutions(st State, q int) []string {
	var out []string
	for _, u := range st.Updates {
		if u.ReplyTo == q && u.Kind == "resolved" {
			out = append(out, u.Body)
		}
	}
	return out
}

func TestALimitedSessionWithRoomGetsOneQuestionHoldingNoTask(t *testing.T) {
	s, tasks := onHosts(t)
	s.Now = func() time.Time { return stretch }
	t1 := tasks[0].ID
	for range 3 {
		sawLimits(t, s, one(t1, true))
	}
	st := mustSnapshot(t, s)
	qs := fleetQuestions(st)
	if len(qs) != 1 {
		t.Fatalf("questions %+v", qs)
	}
	q := qs[0]
	want := fmt.Sprintf("%s is at claude's limit on devbox until 2026-11-22 10:00 UTC.", t1)
	if !strings.HasPrefix(q.Body, want) || !strings.Contains(q.Body, "`engine task move "+t1+" --harness claude --host spare`") {
		t.Errorf("body %q", q.Body)
	}
	if !slices.Equal(q.Choices, []string{ChoiceWaitReset, "Move to claude on spare"}) || *q.Recommended != 0 || q.Item != "" || q.Host != "devbox" || q.Class != "environment" {
		t.Errorf("question %+v", q)
	}
	if st.Items[t1].State != "running" {
		t.Errorf("the question held %s: %s", t1, st.Items[t1].State)
	}
	inbox := call(t, s, op, Request{Op: "inbox"}).Updates
	if !slices.ContainsFunc(inbox, func(u Update) bool { return u.ID == q.ID }) {
		t.Errorf("not in the inbox: %+v", inbox)
	}
	if ask := st.Hosts["devbox"].Limits["claude"]; ask.Question != q.ID || !slices.Equal(ask.Tasks, []string{t1}) || ask.Group {
		t.Errorf("record %+v", ask)
	}
	// A far reset recommends the move.
	s2, tasks2 := onHosts(t)
	sawLimits(t, s2, one(tasks2[0].ID, false))
	if q := fleetQuestions(mustSnapshot(t, s2))[0]; *q.Recommended != 1 {
		t.Errorf("recommended %d", *q.Recommended)
	}
}

func TestALimitedSessionWithNoRoomIsNotAsked(t *testing.T) {
	s, tasks := onHosts(t)
	r := one(tasks[0].ID, true)
	r.Room, r.MoveHost, r.MoveHarness = false, "", ""
	sawLimits(t, s, r)
	st := mustSnapshot(t, s)
	if qs := fleetQuestions(st); len(qs) != 0 || st.Hosts["devbox"].Limits != nil {
		t.Errorf("questions %+v, record %+v", qs, st.Hosts["devbox"])
	}
	// Room that turns up later asks.
	sawLimits(t, s, one(tasks[0].ID, true))
	if qs := fleetQuestions(mustSnapshot(t, s)); len(qs) != 1 {
		t.Errorf("questions %+v", qs)
	}
}

func TestSeveralLimitedSessionsOnOneHarnessGetOneGroupedQuestion(t *testing.T) {
	s, tasks := onHosts(t)
	ids := []string{tasks[0].ID, tasks[2].ID}
	sawLimits(t, s, several(ids...))
	st := mustSnapshot(t, s)
	qs := fleetQuestions(st)
	if len(qs) != 1 {
		t.Fatalf("questions %+v", qs)
	}
	q := qs[0]
	if !strings.HasPrefix(q.Body, strings.Join(ids, ", ")+" are at claude's limit on devbox until") ||
		!strings.Contains(q.Body, "`engine host move devbox --harness claude`") {
		t.Errorf("body %q", q.Body)
	}
	if !slices.Equal(q.Choices, []string{ChoiceWaitReset, "Move them to other hosts"}) || *q.Recommended != 1 {
		t.Errorf("question %+v", q)
	}
	if ask := st.Hosts["devbox"].Limits["claude"]; !ask.Group || !slices.Equal(ask.Tasks, ids) {
		t.Errorf("record %+v", ask)
	}
}

// A session limited later joins an open grouped question, and replaces an
// open question about one session with a grouped one.
func TestALateLimitedSessionJoinsTheOpenQuestion(t *testing.T) {
	s, tasks := onHosts(t)
	t1, t2, t3 := tasks[0].ID, tasks[1].ID, tasks[2].ID
	sawLimits(t, s, one(t1, true))
	first := fleetQuestions(mustSnapshot(t, s))[0]
	sawLimits(t, s, several(t1, t3))
	st := mustSnapshot(t, s)
	qs := fleetQuestions(st)
	if len(qs) != 2 || qs[1].Choices[1] != "Move them to other hosts" {
		t.Fatalf("questions %+v", qs)
	}
	want := fmt.Sprintf("%s\nby question %d, which covers %s, %s", ResolutionSuperseded, qs[1].ID, t1, t3)
	if got := resolutions(st, first.ID); !slices.Equal(got, []string{want}) {
		t.Errorf("first question's resolutions %q", got)
	}
	// A third joins the grouped question without another.
	sawLimits(t, s, several(t1, t2, t3))
	st = mustSnapshot(t, s)
	if qs := fleetQuestions(st); len(qs) != 2 {
		t.Errorf("questions %+v", qs)
	}
	if ask := st.Hosts["devbox"].Limits["claude"]; ask.Question != qs[1].ID || !slices.Equal(ask.Tasks, []string{t1, t2, t3}) {
		t.Errorf("record %+v", ask)
	}
}

func TestALimitQuestionResolvesOnceNoSessionIsLimited(t *testing.T) {
	s, tasks := onHosts(t)
	t1, t3 := tasks[0].ID, tasks[2].ID
	sawLimits(t, s, several(t1, t3))
	q := fleetQuestions(mustSnapshot(t, s))[0]
	// One resuming leaves the question open for the other.
	sawLimits(t, s, several(t3))
	if got := resolutions(mustSnapshot(t, s), q.ID); len(got) != 0 {
		t.Fatalf("resolved early: %q", got)
	}
	sawLimits(t, s)
	sawLimits(t, s)
	st := mustSnapshot(t, s)
	if got := resolutions(st, q.ID); !slices.Equal(got, []string{ResolutionNoLimit}) {
		t.Errorf("resolutions %q", got)
	}
	if st.Hosts["devbox"].Limits != nil {
		t.Errorf("record %+v", st.Hosts["devbox"])
	}
	// The next limit is a new stretch, with a question of its own.
	sawLimits(t, s, one(t1, true))
	if qs := fleetQuestions(mustSnapshot(t, s)); len(qs) != 2 {
		t.Errorf("questions %+v", qs)
	}
}

// A session a wait covers is not asked about again while it stays limited;
// a session limited after the answer is.
func TestAnAnsweredQuestionCoversItsSessionsUntilTheirLimitEnds(t *testing.T) {
	s, tasks := onHosts(t)
	t1, t3 := tasks[0].ID, tasks[2].ID
	sawLimits(t, s, one(t1, true))
	q := fleetQuestions(mustSnapshot(t, s))[0]
	call(t, s, op, Request{Op: "resolve", ReplyTo: q.ID, Body: ChoiceWaitReset})
	sawLimits(t, s, one(t1, true))
	sawLimits(t, s, one(t1, true))
	if qs := fleetQuestions(mustSnapshot(t, s)); len(qs) != 1 {
		t.Fatalf("asked again: %+v", qs)
	}
	sawLimits(t, s, several(t1, t3))
	qs := fleetQuestions(mustSnapshot(t, s))
	if len(qs) != 2 || !strings.HasPrefix(qs[1].Body, t1+", "+t3+" are at") {
		t.Errorf("questions %+v", qs)
	}
}

// movedOne checks that the answer moved t1 alone, to claude on spare.
func movedOne(t *testing.T, st State, ts []Item) {
	at := st.Attempts[st.Items[ts[0].ID].Attempt]
	if at.Reason != EndMoved || at.MoveHost != "spare" || at.MoveHarness != "claude" || at.MovedBlind {
		t.Errorf("t1's attempt %+v", at)
	}
	if st.Hosts["devbox"].Paused || st.Attempts[st.Items[ts[1].ID].Attempt].State == "ended" {
		t.Errorf("moved more than t1")
	}
}

// movedSeveral checks that the answer paused devbox and moved its claude
// task, t1, off it: t2 runs codex on devbox and t3 claude on spare.
func movedSeveral(t *testing.T, st State, ts []Item) {
	at := st.Attempts[st.Items[ts[0].ID].Attempt]
	if at.Reason != EndMoved || at.MoveHost != "" || !st.Hosts["devbox"].Paused {
		t.Errorf("t1's attempt %+v, devbox %+v", at, st.Hosts["devbox"])
	}
	for _, other := range ts[1:] {
		if st.Attempts[st.Items[other.ID].Attempt].State == "ended" {
			t.Errorf("%s moved", other.ID)
		}
	}
}

func TestEachAnswerToALimitQuestionRunsOnce(t *testing.T) {
	for name, c := range map[string]struct {
		read   func([]Item) LimitRead
		answer string
		check  func(*testing.T, State, []Item)
		note   string
	}{
		"wait": {read: func(ts []Item) LimitRead { return one(ts[0].ID, true) }, answer: ChoiceWaitReset,
			check: waited},
		"free text": {read: func(ts []Item) LimitRead { return one(ts[0].ID, true) }, answer: "let me look first",
			check: waited},
		"move one": {read: func(ts []Item) LimitRead { return one(ts[0].ID, true) }, answer: "Move to claude on spare\nnow please",
			check: movedOne,
			note:  "As answered in update %d, the coordinator moved %s to claude on spare."},
		"move several": {read: func(ts []Item) LimitRead { return several(ts[0].ID, ts[2].ID) }, answer: "Move them to other hosts",
			check: movedSeveral,
			note:  "As answered in update %d, the coordinator moved %s on devbox. devbox is paused until `engine host resume devbox`."},
	} {
		t.Run(name, func(t *testing.T) {
			s, tasks := onHosts(t)
			read := c.read(tasks)
			sawLimits(t, s, read)
			q := fleetQuestions(mustSnapshot(t, s))[0]
			by := call(t, s, op, Request{Op: "resolve", ReplyTo: q.ID, Body: c.answer}).Updates[0].ID
			for range 3 {
				sawLimits(t, s, read)
			}
			st := mustSnapshot(t, s)
			c.check(t, st, tasks)
			var want []string
			if c.note != "" {
				want = []string{fmt.Sprintf(c.note, by, tasks[0].ID)}
			}
			if notes := coordinatorNotes(st); !slices.Equal(notes, want) {
				t.Errorf("notes %q, want %q", notes, want)
			}
			if got := fleetQuestions(st); len(got) != 1 || !st.Hosts["devbox"].Limits["claude"].Acted {
				t.Errorf("questions %d, record %+v", len(got), st.Hosts["devbox"])
			}
		})
	}
}

func TestARefusedMoveTellsTheOperator(t *testing.T) {
	s, tasks := onHosts(t)
	t1 := tasks[0].ID
	read := one(t1, true)
	read.MoveHost = "gone"
	sawLimits(t, s, read)
	q := fleetQuestions(mustSnapshot(t, s))[0]
	by := call(t, s, op, Request{Op: "resolve", ReplyTo: q.ID, Body: "Move to claude on gone"}).Updates[0].ID
	sawLimits(t, s, read)
	sawLimits(t, s, read)
	st := mustSnapshot(t, s)
	want := fmt.Sprintf("As answered in update %d, the coordinator tried to move %s, but the server refused: host gone is not in hosts.toml.", by, t1)
	if notes := coordinatorNotes(st); !slices.Equal(notes, []string{want}) {
		t.Errorf("notes %q", notes)
	}
	if st.Attempts[st.Items[t1].Attempt].State == "ended" {
		t.Errorf("t1 moved")
	}
}
