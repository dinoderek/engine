package start

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// checkTmux plays tmux for the check: the session engine exists, new-window
// opens @7, and the session behind it runs act when its command starts. The
// window stays listed for openPolls polls.
type checkTmux struct {
	openPolls int
	screen    string
	act       func()
	polls     int
	started   bool
	killed    bool
	command   string
}

func (f *checkTmux) run(_ string, args ...string) (string, error) {
	switch args[0] {
	case "new-window":
		return "@7 %7\n", nil
	case "respawn-pane":
		f.started, f.command = true, args[len(args)-1]
		if f.act != nil {
			f.act()
		}
	case "list-windows":
		if !f.started {
			return "@1 %1 0 other\n", nil
		}
		f.polls++
		if f.polls <= f.openPolls {
			return "@1 %1 0 other\n@7 %7 0 " + CheckName + "\n", nil
		}
		return "@1 %1 0 other\n", nil
	case "capture-pane":
		return f.screen, nil
	case "kill-window":
		f.killed = true
	}
	return "", nil
}

func checkDeps(t *testing.T, f *checkTmux) (Deps, home, *int) {
	h := newHome(t, "mac")
	tmp := t.TempDir()
	old := checkTmp
	checkTmp = tmp
	t.Cleanup(func() { checkTmp = old })
	sleeps := 0
	d := Deps{Tmux: &session.Tmux{Run: f.run, Bin: "tmux"}, Home: h.dir, Getenv: func(string) string { return "" },
		Now: fixedNow, Sleep: func(time.Duration) { sleeps++ }}
	return d, h, &sleeps
}

const checkRun = "check-20260102T030405Z"

func TestCheckPasses(t *testing.T) {
	f := &checkTmux{openPolls: 2}
	d, h, sleeps := checkDeps(t, f)
	f.act = func() { write(t, filepath.Join(checkTmp, "engine-"+checkRun), "ok\ndone\n", 0o600) }
	var out bytes.Buffer
	r, err := Check(d, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() || r.RunDir != filepath.Join(h.dir, ".local/share/engine/runs", checkRun) || *sleeps != 3 || f.killed {
		t.Errorf("result %+v, sleeps %d, killed %v", r, *sleeps, f.killed)
	}
	for _, want := range []string{"export ENGINE_UNATTENDED=1", "--model haiku-x", "preflight check", filepath.Join(h.dir, ".claude/plugins/cache/engine/skills/engine/SKILL.md")} {
		if !strings.Contains(f.command, want) {
			t.Errorf("command lacks %q:\n%s", want, f.command)
		}
	}
	if _, err := os.Stat(filepath.Join(checkTmp, "engine-"+checkRun)); !os.IsNotExist(err) {
		t.Errorf("marker left behind: %v", err)
	}
	var report bytes.Buffer
	ReportCheck(&report, r)
	if strings.Contains(report.String(), "FAIL") {
		t.Errorf("report:\n%s", report.String())
	}
}

func TestCheckReportsAStallAndDenials(t *testing.T) {
	f := &checkTmux{openPolls: 1 << 30, screen: "Read outside the working directories\n Allow this read?\n\n"}
	d, _, sleeps := checkDeps(t, f)
	f.act = func() {
		runDir := filepath.Join(d.Home, ".local/share/engine/runs", checkRun)
		write(t, filepath.Join(runDir, session.PermissionLog),
			`{"event":"PermissionRequest","tool":"Read","input":{"file_path":"/opt/x"},"decision":"deny"}`+"\n"+
				`{"event":"PermissionDenied","tool":"Bash","input":{"command":"rm"},"decision":"denied by auto mode","reason":"[Destruction]"}`+"\n", 0o600)
	}
	r, err := Check(d, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if r.OK() || r.Closed || r.Finished || !f.killed || !strings.Contains(r.Screen, "Allow this read?") || len(r.Denied) != 2 ||
		*sleeps != int(CheckTimeout/checkPoll) {
		t.Fatalf("result %+v, killed %v, sleeps %d", r, f.killed, *sleeps)
	}
	var report bytes.Buffer
	ReportCheck(&report, r)
	for _, want := range []string{"FAIL the session closed", "Allow this read?", `Read {"file_path":"/opt/x"}: deny`, "(" + "[Destruction])", "would stop"} {
		if !strings.Contains(report.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, report.String())
		}
	}
}

func TestCheckRefusals(t *testing.T) {
	t.Run("a check is running", func(t *testing.T) {
		f := &checkTmux{}
		d, _, _ := checkDeps(t, f)
		d.Tmux.Run = func(_ string, args ...string) (string, error) {
			if args[0] == "list-windows" {
				return "@7 %7 0 " + CheckName + "\n", nil
			}
			return "", nil
		}
		if _, err := Check(d, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "a check is running") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("no token", func(t *testing.T) {
		d, h, _ := checkDeps(t, &checkTmux{})
		if err := os.Remove(filepath.Join(h.dir, ".config/engine/github-token")); err != nil {
			t.Fatal(err)
		}
		if _, err := Check(d, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "token") {
			t.Errorf("got %v", err)
		}
	})
	t.Run("a bad permission log", func(t *testing.T) {
		f := &checkTmux{}
		d, _, _ := checkDeps(t, f)
		f.act = func() {
			write(t, filepath.Join(d.Home, ".local/share/engine/runs", checkRun, session.PermissionLog), "{", 0o600)
		}
		if _, err := Check(d, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "permissions.jsonl") {
			t.Errorf("got %v", err)
		}
	})
}
