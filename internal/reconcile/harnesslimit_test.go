package reconcile

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// atLimit is v with devbox's claude at a usage limit that ended an attempt
// at t0 and resets at reset (zero unknown).
func atLimit(v View, reset time.Time) View {
	l := control.HarnessLimit{Host: "devbox", Harness: "claude", Ended: t0, Since: t0, Attempt: "a9", Task: "t9"}
	if !reset.IsZero() {
		l.ResetsAt = reset.Unix()
	}
	v.Fleet.Limits = []control.HarnessLimit{l}
	return v
}

// when is v at d after t0.
func when(v View, d time.Duration) View {
	v.Now = t0.Add(d)
	v.Fleet.Now = v.Now
	return v
}

func TestAKnownLimitHoldsDispatchUntilTheResetPlusTheMargin(t *testing.T) {
	v := atLimit(view(plan("planned"), task(1, "todo")), t0.Add(time.Hour))
	if got := shows(Decide(when(v, time.Hour+ResumeDelay-time.Second))); strings.Contains(got, "dispatch") {
		t.Errorf("dispatched before the reset plus %s:\n%s", ResumeDelay, got)
	}
	if got := shows(Decide(when(v, time.Hour+ResumeDelay))); got != "dispatch t1 devbox claude false" {
		t.Errorf("after the reset:\n%s", got)
	}
}

func TestPlacementSkipsALimitedHostForAnother(t *testing.T) {
	v := atLimit(view(plan("planned"), task(1, "todo")), t0.Add(time.Hour))
	v.Fleet.Hosts = append(v.Fleet.Hosts, HostLoad{Name: "mac", Harnesses: []string{"claude"}, FreeSlots: 1, FreeHeavySlots: 1})
	if got := shows(Decide(when(v, time.Minute))); got != "dispatch t1 mac claude false" {
		t.Errorf("got\n%s", got)
	}
	// With no other host, the task waits without telling anyone.
	v = atLimit(view(plan("planned"), task(1, "todo")), t0.Add(time.Hour))
	if got := Decide(when(v, time.Minute)); len(got) != 0 {
		t.Errorf("got\n%s", shows(got))
	}
}

func TestAResetNoLaterThanTheLimitIsUnknown(t *testing.T) {
	v := atLimit(view(plan("planned"), task(1, "todo")), t0.Add(-time.Hour))
	if got := Decide(when(v, time.Minute)); len(got) != 0 {
		t.Errorf("a stale reset opened the host:\n%s", shows(got))
	}
	if got := shows(Decide(when(v, ProbeEvery))); got != "dispatch t1 devbox claude false" {
		t.Errorf("a stale reset is probed as unknown:\n%s", got)
	}
}

func TestAnUnknownLimitIsProbedEvery30MinutesOneTaskAtATime(t *testing.T) {
	v := atLimit(view(plan("planned"), task(1, "todo"), task(2, "todo")), time.Time{})
	if got := Decide(when(v, ProbeEvery-time.Second)); len(got) != 0 {
		t.Errorf("probed too soon:\n%s", shows(got))
	}
	if got := shows(Decide(when(v, ProbeEvery))); got != "dispatch t1 devbox claude false" {
		t.Errorf("one probe:\n%s", got)
	}
	// While a probe runs, nothing else starts there.
	v.Fleet.Limits[0].Probe, v.Fleet.Limits[0].Live = t0.Add(ProbeEvery), true
	if got := Decide(when(v, 2*ProbeEvery)); len(got) != 0 {
		t.Errorf("a second task during a probe:\n%s", shows(got))
	}
	// Once that probe ended without a claim, the next one waits its turn.
	v.Fleet.Limits[0].Live = false
	if got := Decide(when(v, 2*ProbeEvery-time.Second)); len(got) != 0 {
		t.Errorf("probed again too soon:\n%s", shows(got))
	}
	if got := shows(Decide(when(v, 2*ProbeEvery))); got != "dispatch t1 devbox claude false" {
		t.Errorf("the next probe:\n%s", got)
	}
}

func TestAProbeHittingTheLimitIsReleasedAtOnce(t *testing.T) {
	s := working()
	s.Limited, s.Spawned = true, t0.Add(time.Minute)
	probe := with(task(1, "running"), s)
	probe.Current.Harness = "claude"
	v := when(atLimit(view(plan("planned"), probe), time.Time{}), 2*time.Minute)
	acts := Decide(v)
	if len(acts) != 1 || acts[0].Kind != Release || !acts[0].Stopped || acts[0].Attempt != "" || acts[0].Key != "" {
		t.Fatalf("got %+v", acts)
	}
	// A session that claimed its task waits in the session as before.
	claimed := probe
	claimed.Claimed = true
	if got := shows(Decide(when(atLimit(view(plan("planned"), claimed), time.Time{}), 2*time.Minute))); strings.Contains(got, "release") {
		t.Errorf("a claimed session was released:\n%s", got)
	}
	// So does one that started before the limit.
	before := probe
	before.Current.Started = t0.Add(-time.Minute)
	if got := shows(Decide(when(atLimit(view(plan("planned"), before), time.Time{}), 2*time.Minute))); strings.Contains(got, "release") {
		t.Errorf("a session older than the limit was released:\n%s", got)
	}
}

func TestADeathAtALimitEndsTheAttemptWithItsReset(t *testing.T) {
	s := Session{Spawned: t0.Add(-time.Hour), DiedAtLimit: true, ResetsAt: 7000}
	v := when(view(plan("planned"), with(task(1, "running"), s)), time.Minute)
	acts := Decide(v)
	if len(acts) == 0 || acts[0].Kind != EndAttempt || !acts[0].AtLimit || acts[0].ResetsAt != 7000 || acts[0].Probe ||
		!strings.Contains(acts[0].Text, "died at a usage limit") {
		t.Fatalf("got %+v", acts)
	}
	// A probe's death at the limit is marked, so it tells no one.
	probe := with(task(1, "running"), s)
	probe.Current.Harness, probe.Current.Started = "claude", t0.Add(time.Minute)
	acts = Decide(when(atLimit(view(plan("planned"), probe), time.Time{}), 2*time.Minute))
	if len(acts) == 0 || !acts[0].AtLimit || !acts[0].Probe {
		t.Fatalf("got %+v", acts)
	}
}

func TestOneNoteAfter12HoursOfFailedProbes(t *testing.T) {
	v := atLimit(view(plan("planned"), task(9, "todo")), time.Time{})
	v.Fleet.Limits[0].Probes, v.Fleet.Limits[0].Probe, v.Fleet.Limits[0].Live = 23, t0.Add(ProbeNote), true
	if got := shows(Decide(when(v, ProbeNote-time.Second))); strings.Contains(got, "limit-probes") {
		t.Errorf("noted too soon:\n%s", got)
	}
	acts := Decide(when(v, ProbeNote))
	if len(acts) != 1 || acts[0].Key != "limit-probes/devbox/claude/"+strconv.FormatInt(t0.Unix(), 10) || acts[0].Item != "" || !strings.Contains(acts[0].Text, "every probe since has hit it;") {
		t.Fatalf("got %+v", acts)
	}
	// Another failed probe sends the same note, so its key dedupes it.
	v.Fleet.Limits[0].Probes++
	if again := Decide(when(v, ProbeNote+time.Hour)); len(again) != 1 || again[0].Key != acts[0].Key || again[0].Text != acts[0].Text {
		t.Errorf("the note changed with a probe: %+v", again)
	}
	// A known reset, or no probe yet: no note.
	for _, edit := range []func(*View){
		func(v *View) { v.Fleet.Limits[0].ResetsAt = t0.Add(48 * time.Hour).Unix() },
		func(v *View) { v.Fleet.Limits[0].Probes = 0 },
	} {
		w := atLimit(view(plan("planned"), task(9, "todo")), time.Time{})
		w.Fleet.Limits[0].Probes, w.Fleet.Limits[0].Live = 23, true
		edit(&w)
		if got := shows(Decide(when(w, ProbeNote))); strings.Contains(got, "limit-probes") {
			t.Errorf("noted:\n%s", got)
		}
	}
}

func TestALiveSessionAtAnUnknownLimitIsToldToContinueEvery30Minutes(t *testing.T) {
	for _, tc := range []struct {
		name            string
		since, resumed  time.Duration // before t0; zero is never
		limited, resume bool
	}{
		{name: "limited for less than 30 minutes", since: ProbeEvery - time.Second, limited: true},
		{name: "limited for 30 minutes", since: ProbeEvery, limited: true, resume: true},
		{name: "told to continue 10 minutes ago", since: 2 * ProbeEvery, resumed: 10 * time.Minute, limited: true},
		{name: "told to continue 30 minutes ago", since: 2 * ProbeEvery, resumed: ProbeEvery, limited: true, resume: true},
		{name: "waiting, not limited", since: 2 * ProbeEvery},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := working()
			if tc.limited {
				s.Limited = true
				s.LimitedSince = t0.Add(-tc.since)
			} else {
				s.Waiting, s.WaitingSince, s.IdleSince = true, t0.Add(-time.Minute), t0.Add(-time.Minute)
			}
			if tc.resumed != 0 {
				s.ResumeAt = t0.Add(-tc.resumed)
			}
			got := shows(Decide(view(plan("planned"), with(task(1, "running"), s))))
			if want := "resume t1 a1"; (got == want) != tc.resume {
				t.Errorf("got %q, want a resume: %v", got, tc.resume)
			}
		})
	}
}
