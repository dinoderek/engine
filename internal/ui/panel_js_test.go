package ui

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestDashboardScripts runs the dashboard's real scripts against its own
// index.html in a small DOM shim (testdata/dom_shim.js) and checks keyboard
// behaviour, focus handling and rendering of server data. Each scenario file
// prints one "ok"/"FAIL" line per scenario: panel_test.js covers the item
// panel, sessions_test.js the Sessions tab and the feedback links.
func TestDashboardScripts(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	for _, file := range []string{"panel_test.js", "sessions_test.js"} {
		t.Run(file, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			out, err := exec.CommandContext(ctx, node, "testdata/"+file).CombinedOutput()
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", file, err, out)
			}
			t.Logf("%s", out)
		})
	}
}
