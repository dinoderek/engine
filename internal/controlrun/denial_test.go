package controlrun

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// Server mode reads a waiting session's denial from its host's status, and
// posts its stall as the Permission stop.
func TestAStallAfterADenialIsAPermissionStop(t *testing.T) {
	c, h, _, _, task := setup(t)
	pass(t, c)
	pass(t, c)
	now := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	c.Service.Now = func() time.Time { return now }
	h.status.Windows[0].State = session.StateWaiting
	h.status.Windows[0].Denial = &hostops.Denial{At: now.Add(-time.Minute), Tool: "Bash",
		Input: json.RawMessage(`{"command":"gh pr merge 9 -R owner/demo --squash --match-head-commit abc"}`), Decision: "denied by auto mode", Reason: "[Merge Without Review]"}
	pass(t, c)
	// Passes follow each other within OutageGap, or the wait would restart.
	for range 3 {
		now = now.Add(4 * time.Minute)
		pass(t, c)
	}
	st, _ := snapshotOf(c.Service.Store)
	var got []string
	for _, u := range st.Updates {
		if u.Item == task.ID && strings.Contains(u.Body, "waited for input") {
			got = append(got, u.Body)
		}
	}
	if len(got) != 1 || !strings.Contains(got[0], "Permission hard stop") ||
		!strings.Contains(got[0], "Bash `gh pr merge 9 -R owner/demo --squash --match-head-commit abc` was denied by auto mode: [Merge Without Review]") {
		t.Errorf("notices %q", got)
	}
}
