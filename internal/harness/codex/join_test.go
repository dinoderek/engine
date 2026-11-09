package codex

import (
	"errors"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
)

// Only the daemon's own program counts as running with remote control on,
// not a shell or awk that names the path.
func TestRemoteControlOn(t *testing.T) {
	home := "/h/.local/share/engine/codex"
	cases := map[string]bool{
		home + "/packages/app-server-daemon/current/bin/codex app-server --remote-control --listen unix://": true,
		home + "/packages/app-server-daemon/current/bin/codex app-server --listen unix://":                  false,
		"awk -v p=" + home + "/packages/ / --remote-control/":                                               false,
		"/bin/sh -c " + home + "/packages/x --remote-control":                                               false,
		"/h/.codex/packages/app-server-daemon/current/bin/codex app-server --remote-control":                false,
		"": false,
	}
	for ps, want := range cases {
		if got := remoteControlOn("/bin/zsh\n"+ps+"\n", home); got != want {
			t.Errorf("remoteControlOn(%q) = %v, want %v", ps, got, want)
		}
	}
}

func TestDaemonEnvironmentRejectsUnreadableOrAmbiguousProcess(t *testing.T) {
	home := t.TempDir()
	c := New(harness.Env{Home: home}, Options{})
	proc := "123 Mon Nov 2 12:00:00 2026 " + EngineHome(home) + "/packages/app-server-daemon/releases/0.164.0/bin/codex app-server --remote-control --listen unix:// --managed-daemon"
	var instance string
	var instanceErr error
	run := func(string, ...string) (string, error) { return instance, instanceErr }
	instanceErr = errors.New("ps failed")
	if _, got := c.daemonEnvironment(run); !strings.Contains(got, "cannot identify") {
		t.Fatal(got)
	}
	instance, instanceErr = proc+"\n"+proc, nil
	if _, got := c.daemonEnvironment(run); !strings.Contains(got, "ambiguous") {
		t.Fatal(got)
	}
}
