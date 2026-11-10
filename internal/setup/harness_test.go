package setup

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/registry"
	"github.com/dinoderek/engine/internal/hosts"
)

// newCodexRig is a rig whose hosts list claude and codex, with a settled
// devbox.
func newCodexRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.home = callerHomeWith(t, codexHostsTOML)
	r.box = settledBox(r.home)
	return r
}

// The steps are numbered in order: the core's, then each listed harness's in
// the registry's order, then gh auth and the preflight, which checks every
// harness the host lists, the default first.
func TestEachListedHarnessAddsItsStepsInRegistryOrder(t *testing.T) {
	r := newCodexRig(t)
	if !mustRun(t, r, "box") {
		t.Fatalf("items left open:\n%s", r.out.String())
	}
	out := r.out.String()
	for _, want := range []string{
		"4. no suspend: ok", "5. plugin: ok", "6. sign-in: ok", "7. codex: ok", "8. codex sign-in: ok", "9. codex home: ok",
		"10. codex plugin: ok", "11. codex daemon: ok", "12. gh auth: ok", "13. preflight: ok", "checking --harness codex", "box is set up",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if got := strings.Join(r.checked, ","); got != "claude,codex" {
		t.Errorf("checks %q", got)
	}
}

// A host runs only the steps of the harnesses it lists, and needs only their
// tools.
func TestAHostRunsOnlyTheStepsOfItsHarnesses(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	if !mustRun(t, r, "box") {
		t.Fatalf("open:\n%s", r.out.String())
	}
	if out := r.out.String(); strings.Contains(out, "codex") || !strings.Contains(out, "8. preflight: ok") {
		t.Errorf("out:\n%s", out)
	}
	if got := strings.Join(r.checked, ","); got != "claude" {
		t.Errorf("checks %q", got)
	}

	r = newRig(t)
	toml := strings.NewReplacer(`harnesses = ["claude", "codex"]`, `harnesses = ["codex"]`,
		`bin = { claude = "~/bin/claude", codex = "~/.local/bin/codex" }`, `bin = { codex = "~/.local/bin/codex" }`).Replace(codexHostsTOML)
	r.home = callerHomeWith(t, toml)
	r.box = settledBox(r.home)
	if !mustRun(t, r, "box") {
		t.Fatalf("open:\n%s", r.out.String())
	}
	out := r.out.String()
	if strings.Contains(out, ". plugin:") || strings.Contains(out, ". sign-in:") || !strings.Contains(out, "5. codex: ok") || !strings.Contains(out, "11. preflight: ok") {
		t.Errorf("a Codex-only host:\n%s", out)
	}
	if !strings.HasPrefix(r.box.commands[0], "for t in go tmux gh git; do") {
		t.Errorf("a Codex-only host needs claude: %q", r.box.commands[0])
	}
	if got := strings.Join(r.checked, ","); got != "codex" {
		t.Errorf("checks %q", got)
	}
	if calls := strings.Join(r.claude.Calls(), ","); calls != "" {
		t.Errorf("claude's steps ran: %q", calls)
	}
}

func TestACodexCheckThatFailsLeavesCodexOpenAfterClaudePasses(t *testing.T) {
	r := newCodexRig(t)
	r.checkCodex = func() (bool, error) { return false, nil }
	if mustRun(t, r, "box") {
		t.Fatal("closed")
	}
	out := r.out.String()
	if !strings.Contains(out, "13. preflight: OPEN (a codex session would stop)") || !strings.Contains(out, "13. preflight: fix codex") {
		t.Errorf("out:\n%s", out)
	}
	if got := strings.Join(r.checked, ","); got != "claude,codex" {
		t.Errorf("checks %q", got)
	}
	r = newCodexRig(t)
	r.check = func() (bool, error) { return false, errors.New("no protocol") }
	mustRun(t, r, "box")
	if got := strings.Join(r.checked, ","); got != "claude" {
		t.Errorf("Codex was checked after Claude failed: %q", got)
	}
}

// The default harness is checked first, and once.
func TestADefaultCodexHarnessIsCheckedFirst(t *testing.T) {
	r := newCodexRig(t)
	r.home = callerHomeWith(t, strings.Replace(codexHostsTOML, `default_harness = "claude"`, `default_harness = "codex"`, 1))
	r.check = func() (bool, error) { return false, nil }
	if mustRun(t, r, "box") {
		t.Fatal("closed")
	}
	out := r.out.String()
	if got := strings.Join(r.checked, ","); got != "codex,claude" {
		t.Errorf("checks %q", got)
	}
	if strings.Contains(out, "checking --harness codex") || !strings.Contains(out, "checking --harness claude") {
		t.Errorf("out:\n%s", out)
	}
	if !strings.Contains(out, "fix claude") || strings.Contains(out, "fix codex") {
		t.Errorf("the failing harness's fix is not the one named:\n%s", out)
	}
}

// A harness the registry lacks still gets its check, whose failure names the
// rerun.
func TestACheckOfAnUnknownHarnessFailsOpen(t *testing.T) {
	r := newCodexRig(t)
	d := r.deps()
	d.Harnesses = harness.NewAdapters(r.claude)
	r.checkCodex = func() (bool, error) { return false, nil }
	r.out.Reset()
	closed, err := Run("box", d, &r.out)
	if closed || err != nil || !strings.Contains(r.out.String(), "an unattended codex session there would stop") {
		t.Errorf("closed %v, err %v:\n%s", closed, err, r.out.String())
	}
}

// This host's label names the harnesses whose steps act on it.
func TestAHostCheckingItselfNamesTheHarnessesThatAct(t *testing.T) {
	r := newCodexRig(t)
	r.codex.Steps[2].Acts = true
	if !mustRun(t, r, "") || !strings.Contains(r.out.String(), "mac (this host; the codex steps act)") {
		t.Errorf("label:\n%s", r.out.String())
	}
	if len(r.ssh) != 0 {
		t.Errorf("reached out: %q", r.ssh)
	}
}

// An adapter's steps see the host, the caller's plugin and the numbers of
// the report.
func TestAnAdaptersStepsSeeTheTarget(t *testing.T) {
	r := newCodexRig(t)
	var got []string
	r.codex.Steps[0].Run = func(t harness.Target) (harness.Outcome, error) {
		got = []string{t.Host().Name, t.Plugin(), t.Rerun(), t.Ref("tools", "codex"), t.Ref("codex home"), t.OnHost("x", true)}
		if _, err := t.Sh("uname -s", ""); err != nil || !t.Remote() {
			return harness.Outcome{}, errors.New("no shell")
		}
		return harness.Passed(""), nil
	}
	mustRun(t, r, "box")
	want := []string{"box", callerPlugin, "engine setup --host box", "steps 1 and 7", "step 9", "ssh -t dev@box 'x'"}
	if !slices.Equal(got, want) {
		t.Errorf("target %q, want %q", got, want)
	}
}

// The real adapters' steps never take a name the core or another adapter
// uses, so every reference names one step.
func TestTheRegistrysStepNamesAreUnique(t *testing.T) {
	set := registry.New(harness.Env{})
	r := newCodexRig(t)
	d := r.deps()
	d.Harnesses = set
	runner := &runner{d: d, t: target{name: "box", address: boxAddress}, host: hosts.Host{Name: "box", Harnesses: set.Names()}, missing: map[string]bool{}}
	runner.adapters = hostAdapters(set, runner.host)
	runner.steps(nil)
	seen := map[string]bool{}
	for _, name := range runner.names {
		if seen[name] {
			t.Errorf("step %q twice in %q", name, runner.names)
		}
		seen[name] = true
	}
	if len(runner.adapters) != len(set.Names()) {
		t.Errorf("adapters %d, want %d", len(runner.adapters), len(set.Names()))
	}
}

// A local check brings a harness's own copies to the user's plugin on this
// host, so engine's Codex home cannot stay behind it; with no plugin
// installed here, the check still runs and wants none.
func TestALocalCheckWantsThisHostsPlugin(t *testing.T) {
	for _, plugin := range []string{callerPlugin, ""} {
		r := newCodexRig(t)
		r.claude.PluginVersion = plugin
		var got []string
		for i := range r.codex.Steps {
			if r.codex.Steps[i].Name == "codex plugin" {
				r.codex.Steps[i].Run = func(tg harness.Target) (harness.Outcome, error) {
					got = append(got, tg.Plugin())
					return harness.Passed(""), nil
				}
			}
		}
		mustRun(t, r, "")
		if !slices.Equal(got, []string{plugin}) {
			t.Errorf("plugin %q: the codex plugin step wanted %q", plugin, got)
		}
	}
}
