package setup

import (
	"encoding/json"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
)

// unitDoc is where the operator's guide describes the server's unit and the
// fix for each fault the unit step names.
const unitDoc = `operator-setup.md, "The server's unit"`

// runsServer reports whether the target is the host that runs the control
// server: one that records the services table, or that the caller's
// config.toml names.
func (r *runner) runsServer() bool {
	if r.host.Services.Recorded() {
		return true
	}
	s, err := hosts.ReadServer(hosts.ConfigDir(r.d.Home))
	return err == nil && s.Host == r.host.Name
}

// unitFault is one way the server's unit would fail to bring the server back,
// or would kill sessions when it stops. fix is the command that corrects it,
// empty when no single command does.
type unitFault struct{ problem, fix string }

// serverUnit refuses, on a Linux server host, a unit that would not bring the
// server back after a reboot or a crash (not enabled, no restart, no linger)
// and a session tmux that stopping the unit would kill. It only reads: it
// never starts, stops or restarts the unit. A server host that records no
// unit gets a note.
func (r *runner) serverUnit() (harness.Outcome, error) {
	res, err := r.t.Sh("uname -s", "")
	if err != nil {
		return harness.Outcome{}, err
	}
	if res.FirstLine() != "Linux" {
		return harness.Passed("not Linux"), nil
	}
	if !r.host.Services.Recorded() {
		return harness.Passed("note: the server runs here with no [host." + r.host.Name + ".services] table in hosts.toml, " +
			"so it will not return after a reboot; record its unit (" + unitDoc + ")"), nil
	}
	unit := r.host.Services.Server
	var faults []unitFault
	for _, check := range []func(string) (*unitFault, error){r.unitEnabled, r.unitRestarts, r.unitLingers, r.unitTmux} {
		f, err := check(unit)
		if err != nil {
			return harness.Outcome{}, err
		}
		if f != nil {
			faults = append(faults, *f)
		}
	}
	if len(faults) == 0 {
		return harness.Passed(unit + " enabled, restarts, lingers; the session tmux is outside it"), nil
	}
	return r.unitOpen(faults), nil
}

// unitOpen is the open item for faults: every problem, then the commands that
// fix them, or the rerun when none does.
func (r *runner) unitOpen(faults []unitFault) harness.Outcome {
	var problems, fixes []string
	for _, f := range faults {
		problems = append(problems, f.problem)
		if f.fix != "" {
			fixes = append(fixes, f.fix)
		}
	}
	command := r.rerun()
	if len(fixes) > 0 {
		command = r.t.onHost(strings.Join(fixes, "; "), true)
	}
	return harness.Open("the server would not come back or would lose its sessions: "+strings.Join(problems, "; ")+" ("+unitDoc+")", command)
}

// unitEnabled faults a unit that systemd does not start at login.
func (r *runner) unitEnabled(unit string) (*unitFault, error) {
	res, err := r.t.Sh("systemctl --user is-enabled "+hosts.ShellWord(unit), "")
	if err != nil {
		return nil, err
	}
	if got := strings.TrimSpace(res.Out); got != "enabled" {
		if got == "" {
			got = res.Why()
		}
		return &unitFault{unit + " is not enabled (" + got + ")", "systemctl --user enable " + hosts.ShellWord(unit)}, nil
	}
	return nil, nil
}

// unitRestarts faults a unit that systemd does not restart after it exits.
func (r *runner) unitRestarts(unit string) (*unitFault, error) {
	res, err := r.t.Sh("systemctl --user show -p Restart "+hosts.ShellWord(unit), "")
	if err != nil {
		return nil, err
	}
	got, found := strings.CutPrefix(res.FirstLine(), "Restart=")
	switch {
	case !found:
		return &unitFault{"cannot read " + unit + "'s Restart: " + res.Why(), ""}, nil
	case got == "no":
		return &unitFault{unit + " has Restart=no; set Restart=on-failure under [Service]", "systemctl --user edit " + hosts.ShellWord(unit)}, nil
	}
	return nil, nil
}

// unitLingers faults a user whose services stop at logout.
func (r *runner) unitLingers(string) (*unitFault, error) {
	res, err := r.t.Sh(`loginctl show-user "$(id -un)" -p Linger`, "")
	if err != nil {
		return nil, err
	}
	line := res.FirstLine()
	if strings.Contains(res.Why(), "not logged in or lingering") {
		line = "Linger=no" // loginctl knows no user that neither lingers nor has a session
	}
	switch got, found := strings.CutPrefix(line, "Linger="); {
	case !found:
		return &unitFault{"cannot read the user's linger: " + res.Why(), ""}, nil
	case got != "yes":
		return &unitFault{"linger is off, so the unit stops at logout", `loginctl enable-linger "$(id -un)"`}, nil
	}
	return nil, nil
}

// hostStatus is the part of `engine host status --json` the unit step reads.
type hostStatus struct {
	Error    string `json:"error"`
	TmuxUnit *struct {
		Unit   string `json:"unit"`
		Reason string `json:"reason"`
	} `json:"tmux_unit"`
	TmuxUnitError string `json:"tmux_unit_error"`
}

// unitTmux faults a session tmux server that stopping the unit would kill,
// whether tmux started inside the unit or its scope was refused. A cgroup
// that cannot be read is unknown, not passed.
func (r *runner) unitTmux(string) (*unitFault, error) {
	res, err := r.t.Sh("engine host status --json", "")
	if err != nil {
		return nil, err
	}
	var st hostStatus
	switch {
	case json.Unmarshal([]byte(res.Out), &st) != nil:
		return &unitFault{"cannot read the session tmux: engine host status: " + res.Why(), ""}, nil
	case st.Error != "":
		return &unitFault{"cannot read the session tmux: " + st.Error, ""}, nil
	case st.TmuxUnitError != "":
		return &unitFault{"cannot read the session tmux's cgroup: " + st.TmuxUnitError, ""}, nil
	case st.TmuxUnit != nil:
		return &unitFault{"the session tmux runs inside " + st.TmuxUnit.Unit + ": " + st.TmuxUnit.Reason, ""}, nil
	}
	return nil, nil
}
