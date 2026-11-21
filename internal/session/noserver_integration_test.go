package session

import "testing"

// A host whose tmux server stopped reads as a host with no windows, not as
// one that cannot be read, so the coordinator sees every window gone.
func TestAStoppedTmuxServerShowsNoWindows(t *testing.T) {
	tm := privateTmux(t)
	if _, err := tm.tmux("new-session", "-d", "-s", "engine"); err != nil {
		t.Fatal(err)
	}
	if got, err := tm.Windows(); len(got) != 1 || err != nil {
		t.Fatalf("with a server: %v, %v", got, err)
	}
	if _, err := tm.tmux("kill-server"); err != nil {
		t.Fatal(err)
	}
	if got, err := tm.Windows(); got != nil || err != nil {
		t.Errorf("windows with no server: %v, %v", got, err)
	}
	if got, err := tm.ExitCodes(); got != nil || err != nil {
		t.Errorf("exit codes with no server: %v, %v", got, err)
	}
}
