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

// onClaude is onHosts with two more tasks running claude on devbox: t1, t4
// and t5 claude on devbox, t2 codex on devbox, t3 claude on spare.
func onClaude(t *testing.T) (Service, []Item) {
	t.Helper()
	s := service(t)
	s.Hosts = func() HostStatus {
		return HostStatus{At: stretch, Hosts: []HostRow{{Name: "devbox"}, {Name: "mac", Unreachable: "ssh: exit 255"}, {Name: "spare"}}}
	}
	repo, plan := repoPlan(t, s)
	var tasks []Item
	for range 5 {
		tasks = append(tasks, newTask(t, s, repo.ID, plan.ID))
	}
	approve(t, s, plan)
	for i, where := range [][2]string{{"devbox", "claude"}, {"devbox", "codex"}, {"spare", "claude"}, {"devbox", "claude"}, {"devbox", "claude"}} {
		out := call(t, s, op, Request{Op: "claim", ID: tasks[i].ID, Body: where[0], State: where[1]})
		launched(t, s, out.Attempt.ID)
	}
	return s, tasks
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
	s, tasks := onClaude(t)
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
	s2, tasks2 := onClaude(t)
	sawLimits(t, s2, one(tasks2[0].ID, false))
	if q := fleetQuestions(mustSnapshot(t, s2))[0]; *q.Recommended != 1 {
		t.Errorf("recommended %d", *q.Recommended)
	}
}

func TestALimitedSessionWithNoRoomIsNotAsked(t *testing.T) {
	s, tasks := onClaude(t)
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
	s, tasks := onClaude(t)
	ids := []string{tasks[0].ID, tasks[3].ID}
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
	s, tasks := onClaude(t)
	t1, t2, t3 := tasks[0].ID, tasks[4].ID, tasks[3].ID
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
	s, tasks := onClaude(t)
	t1, t3 := tasks[0].ID, tasks[3].ID
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
	s, tasks := onClaude(t)
	t1, t3 := tasks[0].ID, tasks[3].ID
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
// tasks, t1, t4 and t5, off it, and only those: t2 runs codex on devbox and
// t3 claude on spare.
func movedSeveral(t *testing.T, st State, ts []Item) {
	if !st.Hosts["devbox"].Paused {
		t.Errorf("devbox %+v", st.Hosts["devbox"])
	}
	for i, task := range ts {
		at := st.Attempts[st.Items[task.ID].Attempt]
		if want := i == 0 || i > 2; (at.Reason == EndMoved && at.MoveHost == "") != want {
			t.Errorf("%s's attempt %+v, want moved %v", task.ID, at, want)
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
		"move several": {read: func(ts []Item) LimitRead { return several(ts[0].ID, ts[3].ID) }, answer: "Move them to other hosts",
			check: movedSeveral,
			note:  "As answered in update %d, the coordinator moved %s on devbox. devbox is paused until `engine host resume devbox`."},
	} {
		t.Run(name, func(t *testing.T) {
			s, tasks := onClaude(t)
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
				moved := tasks[0].ID
				if name == "move several" {
					moved = tasks[0].ID + ", " + tasks[3].ID + ", " + tasks[4].ID
				}
				want = []string{fmt.Sprintf(c.note, by, moved)}
			}
			if notes := coordinatorNotes(st); !slices.Equal(notes, want) {
				t.Errorf("notes %q, want %q", notes, want)
			}
			// A wait keeps the answered question covering its session; a move
			// ends the stretch, its tasks gone from the host × harness.
			ask, kept := st.Hosts["devbox"].Limits["claude"]
			if got := fleetQuestions(st); len(got) != 1 || kept != (c.note == "") || kept && !ask.Acted {
				t.Errorf("questions %d, record %+v", len(got), st.Hosts["devbox"])
			}
		})
	}
}

func TestARefusedMoveTellsTheOperator(t *testing.T) {
	s, tasks := onClaude(t)
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

// A read taken before an answer moved its task asks nothing about that
// task: the move runs, the read loses it, and the next read asks afresh.
func TestAReadThatAMoveOvertookAsksNothingStale(t *testing.T) {
	s, tasks := onClaude(t)
	t1, t4 := tasks[0].ID, tasks[3].ID
	sawLimits(t, s, one(t1, true))
	q := fleetQuestions(mustSnapshot(t, s))[0]
	call(t, s, op, Request{Op: "resolve", ReplyTo: q.ID, Body: "Move to claude on spare"})
	sawLimits(t, s, several(t1, t4))
	st := mustSnapshot(t, s)
	if qs := fleetQuestions(st); len(qs) != 1 {
		t.Fatalf("asked on a stale read: %+v", qs[1:])
	}
	if at := st.Attempts[st.Items[t1].Attempt]; at.Reason != EndMoved {
		t.Fatalf("t1 not moved: %+v", at)
	}
	r := one(t4, true)
	sawLimits(t, s, r)
	qs := fleetQuestions(mustSnapshot(t, s))
	if len(qs) != 2 || !strings.HasPrefix(qs[1].Body, t4+" is at claude's limit") {
		t.Errorf("questions %+v", qs)
	}
}
