package controlrun

import (
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

// at sets the coordinator's and the server's clock to d after the setup's
// time.
func at(c *Coordinator, d time.Duration) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC).Add(d)
	c.Service.Now = func() time.Time { return now }
}

// dieAtLimit kills the task's newest window with its run's limit marker set,
// resetting at reset (zero unknown), and runs a pass that ends its attempt.
func dieAtLimit(t *testing.T, c *Coordinator, h *host, task control.Item, reset int64) control.Attempt {
	t.Helper()
	st, _ := c.Service.Store.Snapshot()
	at := st.Attempts[st.Items[task.ID].Attempt]
	for i, w := range h.status.Windows {
		if w.Name == "demo-"+task.ID[1:] {
			h.status.Windows[i].Alive = false
		}
	}
	for i, r := range h.status.Runs {
		if r.Record.RunID == at.RunID {
			h.status.Runs[i].Limit = &hostops.RunLimit{ResetsAt: reset}
		}
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	return st.Attempts[at.ID]
}

func TestASessionDeadAtAKnownLimitIsDispatchedAgainAfterTheReset(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	reset := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	ended := dieAtLimit(t, c, h, task, reset.Unix())
	st, _ := c.Service.Store.Snapshot()
	if ended.Reason != control.EndVanished || !ended.Limited || ended.ResetsAt != reset.Unix() || st.Items[task.ID].State == "failed" {
		t.Fatalf("a death at a limit: %+v, task %s", ended, st.Items[task.ID].State)
	}
	notices := taskNotices(c, task)
	if len(notices) != 1 || !strings.Contains(notices[0], "died at a usage limit") || !strings.Contains(notices[0], "2026-11-07T10:02:00Z") {
		t.Fatalf("notices %q", notices)
	}
	// No dispatch on that host × harness until the reset plus the margin.
	at(c, time.Hour+time.Minute)
	pass(t, c)
	if len(h.launches) != 1 {
		t.Fatalf("dispatched before the reset: %d launches", len(h.launches))
	}
	at(c, time.Hour+2*time.Minute)
	pass(t, c)
	if len(h.launches) != 2 {
		t.Fatalf("not dispatched after the reset: %d launches", len(h.launches))
	}
	for _, r := range h.launches {
		if r.Attempt != ended.ID && !r.Spawn.Resume {
			t.Errorf("the dispatch after the reset is no resume: %+v", r.Spawn)
		}
	}
}

func TestAProbeThatHitsTheLimitAgainEndsAtOnceAndQuietly(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	first := dieAtLimit(t, c, h, task, 0)
	if !first.Limited || first.Reason != control.EndVanished {
		t.Fatalf("the first death: %+v", first)
	}
	if notices := taskNotices(c, task); len(notices) != 1 || !strings.Contains(notices[0], "by a probe every 30 minutes") {
		t.Fatalf("notices %q", notices)
	}
	at(c, 29*time.Minute)
	pass(t, c)
	if len(h.launches) != 1 {
		t.Fatalf("probed too soon: %d launches", len(h.launches))
	}
	at(c, 30*time.Minute)
	pass(t, c)
	if len(h.launches) != 2 {
		t.Fatalf("no probe after 30 minutes: %d launches", len(h.launches))
	}
	// The probe's kickoff turn hits the limit: it is released at once,
	// keeping its worktree and branch.
	h.status.Windows[len(h.status.Windows)-1].State = session.StateLimited
	at(c, 31*time.Minute)
	pass(t, c)
	if n := len(h.outcomes); n == 0 || h.outcomes[n-1] != hostops.RunStopped {
		t.Fatalf("the limited probe was not released keeping its worktree: %v", h.outcomes)
	}
	probe := dieAtLimit(t, c, h, task, 0)
	if !probe.Limited || probe.Reason != control.EndVanished || probe.ID == first.ID {
		t.Fatalf("the probe's end: %+v", probe)
	}
	if notices := taskNotices(c, task); len(notices) != 1 {
		t.Errorf("a probe's end told the operator: %q", notices)
	}
	// The wait restarts from the probe.
	h.status.FreeSlots = 2 // the fake host frees no slot on a release
	at(c, 60*time.Minute)
	pass(t, c)
	if len(h.launches) != 2 {
		t.Errorf("probed again too soon: %d launches", len(h.launches))
	}
}

func TestResumedOncePerKnownResetAndEvery30MinutesOtherwise(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		at    control.Attempt
		reset int64
		want  bool
	}{
		{"a known reset not yet resumed", control.Attempt{ResumedFor: 4000, ResumeAt: now.Add(-time.Hour)}, 5000, false},
		{"a known reset resumed", control.Attempt{ResumedFor: 5000, ResumeAt: now.Add(-time.Hour)}, 5000, true},
		{"an unknown reset never resumed", control.Attempt{}, 0, false},
		{"an unknown reset resumed lately", control.Attempt{ResumeAt: now.Add(-10 * time.Minute)}, 0, true},
		{"an unknown reset resumed 30 minutes ago", control.Attempt{ResumeAt: now.Add(-30 * time.Minute)}, 0, false},
		{"an unknown reset after a known one", control.Attempt{ResumedFor: 5000, ResumeAt: now.Add(-time.Minute)}, 0, false},
	} {
		if got := resumed(tc.at, tc.reset, now); got != tc.want {
			t.Errorf("%s: resumed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAProbeThatDiesWithAKnownResetWaitsForIt(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	dieAtLimit(t, c, h, task, 0)
	at(c, 30*time.Minute)
	pass(t, c)
	h.status.Windows[len(h.status.Windows)-1].State = session.StateLimited
	at(c, 31*time.Minute)
	pass(t, c)
	reset := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	probe := dieAtLimit(t, c, h, task, reset.Unix())
	if probe.ResetsAt != reset.Unix() || !probe.Limited {
		t.Fatalf("the probe's end does not record its reset: %+v", probe)
	}
	// The known reset, not the probe cadence, now decides.
	h.status.FreeSlots = 2 // the fake host frees no slot on a release
	at(c, 3*time.Hour+time.Minute)
	pass(t, c)
	if len(h.launches) != 2 {
		t.Fatalf("dispatched before the new reset: %d launches", len(h.launches))
	}
	at(c, 3*time.Hour+2*time.Minute)
	pass(t, c)
	if len(h.launches) != 3 {
		t.Errorf("not dispatched after the new reset: %d launches", len(h.launches))
	}
}

func TestKeptMarksWhenASessionWasFirstSeenLimited(t *testing.T) {
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	at := kept(control.Attempt{}, reconcile.Session{Live: true, Limited: true}, now, true)
	if !at.LimitedSince.Equal(now) {
		t.Fatalf("limited since %v", at.LimitedSince)
	}
	if again := kept(at, reconcile.Session{Live: true, Limited: true}, now.Add(time.Hour), true); !again.LimitedSince.Equal(now) {
		t.Errorf("a later pass moved it: %v", again.LimitedSince)
	}
	if working := kept(at, reconcile.Session{Live: true}, now.Add(time.Hour), true); !working.LimitedSince.IsZero() {
		t.Errorf("a working session keeps it: %v", working.LimitedSince)
	}
}
