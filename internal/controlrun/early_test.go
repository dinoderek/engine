package controlrun

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

// taskNotices are the task's notices to the operator, oldest first.
func taskNotices(c *Coordinator, task control.Item) []string {
	st, _ := c.Service.Store.Snapshot()
	var out []string
	for _, u := range st.Updates {
		if u.Item == task.ID && u.Recipient == "operator" {
			out = append(out, u.Body)
		}
	}
	return out
}

// dieEarly kills the task's running window, whose run shows tail, and runs a
// pass that ends the attempt.
func dieEarly(t *testing.T, c *Coordinator, h *host, task control.Item, tail string) control.Attempt {
	t.Helper()
	st, _ := c.Service.Store.Snapshot()
	at := st.Attempts[st.Items[task.ID].Attempt]
	h.status.Windows[len(h.status.Windows)-1].Alive = false
	for i, r := range h.status.Runs {
		if r.Record.RunID == at.RunID {
			h.status.Runs[i].PaneTail = tail
		}
	}
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	return st.Attempts[at.ID]
}

func TestASessionThatDiesBeforeClaimingIsRetriedOnceThenFails(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	first := dieEarly(t, c, h, task, "tmux: server exited unexpectedly")
	st, _ := c.Service.Store.Snapshot()
	if first.Reason != control.EndEarlyDeath || st.Items[task.ID].State != "todo" {
		t.Fatalf("the first early death: %+v, task %s", first, st.Items[task.ID].State)
	}
	if notices := taskNotices(c, task); len(notices) != 1 || !strings.Contains(notices[0], "dispatched again once") || !strings.Contains(notices[0], "tmux: server exited unexpectedly") {
		t.Fatalf("the first early death's notice: %q", notices)
	}
	pass(t, c)
	pass(t, c)
	st, _ = c.Service.Store.Snapshot()
	second := st.Attempts[st.Items[task.ID].Attempt]
	if second.ID == first.ID || second.State != "running" || st.Items[task.ID].State != "running" {
		t.Fatalf("the task was not dispatched again: %+v %s", second, st.Items[task.ID].State)
	}
	ended := dieEarly(t, c, h, task, "claude: command not found")
	st, _ = c.Service.Store.Snapshot()
	if ended.Reason != control.EndFailed || !ended.Early || st.Items[task.ID].State != "failed" {
		t.Fatalf("the second early death: %+v, task %s", ended, st.Items[task.ID].State)
	}
	notices := taskNotices(c, task)
	last := notices[len(notices)-1]
	for _, want := range []string{"second time", "Pane log tail of " + first.ID, "tmux: server exited unexpectedly", "Pane log tail of " + second.ID, "claude: command not found"} {
		if !strings.Contains(last, want) {
			t.Errorf("the failing notice lacks %q: %s", want, last)
		}
	}
}

func TestAnEarlyDeathWithoutAPaneLogSaysSo(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	dieEarly(t, c, h, task, "")
	if notices := taskNotices(c, task); len(notices) != 1 || !strings.Contains(notices[0], "the host showed no pane log") {
		t.Fatalf("notice: %q", notices)
	}
}

func TestASessionThatClaimedItsTaskResumesAfterItsDeathsUntilTheCap(t *testing.T) {
	c, h, _, _, task := setup(t)
	h.status.FreeSlots = control.DeathCap // the fake host takes a slot per launch and never gives it back
	var deaths []control.Attempt
	for i := range control.DeathCap {
		pass(t, c)
		pass(t, c)
		claim(t, c, task)
		deaths = append(deaths, dieEarly(t, c, h, task, fmt.Sprintf("crash %d", i+1)))
	}
	st, _ := c.Service.Store.Snapshot()
	for i, at := range deaths[:control.DeathCap-1] {
		if at.Reason != control.EndVanished || !at.Crashed || i > 0 && at.ID == deaths[i-1].ID {
			t.Fatalf("claimed death %d did not resume: %+v", i+1, at)
		}
		if i > 0 && !h.launches[at.RunID].Spawn.Resume {
			t.Errorf("claimed death %d's attempt was not a resume", i+1)
		}
	}
	last := deaths[control.DeathCap-1]
	if last.Reason != control.EndFailed || st.Items[task.ID].State != "failed" {
		t.Fatalf("the cap's death: %+v, task %s", last, st.Items[task.ID].State)
	}
	notices := taskNotices(c, task)
	if !strings.Contains(notices[0], "dispatched again as a resume") {
		t.Errorf("the first claimed death's notice: %s", notices[0])
	}
	failing := notices[len(notices)-1]
	wants := []string{fmt.Sprintf("%d such deaths in a row; the task failed", control.DeathCap),
		"Pane log tail of " + deaths[1].ID + ":\ncrash 2", "Pane log tail of " + last.ID + ":\ncrash 3"}
	for _, want := range wants {
		if !strings.Contains(failing, want) {
			t.Errorf("the failing notice lacks %q: %s", want, failing)
		}
	}
	if strings.Contains(failing, "crash 1") {
		t.Errorf("the failing notice carries more than the last two tails: %s", failing)
	}
}
