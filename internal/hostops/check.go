package hostops

import (
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
	// Denied are the permission requests the hook denied and the auto-mode
	// refusals the session met, one line each.
	Denied []string
	// hooked is true for a harness whose hooks report its state, so the
	// check must see the file.
	hooked bool
	// Reviewed are the permission requests the hook left to the harness's
	// own review, which decided them without a prompt.
	Reviewed []string
	// Signalled is true when the session's hooks wrote its state file. Only
	// a harness whose state comes from hooks needs it.
	Signalled bool
	// State is the state the file last recorded, when it is there.
	State string
	// Joined is true when the session joined the host's remote-control
	// daemon, as a spawn would; Note says why it did not, or why the apps
	// may not see a joined session.
	Joined bool
	Note   string
	// GH is what `gh auth status` said in a window of the session engine.
	GH GHAuth
}

// OK reports whether an unattended session would run here without a prompt.
func (r CheckResult) OK() bool {
	return r.Closed && r.Finished && len(r.Denied) == 0 && (r.Signalled || !r.hooked) && r.GH.OK
}

// Check launches a throwaway hands-off session of the harness (the default
// when empty) in a scratch folder, with this host's launch line at the light tier,
// and has it do what an executor does that has needed a prompt before: read
// the skill, read a file it wrote in /tmp, run gh and engine, and close its
// own window. It reports every permission request, a stall, and a step left
// undone. First, without the harness, it runs `gh auth status` in a window of
// the same tmux session, so a gh that cannot read its token there fails the
// check. Each run gets its own run directory, which it leaves in place.
func Check(d Deps, harnessName string, out io.Writer) (CheckResult, error) {
	h, err := adapterFor(d, harnessName)
	if err != nil {
		return CheckResult{}, err
	}
	l := h.Session(CheckName)
	cfg, host, bin, err := checkHost(d, h.Name())
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
	line, err := cfg.LaunchLine(host, hosts.Launch{Harness: h.Name(), Tier: hosts.Light, Name: CheckName, Clone: work, Home: d.Home})
	if err != nil {
		return CheckResult{}, err
	}
	r := CheckResult{RunDir: runDir, hooked: h.HookedState()}
	if r.GH, err = probeGH(d, runDir, work); err != nil {
		return CheckResult{}, err
	}
	if d.JoinRemote {
		r.Joined, r.Note = l.Join(bin, line)
	}
	if err := l.Trust(work, work, r.Joined); err != nil {
		return CheckResult{}, err
	}
	defer func() {
		if err := l.Release(work); err != nil {
			fmt.Fprintf(out, "could not remove the check's trust: %v\n", err)
		}
	}()
	// A joined session's hooks find their run by its folder, in run.json.
	if r.Joined {
		rec := session.Record{Schema: session.RecordSchema, RunID: runID, Host: host.Name, Harness: h.Name(), Tier: string(hosts.Light),
			Mode: HandsOff, Name: CheckName, Worktree: work, PaneLog: filepath.Join(runDir, session.PaneLog), Spawned: d.Now().UTC()}
		if err := session.WriteRecord(runDir, rec); err != nil {
			return CheckResult{}, err
		}
	}
	// A marker left by anyone else must not read as this session's.
	marker := filepath.Join(checkTmp, "engine-"+runID)
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return CheckResult{}, err
	}
	defer func() { _ = os.Remove(marker) }()
	run := launch{line: line, dir: work, runDir: runDir, mode: HandsOff, kickoff: h.CheckKickoff(marker), joined: r.Joined}
	w := session.Window{Name: CheckName, Dir: work, Command: launchCommand(h.Name(), l, run), Log: filepath.Join(runDir, session.PaneLog)}
	opened, err := d.Tmux.Open(w)
	if err != nil {
		return CheckResult{}, fmt.Errorf("open the tmux window: %w", err)
	}
	fmt.Fprintf(out, "checking in %s:%s (run directory %s); this takes about a minute\n", session.Name, opened.ID, runDir)
	r.Closed, err = waitClosed(d, opened.ID)
	if !r.Closed {
		r.Screen, _ = d.Tmux.Capture(opened.ID, stallLines)
		if kerr := d.Tmux.Kill(opened.ID); kerr != nil {
			fmt.Fprintf(out, "could not close %s:%s: %v\n", session.Name, opened.ID, kerr)
		}
	}
	if err != nil {
		return r, err
	}
	data, _ := os.ReadFile(marker)
	r.Finished = strings.Contains(string(data), "done")
	if st, ok := session.ReadHarnessState(runDir); ok {
		r.Signalled, r.State = true, st.State
	}
	if r.Denied, r.Reviewed, err = readPermissions(filepath.Join(runDir, session.PermissionLog)); err != nil {
		return r, err
	}
	return r, nil
}

// checkHost runs the refusals that apply without a task: the host config, the harness and its binary, and a free window name. It
// returns the binary's path.
func checkHost(d Deps, harness string) (*hosts.Config, hosts.Host, string, error) {
	cfg, host, err := openHost(d.Home)
	if err != nil {
		return nil, hosts.Host{}, "", err
	}
	bin, err := checkHarness(host, harness, d.Home)
	if err != nil {
		return nil, hosts.Host{}, "", err
	}
	for _, name := range []string{CheckName, GHProbeName} {
		ok, err := d.Tmux.HasWindow(name)
		if err != nil {
			return nil, hosts.Host{}, "", refuse("cannot list the tmux windows: %v", err)
		}
		if ok {
			return nil, hosts.Host{}, "", refuse("the tmux session %s already has a window %s: a check is running, or close it", session.Name, name)
		}
	}
	return cfg, host, bin, nil
}

// waitClosed polls until the window with id is gone, or CheckTimeout. A
// window whose command has exited, as when the harness cannot start, ends the
// wait at once, as not closed.
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
		w, ok := find(windows, id)
		if !ok {
			return true, nil
		}
		if w.Dead {
			return false, nil
		}
	}
	return false, nil
}

func find(windows []session.WindowInfo, id string) (session.WindowInfo, bool) {
	for _, w := range windows {
		if w.ID == id {
			return w, true
		}
	}
	return session.WindowInfo{}, false
}

// RunCheck runs the check of the harness, prints its report to out, and
// reports whether an unattended session here would run without a prompt.
func RunCheck(d Deps, harness string, out io.Writer) (bool, error) {
	r, err := Check(d, harness, out)
	if err != nil {
		return false, err
	}
	ReportCheck(out, r)
	return r.OK(), nil
}

// ReportCheck prints the check's findings.
func ReportCheck(out io.Writer, r CheckResult) {
	mark := map[bool]string{true: "ok  ", false: "FAIL"}
	reportGH(out, mark, r.GH)
	if r.Joined {
		fmt.Fprintln(out, "ok   the session joined remote control")
	}
	if r.Note != "" {
		fmt.Fprintf(out, "note %s\n", r.Note)
	}
	fmt.Fprintf(out, "%s the session closed its own window\n", mark[r.Closed])
	if r.Screen != "" {
		fmt.Fprintf(out, "     it stalled; its last screen:\n%s\n", indent(r.Screen))
	}
	fmt.Fprintf(out, "%s the session did every step\n", mark[r.Finished])
	if r.hooked {
		fmt.Fprintf(out, "%s the session's hooks wrote its state%s\n", mark[r.Signalled], lastState(r.State))
	}
	fmt.Fprintf(out, "%s no permission request was denied\n", mark[len(r.Denied) == 0])
	for _, l := range r.Denied {
		fmt.Fprintf(out, "     %s\n", l)
	}
	if len(r.Reviewed) > 0 {
		fmt.Fprintln(out, "     auto-review decided, without a prompt:")
		for _, l := range r.Reviewed {
			fmt.Fprintf(out, "     %s\n", l)
		}
	}
	if !r.OK() {
		fmt.Fprintf(out, "an unattended session here would stop; see %s\n", r.RunDir)
	}
}

// lastState is the state the hooks last recorded, as a report suffix.
func lastState(state string) string {
	if state == "" {
		return " (no state file in the run directory)"
	}
	return " (last: " + state + ")"
}

func indent(s string) string {
	return "     " + strings.ReplaceAll(s, "\n", "\n     ")
}
