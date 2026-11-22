package setup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/remote"
)

const servicesTOML = "[host.box.services]\nmanager = \"systemd\"\nserver = \"engine-server.service\"\n"

// unitState is the server host's unit, linger and session tmux. The zero
// value is a unit that comes back and a tmux outside it.
type unitState struct {
	enabled      string // is-enabled's answer; "" is enabled
	restart      string // Restart's value; "" is on-failure
	linger       string // Linger's value; "" is yes
	tmuxUnit     bool   // the session tmux runs inside the unit
	tmuxErr      bool   // its cgroup cannot be read
	statusFails  bool   // engine host status answers an error
	statusGarble bool   // engine host status answers no JSON
	notFound     bool   // systemctl and loginctl know nothing of the unit
}

// answer is the host's reply to the unit step's reads.
func (u unitState) answer(cmd string, out, errOut io.Writer) (int, bool) {
	switch cmd {
	case "systemctl --user is-enabled engine-server.service":
		if u.notFound {
			fmt.Fprint(errOut, "Failed to get unit file state for engine-server.service: No such file or directory")
			return 1, true
		}
		got := u.enabled
		if got == "" {
			got = "enabled"
		}
		fmt.Fprintln(out, got)
		return map[bool]int{true: 0, false: 1}[got == "enabled"], true
	case "systemctl --user show -p Restart engine-server.service":
		if u.notFound {
			fmt.Fprint(errOut, "Failed to connect to bus")
			return 1, true
		}
		got := u.restart
		if got == "" {
			got = "on-failure"
		}
		fmt.Fprintf(out, "Restart=%s\n", got)
		return 0, true
	case `loginctl show-user "$(id -un)" -p Linger`:
		got := u.linger
		if got == "" {
			got = "yes"
		}
		fmt.Fprintf(out, "Linger=%s\n", got)
		return 0, true
	case "engine host status --json":
		return u.status(out), true
	}
	return 0, false
}

func (u unitState) status(out io.Writer) int {
	switch {
	case u.statusGarble:
		fmt.Fprint(out, "not json")
		return 1
	case u.statusFails:
		fmt.Fprint(out, `{"error":"tmux: no server running"}`)
		return 1
	case u.tmuxErr:
		fmt.Fprint(out, `{"tmux_unit_error":"read tmux server 9's cgroup: permission denied"}`)
	case u.tmuxUnit:
		fmt.Fprint(out, `{"tmux_unit":{"pid":9,"unit":"engine-server.service","reason":"tmux server 9 runs inside engine-server.service; restart it with tmux kill-server"}}`)
	default:
		fmt.Fprint(out, `{"slots":8}`)
	}
	return 0
}

// unitRig is a Mac that sets up box, which records the server's unit.
func unitRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.home = callerHomeWith(t, hostsTOML+servicesTOML)
	r.box = settledBox(r.home)
	return r
}

func TestAGoodUnitPasses(t *testing.T) {
	r := unitRig(t)
	if !mustRun(t, r, "box") {
		t.Fatalf("items left open:\n%s", r.out.String())
	}
	if want := "no suspend: ok (masked)\n5. server unit: ok (engine-server.service enabled, restarts, lingers; the session tmux is outside it)"; !strings.Contains(r.out.String(), want) {
		t.Errorf("want %q in:\n%s", want, r.out.String())
	}
	if len(r.box.mutations) != 0 {
		t.Errorf("the check changed the host: %q", r.box.mutations)
	}
	for _, c := range r.box.commands {
		if strings.Contains(c, "systemctl --user start") || strings.Contains(c, "systemctl --user stop") || strings.Contains(c, "systemctl --user restart") {
			t.Errorf("the check ran %q", c)
		}
	}
}

func TestABadUnitRefusesWithItsFix(t *testing.T) {
	cases := []struct {
		name    string
		bad     unitState
		problem string
		fix     string
	}{
		{"not enabled", unitState{enabled: "disabled"}, "engine-server.service is not enabled (disabled)", "ssh -t dev@box 'systemctl --user enable engine-server.service'"},
		{"unit unknown", unitState{notFound: true}, "is not enabled (Failed to get unit file state", "systemctl --user enable"},
		{"no restart", unitState{restart: "no"}, "has Restart=no; set Restart=on-failure under [Service]", "ssh -t dev@box 'systemctl --user edit engine-server.service'"},
		{"no linger", unitState{linger: "no"}, "linger is off, so the unit stops at logout", `loginctl enable-linger "$(id -un)"`},
		{"tmux inside the unit", unitState{tmuxUnit: true}, "the session tmux runs inside engine-server.service: tmux server 9", "engine setup --host box"},
		{"tmux cgroup unreadable", unitState{tmuxErr: true}, "cannot read the session tmux's cgroup: read tmux server 9's cgroup: permission denied", "engine setup --host box"},
		{"status fails", unitState{statusFails: true}, "cannot read the session tmux: tmux: no server running", "engine setup --host box"},
		{"status unreadable", unitState{statusGarble: true}, "cannot read the session tmux: engine host status:", "engine setup --host box"},
	}
	for _, c := range cases {
		r := unitRig(t)
		r.box.unit = c.bad
		if mustRun(t, r, "box") {
			t.Errorf("%s: closed", c.name)
		}
		out := r.out.String()
		for _, want := range []string{"5. server unit: OPEN", c.problem, c.fix, `operator-setup.md, "The server's unit"`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: want %q in:\n%s", c.name, want, out)
			}
		}
	}
}

func TestEveryBadStateIsNamedAndFixedTogether(t *testing.T) {
	r := unitRig(t)
	r.box.unit = unitState{enabled: "disabled", restart: "no", linger: "no", tmuxUnit: true}
	if mustRun(t, r, "box") {
		t.Fatal("closed")
	}
	out := r.out.String()
	for _, want := range []string{
		"is not enabled", "has Restart=no", "linger is off", "the session tmux runs inside",
		`'systemctl --user enable engine-server.service; systemctl --user edit engine-server.service; loginctl enable-linger "$(id -un)"'`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}

func TestAServerHostWithNoServicesGetsANote(t *testing.T) {
	r := newRig(t)
	r.home = callerHomeWith(t, hostsTOML)
	r.box = settledBox(r.home)
	r.writeServerConfig(t, "box")
	if !mustRun(t, r, "box") {
		t.Fatalf("a note refused:\n%s", r.out.String())
	}
	out := r.out.String()
	for _, want := range []string{"5. server unit: ok (note: the server runs here with no [host.box.services] table in hosts.toml, so it will not return after a reboot;", `operator-setup.md, "The server's unit"`} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	for _, c := range r.box.commands {
		if strings.Contains(c, "systemctl --user") || strings.Contains(c, "loginctl") {
			t.Errorf("a host with no unit ran %q", c)
		}
	}
}

func TestOnlyTheServersHostIsChecked(t *testing.T) {
	r := newRig(t)
	r.home = callerHomeWith(t, hostsTOML)
	r.box = settledBox(r.home)
	r.writeServerConfig(t, "mac")
	if !mustRun(t, r, "box") {
		t.Fatalf("items left open:\n%s", r.out.String())
	}
	if strings.Contains(r.out.String(), "server unit") {
		t.Errorf("a host that does not run the server was checked:\n%s", r.out.String())
	}
}

func TestAMacServerHostIsLeftAlone(t *testing.T) {
	r := newRig(t)
	r.home = callerHomeWith(t, hostsTOML)
	r.os = "darwin"
	r.local.rev = "2a08a0c"
	r.writeServerConfig(t, "mac")
	if !mustRun(t, r, "") {
		t.Fatalf("items left open:\n%s", r.out.String())
	}
	if !strings.Contains(r.out.String(), "4. server unit: ok (not Linux)") {
		t.Errorf("want the Mac passed as not Linux:\n%s", r.out.String())
	}
}

func TestAnUnreachableServerHostIsAnErrorNotAPass(t *testing.T) {
	r := unitRig(t)
	r.box.down = true
	var un *remote.UnreachableError
	if _, err := r.run("box"); !errors.As(err, &un) {
		t.Errorf("err %v, want an unreachable host", err)
	}
}

// writeServerConfig makes the caller's config.toml name host as the server's.
func (r *rig) writeServerConfig(t *testing.T, host string) {
	t.Helper()
	path := filepath.Join(r.home, ".config", "engine", "config.toml")
	if err := os.WriteFile(path, []byte("[server]\nhost = \""+host+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
