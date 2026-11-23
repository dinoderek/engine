package controlrun

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// outageRig is a coordinator with one running task whose session waits, a
// clock the test moves, and a record of the outage wakes typed.
type outageRig struct {
	c     *Coordinator
	h     *host
	task  control.Item
	now   time.Time
	wakes []string
}

func newOutageRig(t *testing.T) *outageRig {
	t.Helper()
	c, h, _, _, task := setup(t)
	r := &outageRig{c: c, h: h, task: task, now: c.Service.Now()}
	c.Service.Now = func() time.Time { return r.now }
	c.Wake = func(_ reconcile.Session, text string) error {
		if strings.HasPrefix(text, "The engine server was unavailable") {
			r.wakes = append(r.wakes, text)
		}
		return nil
	}
	pass(t, c)
	pass(t, c)
	h.status.Windows[0].State = session.StateWaiting
	pass(t, c)
	return r
}

// after moves the clock on by d and runs one pass.
func (r *outageRig) after(t *testing.T, d time.Duration) {
	t.Helper()
	r.now = r.now.Add(d)
	pass(t, r.c)
}

func (r *outageRig) attempt(t *testing.T) control.Attempt {
	t.Helper()
	st, err := snapshotOf(r.c.Service.Store)
	if err != nil {
		t.Fatal(err)
	}
	return st.Attempts[st.Items[r.task.ID].Attempt]
}

func TestAGapOverFiveMinutesWakesAnIdleRunningSessionOnceAndRestartsItsStalls(t *testing.T) {
	r := newOutageRig(t)
	before := r.attempt(t)
	if before.IdleSince.IsZero() || before.WaitingSince.IsZero() {
		t.Fatalf("attempt %+v, want its stall timers running", before)
	}
	gapStart := r.now
	r.after(t, OutageGap+time.Second)
	if len(r.wakes) != 1 {
		t.Fatalf("woke %q, want one wake", r.wakes)
	}
	want := "The engine server was unavailable from " + gapStart.Format(time.RFC3339) + " to " + r.now.Format(time.RFC3339) + ". Retry any engine command that failed in that time, with its request key."
	if r.wakes[0] != want {
		t.Errorf("wake %q, want %q", r.wakes[0], want)
	}
	at := r.attempt(t)
	if !at.IdleSince.Equal(r.now) || !at.WaitingSince.Equal(r.now) {
		t.Errorf("stall timers %v and %v, want both restarted at %v", at.WaitingSince, at.IdleSince, r.now)
	}
	r.after(t, time.Minute)
	r.after(t, time.Minute)
	if len(r.wakes) != 1 {
		t.Errorf("woke %d times across later passes, want once", len(r.wakes))
	}
}

func TestAWakeThatFailsIsRetriedAndThenSentOnce(t *testing.T) {
	r := newOutageRig(t)
	failing := true
	r.c.Wake = func(_ reconcile.Session, text string) error {
		if failing {
			return errors.New("tmux: no server")
		}
		r.wakes = append(r.wakes, text)
		return nil
	}
	r.after(t, OutageGap+time.Second)
	r.after(t, time.Minute)
	if len(r.wakes) != 0 {
		t.Fatalf("woke %q while the wake failed", r.wakes)
	}
	failing = false
	r.after(t, time.Minute)
	r.after(t, time.Minute)
	if len(r.wakes) != 1 {
		t.Errorf("woke %d times after the wake worked, want once", len(r.wakes))
	}
}

func TestAnOutageAlreadyWokenForIsNotWokenForAgain(t *testing.T) {
	r := newOutageRig(t)
	r.after(t, OutageGap+time.Second)
	// A pass that fails and repeats sees the same gap again.
	r.c.outage = &outage{from: r.now.Add(-OutageGap - time.Second), to: r.now}
	r.after(t, time.Second)
	if len(r.wakes) != 1 {
		t.Errorf("woke %d times for one gap, want once", len(r.wakes))
	}
}

func TestAGapOfFiveMinutesOrLessWakesNobodyAndKeepsItsStalls(t *testing.T) {
	r := newOutageRig(t)
	before := r.attempt(t)
	r.after(t, OutageGap)
	if len(r.wakes) != 0 {
		t.Errorf("woke %q after a gap of exactly the threshold", r.wakes)
	}
	if at := r.attempt(t); !at.IdleSince.Equal(before.IdleSince) || !at.WaitingSince.Equal(before.WaitingSince) {
		t.Errorf("stall timers %v and %v moved from %v and %v", at.WaitingSince, at.IdleSince, before.WaitingSince, before.IdleSince)
	}
}

func TestTheFirstPassWithNoSavedTimeWakesNobody(t *testing.T) {
	c, _, _, _, _ := setup(t)
	woke := 0
	c.Wake = func(reconcile.Session, string) error { woke++; return nil }
	pass(t, c)
	if woke != 0 || c.outage != nil {
		t.Errorf("woke %d times, outage %v, want neither on a first pass", woke, c.outage)
	}
}

func TestAnOutageDoesNotWakeSessionsOutsideRunningOrAtADialog(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *outageRig)
	}{
		{"in review", func(t *testing.T, r *outageRig) {
			claim(t, r.c, r.task)
			st, _ := snapshotOf(r.c.Service.Store)
			item := st.Items[r.task.ID]
			execute2(t, r.c, item.Attempt, control.Request{Op: "state", ID: item.ID, Version: item.Version, State: "in-review"})
		}},
		{"needs-human", func(t *testing.T, r *outageRig) {
			claim(t, r.c, r.task)
			st, _ := snapshotOf(r.c.Service.Store)
			item := st.Items[r.task.ID]
			execute2(t, r.c, item.Attempt, control.Request{Op: "ask", ID: item.ID, Body: "Which one?"})
		}},
		{"at a dialog", func(_ *testing.T, r *outageRig) {
			r.h.status.Windows[0].State, r.h.status.Windows[0].Dialog = session.StateWaiting, true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newOutageRig(t)
			tc.setup(t, r)
			r.after(t, OutageGap+time.Second)
			if len(r.wakes) != 0 {
				t.Errorf("woke %q, want nobody", r.wakes)
			}
		})
	}
}

func TestAnOutageDoesNotWakeABusySession(t *testing.T) {
	r := newOutageRig(t)
	r.h.status.Windows[0].State = session.StateWorking
	r.after(t, OutageGap+time.Second)
	if len(r.wakes) != 0 {
		t.Errorf("woke %q, want a working session left alone", r.wakes)
	}
}

// execute2 runs r as the session of attempt, as its engine commands do.
func execute2(t *testing.T, c *Coordinator, attempt string, r control.Request) {
	t.Helper()
	r.Key = control.Secret()
	if _, err := c.Service.Execute(control.Actor{Name: attempt, Role: "worker", Attempt: attempt}, r); err != nil {
		t.Fatal(r.Op, err)
	}
}

// slowHost is a host whose status read takes d on the test's clock, as a
// slow host call does inside a pass.
type slowHost struct {
	*host
	r *outageRig
	d time.Duration
}

func (s slowHost) Status() (hostops.Status, error) {
	s.r.now = s.r.now.Add(s.d)
	return s.host.Status()
}

func TestAPassThatRunsLongerThanTheGapIsNotAnOutage(t *testing.T) {
	r := newOutageRig(t)
	before := r.attempt(t)
	r.c.Client = func(hosts.Host) remote.Client { return slowHost{r.h, r, OutageGap + time.Minute} }
	r.after(t, time.Second)
	r.c.Client = func(hosts.Host) remote.Client { return r.h }
	r.after(t, time.Minute)
	if len(r.wakes) != 0 {
		t.Errorf("woke %q after a slow pass, want nobody: the server kept running", r.wakes)
	}
	if at := r.attempt(t); !at.IdleSince.Equal(before.IdleSince) {
		t.Errorf("idle start %v, want %v kept", at.IdleSince, before.IdleSince)
	}
}
