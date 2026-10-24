package start

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// CheckName is the tmux window of the preflight check.
const CheckName = "engine-check"

// CheckTimeout is how long the check waits for its session to finish.
const CheckTimeout = 4 * time.Minute

// checkPoll is how often the check looks for its window.
const checkPoll = 5 * time.Second

// checkTmp is where the session writes and reads its marker: /tmp itself,
// not os.TempDir, because the launch line allows reads in /tmp only. Tests
// replace it.
var checkTmp = "/tmp"

// stallLines is how much of a stalled screen the check prints.
const stallLines = 20

// CheckResult is what the preflight check saw.
type CheckResult struct {
	RunDir string
	// Closed is true when the session closed its own window in time.
	Closed bool
	// Screen is the stalled window's last lines, when it did not close.
	Screen string
	// Finished is true when the session did every step.
	Finished bool
	// Denied are the permission requests and auto-mode refusals the
	// session met, one line each.
	Denied []string
}

// OK reports whether an unattended session would run here without a prompt.
func (r CheckResult) OK() bool { return r.Closed && r.Finished && len(r.Denied) == 0 }

// Check launches a throwaway hands-off session in a scratch folder, with this
// host's launch line at the light tier, and has it do what an executor does
// that has needed a prompt before: read the skill, read a file it wrote in
// /tmp, run gh and engine, and close its own window. It reports every
// permission request, a stall, and a step left undone. Each run gets its own
// run directory, which it leaves in place.
func Check(d Deps, out io.Writer) (CheckResult, error) {
	p, err := checkPlan(d)
	if err != nil {
		return CheckResult{}, err
	}
	runID := "check-" + d.Now().UTC().Format("20060102T150405Z")
	runDir, err := session.RunDir(d.Home, runID)
	if err != nil {
		return CheckResult{}, err
	}
	work := filepath.Join(runDir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return CheckResult{}, err
	}
	if err := session.TrustFolder(session.ClaudeConfig(d.Getenv, d.Home), work); err != nil {
		return CheckResult{}, fmt.Errorf("trust %s for Claude Code: %w", work, err)
	}
	marker := filepath.Join(checkTmp, "engine-"+runID)
	line, err := p.cfg.LaunchLine(p.host, hosts.Launch{Harness: Harness, Tier: hosts.Light, Name: CheckName, Clone: work, Home: d.Home})
	if err != nil {
		return CheckResult{}, err
	}
	kickoff := CheckKickoff(filepath.Join(session.PluginDir(d.Getenv, d.Home), "skills", "engine", "SKILL.md"), marker)
	w := session.Window{Name: CheckName, Dir: work, Command: launchCommand(d, line, runDir, HandsOff, kickoff), Log: filepath.Join(runDir, session.PaneLog)}
	opened, err := d.Tmux.Open(w)
	if err != nil {
		return CheckResult{}, fmt.Errorf("open the tmux window: %w", err)
	}
	fmt.Fprintf(out, "checking in %s:%s (run directory %s); this takes a few minutes\n", session.Name, opened.ID, runDir)
	r := CheckResult{RunDir: runDir}
	if r.Closed, err = waitClosed(d, opened.ID); err != nil {
		return r, err
	}
	if !r.Closed {
		r.Screen, _ = d.Tmux.Capture(opened.ID, stallLines)
		if err := d.Tmux.Kill(opened.ID); err != nil {
			fmt.Fprintf(out, "could not close %s:%s: %v\n", session.Name, opened.ID, err)
		}
	}
	data, _ := os.ReadFile(marker)
	r.Finished = strings.Contains(string(data), "done")
	_ = os.Remove(marker)
	if r.Denied, err = readDenied(filepath.Join(runDir, session.PermissionLog)); err != nil {
		return r, err
	}
	return r, nil
}

// checkPlan runs the refusals that apply without a task: the token, the
// host config, the harness binary and a free window name.
func checkPlan(d Deps) (plan, error) {
	p := plan{opts: Options{Mode: HandsOff}}
	if err := p.loadHost(d); err != nil {
		return plan{}, err
	}
	if !p.host.Runs(Harness) {
		return plan{}, refuse("host %s does not run %s", p.host.Name, Harness)
	}
	if err := p.checkBin(d); err != nil {
		return plan{}, err
	}
	ok, err := d.Tmux.HasWindow(CheckName)
	if err != nil {
		return plan{}, refuse("cannot list the tmux windows: %v", err)
	}
	if ok {
		return plan{}, refuse("the tmux session %s already has a window %s: a check is running, or close it", session.Name, CheckName)
	}
	return p, nil
}

// CheckKickoff is the check session's first message.
func CheckKickoff(skill, marker string) string {
	return "This is engine's preflight check, not a task: do exactly these steps, in order, and nothing else. " +
		"1. Read " + skill + " with the Read tool. " +
		"2. Run `echo ok > " + marker + "` with Bash, then read " + marker + " with the Read tool. " +
		"3. Run `gh api user --jq .login` with Bash. " +
		"4. Run `engine version` with Bash. " +
		"5. Run `echo done >> " + marker + "` with Bash. " +
		"6. Run `cd \"$HOME\" && tmux kill-window -t \"$TMUX_PANE\"` with Bash. " +
		"If a step is denied, skip it and go on."
}

// waitClosed polls until the window with id is gone, or CheckTimeout.
func waitClosed(d Deps, id string) (bool, error) {
	sleep := d.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	for waited := time.Duration(0); waited < CheckTimeout; waited += checkPoll {
		sleep(checkPoll)
		windows, err := d.Tmux.Windows()
		if err != nil {
			return false, err
		}
		if !hasID(windows, id) {
			return true, nil
		}
	}
	return false, nil
}

func hasID(windows []session.WindowInfo, id string) bool {
	for _, w := range windows {
		if w.ID == id {
			return true
		}
	}
	return false
}

// readDenied reads permissions.jsonl, if any, as one line per entry.
func readDenied(path string) ([]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var lines []string
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var e struct {
			Event, Tool, Decision, Reason string
			Input                         json.RawMessage
		}
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		line := fmt.Sprintf("%s %s: %s", e.Tool, e.Input, e.Decision)
		if e.Reason != "" {
			line += " (" + e.Reason + ")"
		}
		lines = append(lines, line)
	}
	return lines, scan.Err()
}

// ReportCheck prints the check's findings.
func ReportCheck(out io.Writer, r CheckResult) {
	mark := map[bool]string{true: "ok  ", false: "FAIL"}
	fmt.Fprintf(out, "%s the session closed its own window\n", mark[r.Closed])
	if r.Screen != "" {
		fmt.Fprintf(out, "     it stalled; its last screen:\n%s\n", indent(r.Screen))
	}
	fmt.Fprintf(out, "%s the session did every step\n", mark[r.Finished])
	fmt.Fprintf(out, "%s no permission request was denied\n", mark[len(r.Denied) == 0])
	for _, l := range r.Denied {
		fmt.Fprintf(out, "     %s\n", l)
	}
	if !r.OK() {
		fmt.Fprintf(out, "an unattended session here would stop; see %s\n", r.RunDir)
	}
}

func indent(s string) string {
	return "     " + strings.ReplaceAll(s, "\n", "\n     ")
}
