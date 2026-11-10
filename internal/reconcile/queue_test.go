package reconcile

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
)

// showStep is a step's outcome: the entry's new phase or end, then the
// update, notice and completion it makes.
func showStep(s control.QueueStep) []string {
	out := []string{"end"}
	if s.To != nil {
		out[0] = s.To.Phase
	}
	for _, f := range []struct {
		on   bool
		text string
	}{{s.Kind != "", s.Kind}, {s.Notice != "", "notice"}, {s.Done, "done"}} {
		if f.on {
			out = append(out, f.text)
		}
	}
	return out
}

// queued is task n's entry, at seq, in-review with its request made an hour
// before t0, head for a minute if it is the head, and never checked.
func queued(n, seq int, phase string) QueueItem {
	id := fmt.Sprintf("t%d", n)
	return QueueItem{
		QueueEntry: control.QueueEntry{Task: id, Repo: "r1", Request: seq, Seq: seq, Phase: phase, PR: fmt.Sprintf("https://github.com/o/r/pull/%d", n),
			Head: strings.Repeat("a", 40), HeadAt: t0.Add(-time.Minute)},
		Request: control.Update{ID: seq, Item: id, Kind: "merge-request", At: t0.Add(-time.Hour), Merge: &control.MergeRequest{Method: "squash"}},
		State:   "in-review", Host: "devbox",
	}
}

func asked(q QueueItem) QueueItem                    { q.Question = 7; return q }
func stopped(q QueueItem) QueueItem                  { q.Stopped = true; return q }
func checked(q QueueItem, d time.Duration) QueueItem { q.CheckedAt = t0.Add(-d); return q }
func turned(q QueueItem, d time.Duration) QueueItem {
	q.Phase, q.TurnAt = control.QueueRefresh, t0.Add(-d)
	return q
}
func inState(q QueueItem, s string) QueueItem { q.State = s; return q }

func TestDecideQueue(t *testing.T) {
	waitingQ, check := control.QueueWaiting, control.QueueCheck
	for _, c := range []struct {
		name    string
		entries []QueueItem
		want    []string
	}{
		{"an empty queue does nothing", nil, nil},
		{"the first eligible entry becomes head", []QueueItem{asked(queued(3, 3, waitingQ)), queued(2, 5, waitingQ)}, []string{"queue t2 check"}},
		{"the queue is first come, first served", []QueueItem{queued(3, 3, waitingQ), queued(2, 5, waitingQ)}, []string{"queue t3 check"}},
		{"a stopped root's entry is skipped", []QueueItem{stopped(queued(3, 1, waitingQ)), queued(2, 2, waitingQ)}, []string{"queue t2 check"}},
		{"a task not in-review is skipped", []QueueItem{inState(queued(3, 1, waitingQ), "running"), queued(2, 2, waitingQ)}, []string{"queue t2 check"}},
		{"nothing is eligible", []QueueItem{asked(queued(3, 1, waitingQ))}, nil},
		{"the head is merged; an earlier entry never preempts it", []QueueItem{queued(3, 3, waitingQ), queued(2, 9, check)}, []string{"merge t2"}},
		{"a head never checked is called at once", []QueueItem{queued(2, 2, check)}, []string{"merge t2"}},
		{"a head checked within the interval is not called", []QueueItem{checked(queued(2, 2, check), CheckEvery-time.Second)}, nil},
		{"a head is called again once the interval passes", []QueueItem{checked(queued(2, 2, check), CheckEvery)}, []string{"merge t2"}},
		{"waiting entries are never called", []QueueItem{checked(queued(2, 2, check), time.Second), queued(3, 3, waitingQ), queued(4, 4, waitingQ)}, nil},
		{"a head that gets a question gives up the head and keeps its place", []QueueItem{asked(queued(2, 2, check)), queued(3, 3, waitingQ)}, []string{"queue t2 queued"}},
		{"a head whose root stops gives up the head", []QueueItem{stopped(queued(2, 2, check))}, []string{"queue t2 queued"}},
		{"a head refreshing in time is left alone", []QueueItem{turned(queued(2, 2, check), TurnWait-time.Second)}, nil},
		{"a head that does not refresh in time is refused", []QueueItem{turned(queued(2, 2, check), TurnWait)}, []string{"queue t2 end merge-refused notice"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := shows(DecideQueue(QueueView{Now: t0, Entries: c.entries}))
			if want := strings.Join(c.want, "\n"); got != want {
				t.Errorf("got\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func said(code, reason string) Probe { return Probe{Reply: hostops.Merged{Code: code, Reason: reason}} }

func TestAnswerRoutesEveryMergeOutcome(t *testing.T) {
	head := queued(2, 2, control.QueueCheck)
	recent := head
	recent.HeadAt, recent.Request.At = t0.Add(-2*ChecksWait), t0.Add(-time.Minute)
	unknown := head
	unknown.WaitSince = t0.Add(-MergeableWait)
	early := head
	early.WaitSince = t0.Add(-MergeableWait + time.Second)
	away := head
	away.FailSince = t0.Add(-HostWait)
	briefly := head
	briefly.FailSince = t0.Add(-HostWait + time.Second)
	failing := head
	failing.Failures = GhFailures - 1
	recovered := head
	recovered.Failures = 1
	for _, c := range []struct {
		name string
		q    QueueItem
		p    Probe
		want string
	}{
		{"merged completes the task", head, said(hostops.CodeMerged, "merged at x"), "queue t2 end merged done"},
		{"merged elsewhere completes it and tells the operator", head, said(hostops.CodeMergedElsewhere, "merged at y"), "queue t2 end merged notice done"},
		{"behind gives the head its turn", head, said(hostops.CodeBehind, "main moved"), "queue t2 refresh merge-turn"},
		{"a conflict gives the head its turn", head, said(hostops.CodeConflict, "conflicts"), "queue t2 refresh merge-turn"},
		{"a moved head gives the head its turn", head, said(hostops.CodeHeadMoved, "head is b"), "queue t2 refresh merge-turn"},
		{"red refuses it for the executor", head, said(hostops.CodeRed, "check test"), "queue t2 end merge-refused"},
		{"closed refuses it and tells the operator", head, said(hostops.CodeClosed, "closed"), "queue t2 end merge-refused notice"},
		{"a branch rule refuses it and tells the operator", head, said(hostops.CodeBlocked, "review"), "queue t2 end merge-refused notice"},
		{"checks pending in time wait", head, said(hostops.CodePendingChecks, "running"), "queue t2 check"},
		{"checks pending too long refuse it", headFor(head, ChecksWait), said(hostops.CodePendingChecks, "running"), "queue t2 end merge-refused notice"},
		{"a recent request restarts the checks wait", recent, said(hostops.CodePendingChecks, "running"), "queue t2 check"},
		{"unknown mergeability starts its wait", head, said(hostops.CodePendingMergeable, "?"), "queue t2 check"},
		{"unknown mergeability in time waits", early, said(hostops.CodePendingMergeable, "?"), "queue t2 check"},
		{"unknown mergeability too long refuses it", unknown, said(hostops.CodePendingMergeable, "?"), "queue t2 end merge-refused notice"},
		{"an unreachable host starts its wait", head, Probe{Unreachable: true}, "queue t2 check"},
		{"an unreachable host in time waits", briefly, Probe{Unreachable: true}, "queue t2 check"},
		{"an unreachable host too long refuses it", away, Probe{Unreachable: true}, "queue t2 end merge-refused notice"},
		{"a gh failure is counted", head, Probe{Err: "gh: auth"}, "queue t2 check"},
		{"repeated gh failures refuse it", failing, Probe{Err: "gh: auth"}, "queue t2 end merge-refused notice"},
		{"a good reply clears the failures", recovered, said(hostops.CodePendingChecks, "running"), "queue t2 check"},
		{"a missing host refuses it", head, Probe{Missing: true}, "queue t2 end merge-refused notice"},
		{"an unreadable reply waits like pending checks", head, Probe{}, "queue t2 check"},
		{"an unreadable reply is bounded too", headFor(head, ChecksWait), Probe{}, "queue t2 end merge-refused notice"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := shows(Answer(t0, c.q, c.p)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// headFor is q having been head for d, with its request older.
func headFor(q QueueItem, d time.Duration) QueueItem {
	q.HeadAt = t0.Add(-d)
	q.Request.At = t0.Add(-2 * d)
	return q
}

func TestQueueStepsCarryTheirTimes(t *testing.T) {
	head := queued(2, 2, control.QueueCheck)
	turn := Answer(t0, head, said(hostops.CodeBehind, "main moved"))[0].Step
	if turn.To.TurnAt != t0 || turn.To.CheckedAt != t0 || !strings.Contains(turn.Text, "main moved") || !strings.Contains(turn.Text, "merge-ready") {
		t.Errorf("turn %+v", turn)
	}
	promote := DecideQueue(QueueView{Now: t0, Entries: []QueueItem{queued(3, 3, control.QueueWaiting)}})[0].Step
	if promote.To.HeadAt != t0 || promote.From.Phase != control.QueueWaiting || !promote.To.CheckedAt.IsZero() {
		t.Errorf("promotion %+v", promote)
	}
	unhead := DecideQueue(QueueView{Now: t0, Entries: []QueueItem{asked(checked(head, time.Second))}})[0].Step
	if !unhead.To.CheckedAt.IsZero() || !unhead.To.HeadAt.IsZero() {
		t.Errorf("unheaded %+v", unhead.To)
	}
	wait := Answer(t0, head, Probe{Unreachable: true})[0].Step
	if wait.To.FailSince != t0 || wait.To.CheckedAt != t0 {
		t.Errorf("unreachable %+v", wait.To)
	}
	pending := Answer(t0, checked(head, CheckEvery), said(hostops.CodePendingChecks, "running"))[0].Step
	if pending.To.CheckedAt != t0 {
		t.Errorf("pending %+v", pending.To)
	}
}

// Every way the queue gives up on a head refuses it: the executor is told
// why and what to do, and the operator why.
func TestQueueRefusalsTellBoth(t *testing.T) {
	head := queued(2, 2, control.QueueCheck)
	failing := head
	failing.Failures = GhFailures - 1
	away := head
	away.FailSince = t0.Add(-HostWait)
	unknown := head
	unknown.WaitSince = t0.Add(-MergeableWait)
	for _, c := range []struct {
		name string
		acts []Action
		want []string
	}{
		{"refresh", DecideQueue(QueueView{Now: t0, Entries: []QueueItem{turned(head, TurnWait)}}), []string{"not refreshed", TurnWait.String()}},
		{"checks", Answer(t0, headFor(head, ChecksWait), said(hostops.CodePendingChecks, "running")), []string{"checks were pending", "running"}},
		{"mergeability", Answer(t0, unknown, said(hostops.CodePendingMergeable, "?")), []string{"mergeable", MergeableWait.String()}},
		{"host", Answer(t0, away, Probe{Unreachable: true}), []string{"devbox", "unreachable"}},
		{"gh", Answer(t0, failing, Probe{Err: "gh: auth"}), []string{"gh: auth", "devbox", "gh auth status"}},
		{"missing", Answer(t0, head, Probe{Missing: true}), []string{"devbox", "hosts.toml"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := c.acts[0].Step
			if s.Kind != control.MergeRefused || s.To != nil || !strings.Contains(s.Text, "request the merge again") ||
				!strings.Contains(s.Notice, head.PR) {
				t.Fatalf("step %+v", s)
			}
			for _, w := range c.want {
				if !strings.Contains(s.Text, w) {
					t.Errorf("text %q lacks %q", s.Text, w)
				}
			}
		})
	}
}
