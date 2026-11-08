package ui

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestPanelKeyboardAndFocus runs the dashboard's real scripts against its own
// index.html in a small DOM shim (testdata/dom_shim.js) and checks the item
// panel's keyboard behaviour, focus handling and rendering of server data.
// The scenarios are in testdata/panel_test.js, one "ok"/"FAIL" line each.
func TestPanelKeyboardAndFocus(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "testdata/panel_test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("panel scenarios failed: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
