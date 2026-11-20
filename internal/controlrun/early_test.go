package controlrun

import (
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

func TestASessionThatClaimedItsTaskFailsOnItsFirstDeath(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	claim(t, c, task)
	ended := dieEarly(t, c, h, task, "ignored")
	st, _ := c.Service.Store.Snapshot()
	if ended.Reason != control.EndFailed || ended.Early || ended.Tail != "" || st.Items[task.ID].State != "failed" {
		t.Fatalf("a claimed session's death: %+v, task %s", ended, st.Items[task.ID].State)
	}
}
