package reconcile

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// quietWorking is a session working in a turn whose last activity was d
// before t0.
func quietWorking(d time.Duration) Session {
	s := working()
	s.Working, s.LastActivity, s.Screen = true, t0.Add(-d), "line one\nline two\n"
	return s
}

// A working session quiet for QuietWait is told once, keyed by attempt and
// last activity; idle, limited, dialog-held, background-only and
// unknown-activity sessions never are.
func TestQuietSessions(t *testing.T) {
	limited := quietWorking(2 * QuietWait)
	limited.Limited = true
	dialog := quietWorking(2 * QuietWait)
	dialog.Working, dialog.Dialog = false, true
	idle := quietWorking(2 * QuietWait)
	idle.Working, idle.Waiting = false, true
	bg := quietWorking(2 * QuietWait)
	bg.Background = true
	unknownActivity := quietWorking(0)
	unknownActivity.LastActivity = time.Time{}
	unknownState := quietWorking(2 * QuietWait)
	unknownState.Working = false
	flagged := quietWorking(2 * QuietWait)
	flagged.StallNoted = flagged.LastActivity
	unknownHost := quietWorking(2 * QuietWait)
	unknownHost.Unknown = true
	for _, tc := range []struct {
		name string
		t    Item
		want string
	}{
		{"quiet for QuietWait", with(task(2, "running"), quietWorking(QuietWait)), "notice t2 quiet/a2/" + itoa(t0.Add(-QuietWait).Unix())},
		{"quiet for longer", with(task(2, "in-review"), quietWorking(3*QuietWait)), "notice t2 quiet/a2/" + itoa(t0.Add(-3*QuietWait).Unix())},
		{"active a second ago", with(task(2, "running"), quietWorking(QuietWait-time.Second)), ""},
		{"limited", with(task(2, "running"), limited), ""},
		{"held at a dialog", with(task(2, "running"), dialog), ""},
		{"idle", with(task(2, "running"), idle), ""},
		{"only a background job", with(task(2, "running"), bg), ""},
		{"unknown activity", with(task(2, "running"), unknownActivity), ""},
		{"unknown state", with(task(2, "running"), unknownState), ""},
		{"already told for this last activity", with(task(2, "running"), flagged), ""},
		{"unknown host", with(task(2, "running"), unknownHost), ""},
		{"a done task", with(task(2, "done"), quietWorking(QuietWait)), ""},
		{"a stopped task", func() Item { x := with(task(2, "running"), quietWorking(QuietWait)); x.Stopped = true; return x }(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, a := range quiets(view(plan("planned"), tc.t)) {
				got = append(got, show(a))
			}
			if strings.Join(got, "\n") != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// The notice names the task, host, how long it has been quiet, its last
// screen lines and the ways out, and says nothing was stopped.
func TestQuietNoticeText(t *testing.T) {
	acts := quiets(view(plan("planned"), with(task(2, "running"), quietWorking(90*time.Minute))))
	if len(acts) != 1 {
		t.Fatalf("actions %v", acts)
	}
	a := acts[0]
	if a.Kind != Notice || a.Attempt != "a2" || a.Item != "t2" || !a.Stalled.Equal(t0.Add(-90*time.Minute)) {
		t.Errorf("action %+v", a)
	}
	for _, want := range []string{"demo-2", "t2", "devbox", "1h30m0s", "line one\nline two", "engine attach t2", "engine tell t2", "engine task stop t2", "engine task move t2", "Nothing was stopped"} {
		if !strings.Contains(a.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, a.Text)
		}
	}
}

// Quiet is told again for a later stretch: new activity changes the key and
// the mark, so a session flagged for the old activity is flagged for the new.
func TestQuietKeyFollowsTheLastActivity(t *testing.T) {
	key := func(last time.Time, now time.Time) string {
		s := quietWorking(0)
		s.LastActivity = last
		v := view(plan("planned"), with(task(2, "running"), s))
		v.Now = now
		acts := quiets(v)
		if len(acts) == 0 {
			return ""
		}
		return acts[0].Key
	}
	first := t0.Add(-QuietWait)
	if a, b := key(first, t0), key(first, t0.Add(10*time.Minute)); a == "" || a != b {
		t.Errorf("one stretch's keys %q and %q should be one", a, b)
	}
	later := t0.Add(5 * time.Minute)
	if a, b := key(first, t0), key(later, later.Add(QuietWait)); b == "" || a == b {
		t.Errorf("a new stretch's key %q repeats the first %q", b, a)
	}
}

// A planner that is quiet is told with the plan's ways out.
func TestAQuietPlannerIsTold(t *testing.T) {
	p := with(plan("approved"), quietWorking(QuietWait))
	acts := Decide(view(p))
	if len(acts) != 1 || acts[0].Kind != Notice || !strings.HasPrefix(acts[0].Key, "quiet/a1/") || !strings.Contains(acts[0].Text, "engine plan stop p1") || strings.Contains(acts[0].Text, "task move") {
		t.Errorf("actions %v", acts)
	}
}
