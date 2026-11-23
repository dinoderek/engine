package codex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
)

// An update in engine's Codex home keeps the version it replaced while a
// window that started before the update lives, and a later setup removes it
// once none does. The operator's home is left to Codex's own commands.
func TestAnUpdateKeepsThePreviousVersionUntilOlderWindowsAreGone(t *testing.T) {
	codex := settledCodex(boxHome)
	codex.plugin = "0.7.0"
	codex.disk.now, codex.disk.windows = 200, []int64{100}
	r := newCodexRig(t, codex)
	if !mustRun(t, r) || !strings.Contains(r.out.String(), "10. codex plugin: changed (0.7.1, was 0.7.0)") {
		t.Fatalf("the update:\n%s", r.out.String())
	}
	if got := codex.versions(boxEngineH); !slices.Equal(got, []string{callerPlugin, "0.7.0"}) {
		t.Fatalf("engine's home holds %q after the update; want the new version and the one it replaced", got)
	}
	if len(codex.disk.kept) != 0 {
		t.Errorf("the copy set aside was left: %q", codex.disk.kept)
	}

	// A window from before the update still lives, beside one started
	// after it: a rerun keeps the old version.
	codex.disk.now, codex.disk.windows = 300, []int64{100, 250}
	mustRun(t, r)
	if got := codex.versions(boxEngineH); len(got) != 2 {
		t.Fatalf("a rerun with an older window live left %q", got)
	}

	// Only windows started after the update are left: the next setup removes
	// the old version, and the record of the update with it.
	codex.disk.windows = []int64{250}
	before := len(r.box.mutations)
	mustRun(t, r)
	if got := codex.versions(boxEngineH); !slices.Equal(got, []string{callerPlugin}) {
		t.Fatalf("engine's home holds %q once no older window lives; want only %s", got, callerPlugin)
	}
	if _, marked := codex.disk.marked[boxEngineH]; marked || len(r.box.mutations) != before+1 {
		t.Errorf("the prune ran %q, marked %v", r.box.mutations[before:], codex.disk.marked)
	}
	mustRun(t, r)
	if len(r.box.mutations) != before+1 {
		t.Errorf("a settled rerun changed %q", r.box.mutations[before+1:])
	}
}

// With no window live, or no tmux server, an update removes the old
// version in the same setup; with tmux missing, nothing can be told, and it
// is kept.
func TestAnUpdateKeepsTheOldVersionOnlyWhileAWindowMayNeedIt(t *testing.T) {
	for _, c := range []struct {
		name   string
		noTmux bool
		want   []string
	}{
		{"no window", false, []string{callerPlugin}},
		{"tmux missing", true, []string{callerPlugin, "0.7.0"}},
	} {
		codex := settledCodex(boxHome)
		codex.plugin, codex.disk.now, codex.disk.noTmux = "0.7.0", 200, c.noTmux
		r := newCodexRig(t, codex)
		if !mustRun(t, r) {
			t.Fatalf("%s:\n%s", c.name, r.out.String())
		}
		if got := codex.versions(boxEngineH); !slices.Equal(got, c.want) {
			t.Errorf("%s: engine's home holds %q; want %q", c.name, got, c.want)
		}
	}
}

// A copy that cannot be set aside stops the add, open; one that cannot be
// put back after an add is open too, and a failed add is reported before it.
func TestAKeepOrRestoreThatFailsIsOpen(t *testing.T) {
	for _, c := range []struct {
		name                            string
		keepFail, restoreFail, addFails bool
		want                            string
	}{
		{"the keep", true, false, false, "could not keep the plugin's versions in engine's Codex home: cp: No space left on device"},
		{"the restore", false, true, false, "could not put back the plugin's earlier versions in engine's Codex home: cp: No space left on device"},
		{"the restore after a failed add", false, true, true, "codex plugin marketplace add dinoderek/engine failed in engine's Codex home: marketplace unreachable"},
	} {
		codex := settledCodex(boxHome)
		codex.plugin, codex.marketFail = "0.7.0", c.addFails
		codex.disk.keepFail, codex.disk.restoreFail = c.keepFail, c.restoreFail
		r := newCodexRig(t, codex)
		if mustRun(t, r) || !strings.Contains(r.out.String(), "10. codex plugin: OPEN ("+c.want+")") {
			t.Errorf("%s: want %q open:\n%s", c.name, c.want, r.out.String())
		}
		if c.keepFail && codex.plugin != "0.7.0" {
			t.Errorf("%s: the add ran after a failed keep", c.name)
		}
	}
}

func TestKeptSince(t *testing.T) {
	for _, c := range []struct {
		name string
		res  harness.Result
		kept bool
	}{
		{"no tmux server", harness.Result{Out: "1000\n"}, false},
		{"only panes started after", harness.Result{Out: "1000\n13:19\n05:00\n"}, false},
		{"a pane started that second", harness.Result{Out: "1000\n13:20\n"}, true},
		{"a pane started days before", harness.Result{Out: "1000\n2-01:00:00\n"}, true},
		{"a pane whose process ps cannot read", harness.Result{Out: "1000\nunknown\n"}, true},
		{"an unreadable age", harness.Result{Out: "1000\n??\n"}, true},
		{"no clock", harness.Result{Out: "\n"}, true},
		{"tmux missing", harness.Result{Code: 3}, true},
	} {
		if got := keptSince(c.res, 200); got != c.kept {
			t.Errorf("%s: kept %v, want %v", c.name, got, c.kept)
		}
	}
}

// The window ages, read from a real tmux on a private server: a pane
// started before the install keeps the versions, and none started after
// it does.
func TestWindowAgesOnARealTmux(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is absent")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is absent")
	}
	sock := fmt.Sprintf("engine-retain-%d", os.Getpid())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })
	if out, err := exec.Command("tmux", "-L", sock, "new-session", "-d", "-s", "engine", "sleep 60").CombinedOutput(); err != nil {
		t.Fatalf("tmux: %v\n%s", err, out)
	}
	cmd := strings.ReplaceAll(windowAgesCommand, "tmux list-panes", "tmux -L "+sock+" list-panes")
	out, err := exec.Command(sh, "-c", cmd).Output()
	if err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	res := harness.Result{Out: string(out)}
	if lines := res.Lines(); len(lines) != 2 {
		t.Fatalf("the reply %q has no age for the one pane", out)
	}
	now, _ := strconv.ParseInt(res.Lines()[0], 10, 64)
	if !keptSince(res, now+1) {
		t.Errorf("a pane started before an install a second from now did not keep: %q", out)
	}
	if keptSince(res, now-3600) {
		t.Errorf("a pane started after an install an hour ago kept: %q", out)
	}
}

// The keep, restore and prune scripts, run by a real sh on a Codex home in
// a temporary directory, around a stand-in for Codex's add, which leaves
// only the version it adds.
func TestTheRetentionScriptsOnARealShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is absent")
	}
	home := filepath.Join(t.TempDir(), "codex home")
	word := hosts.ShellWord(home)
	cache := filepath.Join(home, "plugins", "cache", "engine", "engine")
	run := func(script string) {
		t.Helper()
		if out, err := exec.Command(sh, "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
	}
	versions := func() []string {
		entries, _ := os.ReadDir(cache)
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	// A fresh home has nothing to keep.
	run(keepCommand(word))
	run(restoreCommand(word))
	if err := os.MkdirAll(filepath.Join(cache, "0.7.0", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "0.7.0", "skills", "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(keepCommand(word))
	run(`rm -rf ` + pluginCache(word) + `/* && mkdir -p ` + pluginCache(word) + `/0.7.1`)
	run(restoreCommand(word))
	if got := versions(); !slices.Equal(got, []string{"0.7.0", "0.7.1"}) {
		t.Fatalf("after the restore the cache holds %q", got)
	}
	if b, err := os.ReadFile(filepath.Join(cache, "0.7.0", "skills", "SKILL.md")); err != nil || string(b) != "old" {
		t.Errorf("the kept version's skill reads %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, "plugins", ".engine-kept")); !os.IsNotExist(err) {
		t.Errorf("the copy set aside is left: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(home, "plugins", ".engine-installed")); err != nil || strings.TrimSpace(string(b)) == "" {
		t.Errorf("the install record reads %q, %v", b, err)
	}
	// A copy an earlier setup left aside goes back before the next keep.
	leftover := filepath.Join(home, "plugins", ".engine-kept", "0.6.0")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	run(keepCommand(word))
	if got := versions(); !slices.Equal(got, []string{"0.6.0", "0.7.0", "0.7.1"}) {
		t.Fatalf("after a keep with a copy left aside the cache holds %q", got)
	}
	run(restoreCommand(word))
	run(pruneCommand(word, "0.7.1"))
	if got := versions(); !slices.Equal(got, []string{"0.7.1"}) {
		t.Errorf("after the prune the cache holds %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, "plugins", ".engine-installed")); !os.IsNotExist(err) {
		t.Errorf("the install record is left: %v", err)
	}
}
