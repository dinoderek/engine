package controlrun

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

// killWindow marks task's window dead on h, its run showing tail.
func killWindow(t *testing.T, c *Coordinator, h *host, task control.Item, tail string) control.Attempt {
	t.Helper()
	st, _ := c.Service.Store.Snapshot()
	at := st.Attempts[st.Items[task.ID].Attempt]
	for i, w := range h.status.Windows {
		if w.Name == fmt.Sprintf("demo-%d", control.Number(task.ID)) {
			h.status.Windows[i].Alive = false
		}
	}
	for i, r := range h.status.Runs {
		if r.Record.RunID == at.RunID {
			h.status.Runs[i].PaneTail = tail
		}
	}
	return at
}

func TestADeathDuringAnUpgradeResumesUncountedWithABugNotice(t *testing.T) {
	c, h, claimed, unclaimed := twoRunning(t)
	execute(t, c.Service, control.Request{Op: "upgrade", State: control.UpgradeStart})
	at := killWindow(t, c, h, claimed, "panic: the plugin moved")
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	ended := st.Attempts[at.ID]
	if ended.Reason != control.EndVanished || !ended.Upgraded || ended.Crashed || st.Items[claimed.ID].State != "running" {
		t.Fatalf("the death during the upgrade: %+v, task %s", ended, st.Items[claimed.ID].State)
	}
	got := taskNotices(c, claimed)
	if len(got) != 1 || !strings.Contains(got[0], "engine bug") || !strings.Contains(got[0], "panic: the plugin moved") || !strings.Contains(got[0], "counts toward no limit") {
		t.Fatalf("the death's notices: %q", got)
	}

	// After the end marker, a death follows the normal rule: an unclaimed
	// one is a first early death, with no bug notice.
	execute(t, c.Service, control.Request{Op: "upgrade", State: control.UpgradeEnd})
	later := c.Service.Now().Add(time.Minute)
	c.Service.Now = func() time.Time { return later }
	pass(t, c)
	other := killWindow(t, c, h, unclaimed, "exit 1")
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	if e := st.Attempts[other.ID]; e.Reason != control.EndEarlyDeath || !e.Early || e.Upgraded {
		t.Fatalf("the death after the upgrade: %+v", e)
	}
	got = taskNotices(c, unclaimed)
	if len(got) != 1 || strings.Contains(got[0], "engine bug") || !strings.Contains(got[0], "dispatched again once") {
		t.Fatalf("the later death's notices: %q", got)
	}
}

// A host-wide loss during an upgrade sends the host's one notice, and each
// attempt it ended its own engine-bug notice; both resume uncounted.
func TestAHostWideLossDuringAnUpgradeAlsoReportsEachDeathAsABug(t *testing.T) {
	c, h, claimed, unclaimed := twoRunning(t)
	execute(t, c.Service, control.Request{Op: "upgrade", State: control.UpgradeStart})
	before, _ := c.Service.Store.Snapshot()
	h.status.Windows, h.status.TmuxStart = nil, 2000
	pass(t, c)
	st, _ := c.Service.Store.Snapshot()
	if got := hostNotices(c); len(got) != 1 || got[0] != "devbox's tmux restarted; 2 tasks will resume" {
		t.Fatalf("host notices: %q", got)
	}
	for _, task := range []control.Item{claimed, unclaimed} {
		at := st.Attempts[before.Items[task.ID].Attempt]
		if !at.HostLost || !at.Upgraded || at.Crashed || at.Early {
			t.Errorf("%s ended %+v; want lost with its host, during the upgrade, uncounted", task.ID, at)
		}
		if got := taskNotices(c, task); len(got) != 1 || !strings.Contains(got[0], "engine bug") {
			t.Errorf("%s's notices: %q", task.ID, got)
		}
	}
}
