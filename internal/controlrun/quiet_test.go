package controlrun

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/reconcile"
	"github.com/dinoderek/engine/internal/session"
)

// quietNotices are the operator notices of item id about a quiet session.
func quietNotices(t *testing.T, c *Coordinator, id string) []string {
	t.Helper()
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, u := range st.Updates {
		if u.Item == id && u.Recipient == "operator" && strings.Contains(u.Body, "shown no activity") {
			out = append(out, u.Body)
		}
	}
	return out
}

// A working session quiet for an hour is told once; later passes add no
// second notice, and new activity followed by another hour of quiet adds
// one more. The clock is fake, and passes follow each other within OutageGap.
func TestAQuietWorkingSessionIsToldOncePerQuietStretch(t *testing.T) {
	c, h, _, _, task := setup(t)
	var logged []string
	c.Logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	for range 3 {
		pass(t, c)
	}
	claim(t, c, task)
	pass(t, c)
	now := c.Service.Now()
	c.Service.Now = func() time.Time { return now }
	advance := func(d time.Duration) {
		for end := now.Add(d); now.Before(end); {
			now = now.Add(min(4*time.Minute, end.Sub(now)))
			pass(t, c)
		}
	}
	active := func(at time.Time) {
		h.status.Windows[0].State, h.status.Windows[0].LastActivity, h.status.Windows[0].Screen = session.StateWorking, at.Unix(), "last screen"
	}
	active(now)
	advance(reconcile.QuietWait - time.Minute)
	if n := quietNotices(t, c, task.ID); len(n) != 0 {
		t.Fatalf("notices before an hour: %q", n)
	}
	advance(2 * time.Minute)
	n := quietNotices(t, c, task.ID)
	if len(n) != 1 || !strings.Contains(n[0], "engine attach "+task.ID) || !strings.Contains(n[0], "last screen") || !strings.Contains(n[0], "Nothing was stopped") {
		t.Fatalf("notices after an hour: %q", n)
	}
	advance(30 * time.Minute)
	if n := quietNotices(t, c, task.ID); len(n) != 1 {
		t.Fatalf("a later pass sent another: %q", n)
	}
	active(now)
	advance(reconcile.QuietWait + time.Minute)
	if n := quietNotices(t, c, task.ID); len(n) != 2 {
		t.Fatalf("notices after new activity and another hour: %q", n)
	}
	// The later passes decide to send nothing; none sends a notice that the
	// server refuses.
	for _, l := range logged {
		if strings.Contains(l, "notice") || strings.Contains(l, "request key") {
			t.Errorf("a pass logged %q", l)
		}
	}
	st, _ := c.Service.Store.Snapshot()
	if st.Items[task.ID].State != "running" || st.Attempts[st.Items[task.ID].Attempt].State != "running" {
		t.Errorf("a notice stopped something: task %s", st.Items[task.ID].State)
	}
}

// A session that is not working in a turn, or whose activity the host does
// not know, is never told, however long it is quiet.
func TestQuietIsNeverToldForOtherSessions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		window func(*hostops.Window)
	}{
		{"waiting", func(w *hostops.Window) { w.State = session.StateWaiting }},
		{"limited", func(w *hostops.Window) { w.State = session.StateLimited }},
		{"at a dialog", func(w *hostops.Window) { w.State, w.Dialog = session.StateWaiting, true }},
		{"only a background job", func(w *hostops.Window) { w.Background = true }},
		{"unknown activity", func(w *hostops.Window) { w.LastActivity = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, h, _, _, task := setup(t)
			for range 3 {
				pass(t, c)
			}
			claim(t, c, task)
			pass(t, c)
			now := c.Service.Now()
			c.Service.Now = func() time.Time { return now }
			h.status.Windows[0].State, h.status.Windows[0].LastActivity = session.StateWorking, now.Unix()
			tc.window(&h.status.Windows[0])
			for range 20 {
				now = now.Add(4 * time.Minute)
				pass(t, c)
			}
			if n := quietNotices(t, c, task.ID); len(n) != 0 {
				t.Errorf("notices: %q", n)
			}
		})
	}
}

// A window's activity reaches the session only while the window is alive.
func TestAWindowsActivityIsTheSessions(t *testing.T) {
	at := time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)
	w := fakeWindow(session.StateWorking, false)
	w.LastActivity = at.Unix()
	var s reconcile.Session
	withWindow(&s, w)
	if !s.Working || !s.LastActivity.Equal(at) {
		t.Errorf("a live working window: %+v", s)
	}
	w.Alive = false
	var dead reconcile.Session
	withWindow(&dead, w)
	if dead.Working || !dead.LastActivity.IsZero() {
		t.Errorf("a dead window: %+v", dead)
	}
}

// A running task whose session has not written to the server is held, so
// the claim stall never applies to it; its quiet session is told once, and no
// pass sends a notice the server refuses.
func TestARunningUnclaimedQuietSessionIsToldOnce(t *testing.T) {
	c, h, _, _, task := setup(t)
	var logged []string
	c.Logf = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	for range 3 {
		pass(t, c)
	}
	now := c.Service.Now()
	c.Service.Now = func() time.Time { return now }
	h.status.Windows[0].State, h.status.Windows[0].LastActivity = session.StateWorking, now.Unix()
	for range 30 {
		now = now.Add(4 * time.Minute)
		pass(t, c)
	}
	if n := quietNotices(t, c, task.ID); len(n) != 1 {
		t.Errorf("quiet notices: %q, want one", n)
	}
	for _, l := range logged {
		if strings.Contains(l, "notice") || strings.Contains(l, "request key") {
			t.Errorf("a pass logged %q", l)
		}
	}
}
