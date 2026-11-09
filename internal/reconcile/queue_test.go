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
// update, notice, hold, demotion and completion it makes.
func showStep(s control.QueueStep) []string {
	out := []string{"end"}
	if s.To != nil {
		out[0] = s.To.Phase
	}
	for _, f := range []struct {
		on   bool
		text string
	}{{s.Kind != "", s.Kind}, {s.Notice != "", "notice"}, {s.Hold, "hold"}, {s.Demote, "demote"}, {s.Done, "done"}} {
		if f.on {
			out = append(out, f.text)
		}
	}
	return out
}

// queued is task n's entry, at seq, in-review with its request made an hour
// before t0, head for a minute if it is the head, and its PR read just now.
func queued(n, seq int, phase string) QueueItem {
	id := fmt.Sprintf("t%d", n)
	return QueueItem{
		QueueEntry: control.QueueEntry{Task: id, Repo: "r1", Request: seq, Seq: seq, Phase: phase, PR: fmt.Sprintf("https://github.com/o/r/pull/%d", n),
			Head: strings.Repeat("a", 40), ProbedAt: t0, HeadAt: t0.Add(-time.Minute)},
		Request: control.Update{ID: seq, Item: id, Kind: "merge-request", At: t0.Add(-time.Hour), Merge: &control.MergeRequest{Method: "squash"}},
		State:   "in-review", Host: "devbox",
	}
}

func asked(q QueueItem) QueueItem                   { q.Question = 7; return q }
func stopped(q QueueItem) QueueItem                 { q.Stopped = true; return q }
func heldBack(q QueueItem) QueueItem                { q.Held = true; return q }
func probed(q QueueItem, d time.Duration) QueueItem { q.ProbedAt = t0.Add(-d); return q }
func turned(q QueueItem, d time.Duration) QueueItem {
	q.Phase, q.TurnAt = control.QueueRefresh, t0.Add(-d)
	return q
}
func demoted(q QueueItem, n int) QueueItem      { q.Demotions = n; return q }
func inState(q QueueItem, s string) QueueItem   { q.State = s; return q }
func told(q QueueItem, reason string) QueueItem { q.Reason = reason; return q }

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
		{"a held entry is skipped", []QueueItem{heldBack(queued(3, 1, waitingQ)), queued(2, 2, waitingQ)}, []string{"queue t2 check"}},
		{"a stopped root's entry is skipped", []QueueItem{stopped(queued(3, 1, waitingQ)), queued(2, 2, waitingQ)}, []string{"queue t2 check"}},
		{"a task not in-review is skipped", []QueueItem{inState(queued(3, 1, waitingQ), "running"), queued(2, 2, waitingQ)}, []string{"queue t2 check"}},
		{"nothing is eligible", []QueueItem{asked(queued(3, 1, waitingQ))}, nil},
		{"the head is merged; an earlier entry never preempts it", []QueueItem{queued(3, 3, waitingQ), queued(2, 9, check)}, []string{"merge t2"}},
		{"a head that gets a question gives up the head and keeps its place", []QueueItem{asked(queued(2, 2, check)), queued(3, 3, waitingQ)}, []string{"queue t2 queued"}},
		{"a head whose root stops gives up the head", []QueueItem{stopped(queued(2, 2, check))}, []string{"queue t2 queued"}},
		{"a head refreshing in time is left alone", []QueueItem{turned(queued(2, 2, check), TurnWait-time.Second)}, nil},
		{"a head that does not refresh in time is demoted", []QueueItem{turned(queued(2, 2, check), TurnWait)}, []string{"queue t2 queued merge-demoted notice demote"}},
		{"too many demotions drop the entry", []QueueItem{demoted(turned(queued(2, 2, check), TurnWait), MaxDemotions-1)}, []string{"queue t2 end merge-refused notice"}},
		{"a waiting entry is read when due", []QueueItem{queued(2, 2, check), probed(queued(3, 3, waitingQ), ProbeEvery)}, []string{"merge t2", "merge t3 check"}},
		{"a waiting entry read recently is not", []QueueItem{queued(2, 2, check), probed(queued(3, 3, waitingQ), ProbeEvery-time.Second)}, []string{"merge t2"}},
		{"held and stopped entries are not read", []QueueItem{queued(2, 2, check), heldBack(probed(queued(3, 3, waitingQ), time.Hour)), stopped(probed(queued(4, 4, waitingQ), time.Hour))},
			[]string{"merge t2"}},
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
	head, waitingQ := queued(2, 2, control.QueueCheck), control.QueueWaiting
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
		{"red drops it for the executor", head, said(hostops.CodeRed, "check test"), "queue t2 end merge-refused"},
		{"closed drops it and tells the operator", head, said(hostops.CodeClosed, "closed"), "queue t2 end merge-refused notice"},
		{"a branch rule drops it and tells the operator", head, said(hostops.CodeBlocked, "review"), "queue t2 end merge-refused notice"},
		{"checks pending in time wait", head, said(hostops.CodePendingChecks, "running"), ""},
		{"checks pending too long demote it", demotedAt(head, ChecksWait), said(hostops.CodePendingChecks, "running"), "queue t2 queued merge-demoted notice demote"},
		{"a recent request restarts the checks wait", recent, said(hostops.CodePendingChecks, "running"), ""},
		{"unknown mergeability starts its wait", head, said(hostops.CodePendingMergeable, "?"), "queue t2 check"},
		{"unknown mergeability in time waits", early, said(hostops.CodePendingMergeable, "?"), ""},
		{"unknown mergeability too long demotes it", unknown, said(hostops.CodePendingMergeable, "?"), "queue t2 queued merge-demoted notice demote"},
		{"an unreachable host starts its wait", head, Probe{Unreachable: true}, "queue t2 check"},
		{"an unreachable host in time waits", briefly, Probe{Unreachable: true}, ""},
		{"an unreachable host too long demotes it", away, Probe{Unreachable: true}, "queue t2 queued merge-demoted notice demote"},
		{"a gh failure is counted", head, Probe{Err: "gh: auth"}, "queue t2 check"},
		{"repeated gh failures hold it for the operator", failing, Probe{Err: "gh: auth"}, "queue t2 queued notice hold"},
		{"a good reply clears the failures", recovered, said(hostops.CodePendingChecks, "running"), "queue t2 check"},
		{"a missing host holds it for the operator", head, Probe{Missing: true}, "queue t2 queued notice hold"},
		{"an unreadable reply waits like pending checks", head, Probe{}, ""},
		{"an unreadable reply is bounded too", demotedAt(head, ChecksWait), Probe{}, "queue t2 queued merge-demoted notice demote"},
		{"a probe tells a moved head once", queued(3, 3, waitingQ), said(hostops.CodeHeadMoved, "head is b"), "queue t3 queued say"},
		{"a probe does not repeat it", told(queued(3, 3, waitingQ), "head is b"), said(hostops.CodeHeadMoved, "head is b"), "queue t3 queued"},
		{"a probe finds a merged PR", queued(3, 3, waitingQ), said(hostops.CodeMerged, "merged"), "queue t3 end merged done"},
		{"a probe finds a red PR", queued(3, 3, waitingQ), said(hostops.CodeRed, "check test"), "queue t3 end merge-refused"},
		{"a probe of a ready PR records the read", queued(3, 3, waitingQ), said(hostops.CodeReady, "ok"), "queue t3 queued"},
		{"a probe leaves a behind PR to its turn", queued(3, 3, waitingQ), said(hostops.CodeBehind, "moved"), "queue t3 queued"},
		{"a failed probe records the read", queued(3, 3, waitingQ), Probe{Unreachable: true}, "queue t3 queued"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := shows(Answer(t0, c.q, c.p)); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// demotedAt is q having been head for d, with its request older.
func demotedAt(q QueueItem, d time.Duration) QueueItem {
	q.HeadAt = t0.Add(-d)
	q.Request.At = t0.Add(-2 * d)
	return q
}

func TestQueueStepsCarryTheirTimes(t *testing.T) {
	head := queued(2, 2, control.QueueCheck)
	turn := Answer(t0, head, said(hostops.CodeBehind, "main moved"))[0].Step
	if turn.To.TurnAt != t0 || !strings.Contains(turn.Text, "main moved") || !strings.Contains(turn.Text, "merge-ready") {
		t.Errorf("turn %+v", turn)
	}
	promote := DecideQueue(QueueView{Now: t0, Entries: []QueueItem{queued(3, 3, control.QueueWaiting)}})[0].Step
	if promote.To.HeadAt != t0 || promote.From.Phase != control.QueueWaiting {
		t.Errorf("promotion %+v", promote)
	}
	demotion := DecideQueue(QueueView{Now: t0, Entries: []QueueItem{turned(head, TurnWait)}})[0].Step
	if demotion.To.Demotions != 1 || !demotion.To.TurnAt.IsZero() || !demotion.To.HeadAt.IsZero() || demotion.To.Reason == "" {
		t.Errorf("demotion %+v", demotion.To)
	}
	hold := Answer(t0, func() QueueItem { q := head; q.Failures = GhFailures - 1; return q }(), Probe{Err: "gh: auth"})[0].Step
	if !strings.Contains(hold.Notice, "gh auth status") || !strings.Contains(hold.Notice, "devbox") || hold.To.Failures != 0 {
		t.Errorf("hold %+v", hold)
	}
	wait := Answer(t0, head, Probe{Unreachable: true})[0].Step
	if wait.To.FailSince != t0 {
		t.Errorf("unreachable %+v", wait.To)
	}
	probe := Answer(t0, probed(queued(3, 3, control.QueueWaiting), time.Hour), Probe{Err: "x"})[0].Step
	if probe.To.ProbedAt != t0 {
		t.Errorf("probe %+v", probe.To)
	}
}
