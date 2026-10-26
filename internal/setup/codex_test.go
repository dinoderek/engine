package setup

import (
	"errors"
	"strings"
	"testing"
)

const (
	boxHome    = "/home/dev"
	boxCodex   = boxHome + "/.local/bin/codex"
	boxEngineH = boxHome + "/.local/share/engine/codex"
)

// newCodexRig is a rig whose hosts list codex, with a devbox that is set up
// except for Codex's engine home and daemon, which the case decides.
func newCodexRig(t *testing.T, codex *codexState) *rig {
	t.Helper()
	r := newRig(t)
	r.home = callerHomeWith(t, codexHostsTOML)
	r.box = settledBox(r.home)
	r.box.codex = codex
	return r
}

func TestCodexStepsOnASettledHost(t *testing.T) {
	r := newCodexRig(t, settledCodex(boxHome))
	if !mustRun(t, r, "box") {
		t.Fatalf("items left open:\n%s", r.out.String())
	}
	out := r.out.String()
	for _, want := range []string{
		"7. codex: ok (codex-cli 0.164.0)", "8. codex sign-in: ok (Logged in using ChatGPT)", "9. codex home: ok (" + boxEngineH + ")",
		"10. codex plugin: ok (0.7.1)", "11. codex daemon: ok (running; remote control on)", "12. preflight: ok",
		"checking --harness codex", "box is set up",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if len(r.box.mutations) != 0 {
		t.Errorf("a settled host changed: %q", r.box.mutations)
	}
	if got := strings.Join(r.checked, ","); got != "checked,codex" {
		t.Errorf("checks %q", got)
	}
}

func TestCodexSetsUpAFreshHostAndStaysSafeToRerun(t *testing.T) {
	c := freshCodex(boxHome)
	r := newCodexRig(t, c)
	if !mustRun(t, r, "box") {
		t.Fatalf("items left open:\n%s", r.out.String())
	}
	out := r.out.String()
	for _, want := range []string{
		"9. codex home: changed (wrote config.toml, linked the login)", "10. codex plugin: changed (0.7.1, was none)",
		"11. codex daemon: changed (started; remote control on)", "12. preflight: ok",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if c.config == nil || !strings.HasPrefix(*c.config, engineBlock(boxHome)) ||
		!strings.Contains(*c.config, `writable_roots = ["/home/dev/.local/share/engine/repos"]`) {
		t.Errorf("config.toml: %v", c.config)
	}
	for _, cmd := range r.box.commands {
		if strings.Contains(cmd, secret) {
			t.Errorf("the token is on a command line: %q", cmd)
		}
	}
	start := lastWith(r.box.mutations, "remote-control start")
	if !strings.Contains(start, `GH_TOKEN="$t" CODEX_HOME=`+boxEngineH+" "+boxCodex+" remote-control start") ||
		!strings.Contains(start, "cat "+boxHome+"/.config/engine/github-token") {
		t.Errorf("start %q", start)
	}

	changes := len(r.box.mutations)
	if !mustRun(t, r, "box") || len(r.box.mutations) != changes || c.started != 1 {
		t.Errorf("a rerun changed %q (started %d times):\n%s", r.box.mutations[changes:], c.started, r.out.String())
	}
}

func lastWith(cmds []string, sub string) string {
	for i := len(cmds) - 1; i >= 0; i-- {
		if strings.Contains(cmds[i], sub) {
			return cmds[i]
		}
	}
	return ""
}

func TestACodexStepThatFailsStaysOpen(t *testing.T) {
	cases := []struct {
		name, step, detail, command string
		break_                      func(c *codexState)
	}{
		{"no binary anywhere", "7. codex: OPEN", "codex does not run at " + boxCodex, "install Codex there",
			func(c *codexState) { c.bin = "" }},
		{"the binary is elsewhere", "7. codex: OPEN", "codex is at /usr/bin/codex, not at " + boxCodex,
			"ssh dev@box 'mkdir -p /home/dev/.local/bin && ln -sf /usr/bin/codex " + boxCodex + "'",
			func(c *codexState) { c.bin, c.onPath = "", "/usr/bin/codex" }},
		{"signed out", "8. codex sign-in: OPEN (not signed in to Codex)", "", "ssh -t dev@box '" + boxCodex + " login --device-auth'",
			func(c *codexState) { c.signedIn = false }},
		{"config cannot be written", "9. codex home: OPEN", "write config.toml failed: cat: No space left on device", "engine setup --host box",
			func(c *codexState) { c.config, c.writeFail = nil, true }},
		{"the operator's login is not a file", "9. codex home: OPEN", "the operator's login is not a file at /home/dev/.codex/auth.json",
			"ssh -t dev@box 'CODEX_HOME=" + boxEngineH + " " + boxCodex + " login'",
			func(c *codexState) { c.authLink, c.userAuth = "", false }},
		{"engine's home rejects the login", "9. codex home: OPEN (engine's Codex home is not signed in)", "", "login'",
			func(c *codexState) { c.loginBroken = true }},
		{"the operator's home lacks the plugin", "10. codex plugin: OPEN", "not installed in the operator's Codex home",
			"ssh dev@box '" + boxCodex + " plugin marketplace add dinoderek/engine && " + boxCodex + " plugin add engine@engine'",
			func(c *codexState) { c.userPlugin = "" }},
		{"the marketplace is unreachable", "10. codex plugin: OPEN", "codex plugin marketplace add dinoderek/engine failed: marketplace unreachable",
			"ssh dev@box 'CODEX_HOME=" + boxEngineH + " " + boxCodex + " plugin marketplace add dinoderek/engine'",
			func(c *codexState) { c.plugin, c.marketFail = "", true }},
		{"the plugin is not enabled after the install", "10. codex plugin: OPEN", "is not enabled in engine's Codex home after the install", "engine setup --host box",
			func(c *codexState) { c.plugin, c.leavesNothing = "", true }},
		{"the marketplace is older than the caller", "10. codex plugin: OPEN", "gives Codex plugin 0.6.0 there, older than this host's 0.7.1", "engine setup --host box",
			func(c *codexState) { c.plugin, c.pluginLeaves = "0.2.0", "0.6.0" }},
		{"the daemon cannot start", "11. codex daemon: OPEN", "engine's daemon is not running after the start: cat: github-token: No such file or directory",
			"ssh dev@box 't=\"$(cat /home/dev/.config/engine/github-token)\"",
			func(c *codexState) { c.daemonUp, c.rcOn, c.startFail = false, false, true }},
		{"remote control stays off", "11. codex daemon: OPEN", "remote control is off after the start", "remote-control start",
			func(c *codexState) { c.daemonUp, c.rcOn, c.startNoRC = false, false, true }},
		{"the check finds a prompt", "12. preflight: OPEN", "an unattended session there would stop", "ssh -t dev@box 'claude --permission-mode auto --remote-control'",
			func(*codexState) {}},
	}
	for _, c := range cases {
		codex := settledCodex(boxHome)
		c.break_(codex)
		r := newCodexRig(t, codex)
		if c.name == "the check finds a prompt" {
			r.check = func() (bool, error) { return false, nil }
		}
		if mustRun(t, r, "box") {
			t.Errorf("%s: closed", c.name)
		}
		out := r.out.String()
		if !strings.Contains(out, c.step) || !strings.Contains(out, c.detail) || !strings.Contains(out, c.command) {
			t.Errorf("%s: want %q, %q and %q in:\n%s", c.name, c.step, c.detail, c.command, out)
		}
	}
}

func TestWithoutTheBinaryTheOtherCodexStepsWait(t *testing.T) {
	codex := settledCodex(boxHome)
	codex.bin = ""
	r := newCodexRig(t, codex)
	mustRun(t, r, "box")
	out := r.out.String()
	for _, want := range []string{"8. codex sign-in: OPEN (codex is not usable (step 7))", "9. codex home: OPEN (codex is not usable", "10. codex plugin: OPEN (codex is not usable", "11. codex daemon: OPEN (codex is not usable"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if len(r.box.mutations) != 0 || len(r.checked) != 0 {
		t.Errorf("changed or checked with Codex missing: %q %q", r.box.mutations, r.checked)
	}
}

func TestBadHomeOrBinConfigStopsTheCodexSteps(t *testing.T) {
	codex := settledCodex("")
	r := newCodexRig(t, codex)
	mustRun(t, r, "box")
	if out := r.out.String(); !strings.Contains(out, "7. codex: OPEN (could not read the home directory") {
		t.Errorf("an empty home:\n%s", out)
	}
	codex = settledCodex(boxHome)
	r = newCodexRig(t, codex)
	r.home = callerHomeWith(t, strings.ReplaceAll(codexHostsTOML, `codex = "~/.local/bin/codex"`, `codex = "bin/codex"`))
	mustRun(t, r, "box")
	if out := r.out.String(); !strings.Contains(out, `7. codex: OPEN (host box: bin for codex is "bin/codex"`) {
		t.Errorf("a relative bin:\n%s", out)
	}
}

func TestCodexSetupKeepsWhatIsAlreadyRight(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(c *codexState)
		did    string
	}{
		{"an own login stays", func(c *codexState) { c.authLink = "FILE" }, ""},
		{"a link elsewhere is repointed", func(c *codexState) { c.authLink = "/old/auth.json" }, "linked the login"},
		{"the daemon runs with remote control off", func(c *codexState) { c.rcOn = false }, ""},
	}
	for _, c := range cases {
		codex := settledCodex(boxHome)
		c.break_(codex)
		r := newCodexRig(t, codex)
		if !mustRun(t, r, "box") {
			t.Errorf("%s: left items open:\n%s", c.name, r.out.String())
		}
		linked := strings.Contains(strings.Join(r.box.mutations, "\n"), "ln -sfn")
		if linked != (c.did != "") {
			t.Errorf("%s: linked %v, mutations %q", c.name, linked, r.box.mutations)
		}
	}
}

func TestAStartThatExitsOnAnErroredConnectionStillStartedTheDaemon(t *testing.T) {
	codex := settledCodex(boxHome)
	codex.daemonUp, codex.rcOn, codex.startExit1 = false, false, true
	r := newCodexRig(t, codex)
	if !mustRun(t, r, "box") || !strings.Contains(r.out.String(), "11. codex daemon: changed (started; remote control on)") {
		t.Errorf("errored start:\n%s", r.out.String())
	}
}

func TestCodexReportsTheOperatorsOwnState(t *testing.T) {
	codex := settledCodex(boxHome)
	codex.operatorRC = result{errOut: "\x1b[2m2026-10-26T10:01:27Z\x1b[0m \x1b[31mERROR\x1b[0m Codex is ignoring 1 unrecognized configuration setting.\n" +
		"  user (/home/dev/.codex/config.toml): `features.rcmp_client` is ignored.\n" +
		"Error: Remote control is enabled on box but the connection is errored.\n", code: 1}
	r := newCodexRig(t, codex)
	if !mustRun(t, r, "box") {
		t.Fatalf("reporting is not an open item:\n%s", r.out.String())
	}
	want := "7. codex: ok (codex-cli 0.164.0; the operator's remote control is errored; features.rcmp_client is ignored in /home/dev/.codex/config.toml)"
	if !strings.Contains(r.out.String(), want) {
		t.Errorf("want %q in:\n%s", want, r.out.String())
	}
}

func TestWithoutAnOperatorDaemonNothingIsRead(t *testing.T) {
	codex := settledCodex(boxHome)
	codex.operatorDaemon = false
	r := newCodexRig(t, codex)
	if !mustRun(t, r, "box") || !strings.Contains(r.out.String(), "7. codex: ok (codex-cli 0.164.0)\n") {
		t.Fatalf("open or noisy:\n%s", r.out.String())
	}
	for _, cmd := range r.box.commands {
		if cmd == boxCodex+" remote-control --json" {
			t.Errorf("ran %q, which starts a foreground server when no daemon runs", cmd)
		}
	}
}

func TestACodexCheckThatFailsLeavesCodexOpenAfterClaudePasses(t *testing.T) {
	r := newCodexRig(t, settledCodex(boxHome))
	r.checkCodex = func() (bool, error) { return false, nil }
	if mustRun(t, r, "box") {
		t.Fatal("closed")
	}
	out := r.out.String()
	if !strings.Contains(out, "12. preflight: OPEN (an unattended Codex session there would stop") || !strings.Contains(out, "engine start --check --harness codex --host box") {
		t.Errorf("out:\n%s", out)
	}
	if got := strings.Join(r.checked, ","); got != "checked,codex" {
		t.Errorf("checks %q", got)
	}
	r = newCodexRig(t, settledCodex(boxHome))
	r.check = func() (bool, error) { return false, errors.New("no protocol") }
	mustRun(t, r, "box")
	if got := strings.Join(r.checked, ","); got != "checked" {
		t.Errorf("Codex was checked after Claude failed: %q", got)
	}
}

func TestADefaultCodexHarnessIsCheckedOnce(t *testing.T) {
	r := newCodexRig(t, settledCodex(boxHome))
	r.home = callerHomeWith(t, strings.Replace(codexHostsTOML, `default_harness = "claude"`, `default_harness = "codex"`, 1))
	if !mustRun(t, r, "box") {
		t.Fatalf("open:\n%s", r.out.String())
	}
	if got := strings.Join(r.checked, ","); got != "checked" {
		t.Errorf("checks %q", got)
	}
	if strings.Contains(r.out.String(), "checking --harness codex") {
		t.Errorf("checked Codex twice:\n%s", r.out.String())
	}
}

func TestACodexHostCheckingItselfAlsoActs(t *testing.T) {
	codex := freshCodex("/Users/me")
	codex.plugin = ""
	r := newCodexRig(t, nil)
	r.local.codex = codex
	if !mustRun(t, r, "") {
		t.Fatalf("items left open:\n%s", r.out.String())
	}
	out := r.out.String()
	for _, want := range []string{"mac (this host; the Codex steps act)", "6. no suspend: ok (not Linux)", "7. codex: ok", "9. codex home: changed (wrote config.toml, linked the login)",
		"10. codex plugin: changed (0.7.1, was none)", "11. codex daemon: changed", "12. preflight: ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if len(r.ssh) != 0 {
		t.Errorf("reached out: %q", r.ssh)
	}
	r.local.codex.plugin = "0.1.0"
	if !mustRun(t, r, "") || !strings.Contains(r.out.String(), "10. codex plugin: ok (0.1.0)") {
		t.Errorf("a local host holds any installed version:\n%s", r.out.String())
	}
}

func TestAHostWithoutCodexRunsNoCodexSteps(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	if !mustRun(t, r, "box") {
		t.Fatalf("open:\n%s", r.out.String())
	}
	if out := r.out.String(); strings.Contains(out, "codex") || !strings.Contains(out, "7. preflight: ok") {
		t.Errorf("out:\n%s", out)
	}
	if got := strings.Join(r.checked, ","); got != "checked" {
		t.Errorf("checks %q", got)
	}
}

func TestAFailingDefaultCodexCheckNamesTheCodexRemedy(t *testing.T) {
	r := newCodexRig(t, settledCodex(boxHome))
	r.home = callerHomeWith(t, strings.Replace(codexHostsTOML, `default_harness = "claude"`, `default_harness = "codex"`, 1))
	r.check = func() (bool, error) { return false, nil }
	if mustRun(t, r, "box") {
		t.Fatal("closed")
	}
	out := r.out.String()
	if !strings.Contains(out, "engine start --check --harness codex --host box") || strings.Contains(out, "claude --permission-mode") {
		t.Errorf("out:\n%s", out)
	}
}

func TestTheDaemonWaitsForTheLoginAndTheHome(t *testing.T) {
	for _, c := range []struct {
		name   string
		break_ func(*codexState)
	}{
		{"signed out", func(c *codexState) { c.signedIn = false }},
		{"the home cannot be written", func(c *codexState) { c.config, c.writeFail = nil, true }},
	} {
		codex := freshCodex(boxHome)
		c.break_(codex)
		r := newCodexRig(t, codex)
		mustRun(t, r, "box")
		if !strings.Contains(r.out.String(), "11. codex daemon: OPEN (engine's Codex home is not ready (steps 8 and 9))") || codex.started != 0 {
			t.Errorf("%s: started %d:\n%s", c.name, codex.started, r.out.String())
		}
	}
}
