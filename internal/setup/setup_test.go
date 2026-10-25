package setup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/remote"
)

func mustRun(t *testing.T, r *rig, host string) bool {
	t.Helper()
	closed, err := r.run(host)
	if err != nil {
		t.Fatalf("setup failed: %v\n%s", err, r.out.String())
	}
	return closed
}

func TestSetupBringsAStaleHostToTheCallersBuild(t *testing.T) {
	r := newRig(t)
	if !mustRun(t, r, "box") {
		t.Fatalf("items left open:\n%s", r.out.String())
	}
	out := r.out.String()
	for _, want := range []string{
		"1. tools: ok", "2. engine: changed (2a08a0c go1.27.3, was d8c1744 go1.27.2)", "3. plugin: changed (0.7.1, was 0.2.0)",
		"4. config: changed (wrote host, hosts.toml, github-token)", "5. sign-in: ok (claude.ai)",
		"6. no suspend: ok (masked)", "7. preflight: ok", "the check report", "box is set up",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	b := r.box
	if b.files["host"] != "box\n" || b.files["github-token"] != secret+"\n" || b.files["hosts.toml"] != hostsTOML {
		t.Errorf("files on the host: %q", b.files)
	}
	wantInstall := `GOBIN="$HOME/.local/bin" GOTOOLCHAIN=go1.27.3 go install github.com/dinoderek/engine/cmd/engine@` + callerRev
	if b.mutations[0] != wantInstall {
		t.Errorf("first change %q, want %q", b.mutations[0], wantInstall)
	}
	for _, c := range r.ssh {
		if !strings.HasPrefix(c, "-o BatchMode=yes -o ConnectTimeout=10 "+boxAddress+" ") {
			t.Errorf("ssh call without the options or the address: %s", c)
		}
		if strings.Contains(c, secret) {
			t.Errorf("the token is on a command line: %s", c)
		}
	}
	if strings.Contains(out, secret) {
		t.Errorf("the token is in the output:\n%s", out)
	}
}

func TestSetupTwiceChangesNothing(t *testing.T) {
	r := newRig(t)
	mustRun(t, r, "box")
	r.box.mutations = nil
	if !mustRun(t, r, "box") {
		t.Fatalf("the second run left items open:\n%s", r.out.String())
	}
	if len(r.box.mutations) != 0 {
		t.Errorf("the second run changed: %q", r.box.mutations)
	}
	if strings.Contains(r.out.String(), "changed") {
		t.Errorf("the second run reports a change:\n%s", r.out.String())
	}
}

func TestSettledHostChangesNothing(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	if !mustRun(t, r, "box") || len(r.box.mutations) != 0 {
		t.Errorf("closed or not, with changes %q:\n%s", r.box.mutations, r.out.String())
	}
}

// each case breaks one step on an otherwise settled host, and expects it
// open with the exact command, the exit 1 and nothing run after it that
// would mislead.
func TestEachStepFailsOpenWithItsCommand(t *testing.T) {
	cases := []struct {
		name    string
		step    string
		break_  func(r *rig)
		detail  string
		command string
	}{
		{"missing tools", "1. tools: OPEN", func(r *rig) { r.box.missing = []string{"tmux", "gh"} },
			"missing: tmux, gh", `ssh dev@box 'grep -qs "^export PATH=.HOME/.local/bin" ~/.bashrc || { { echo "export PATH=\$HOME/.local/bin:\$PATH"; { cat ~/.bashrc 2>/dev/null || :; }; } > ~/.bashrc.new && mv ~/.bashrc.new ~/.bashrc; }'`},
		{"go install fails", "2. engine: OPEN", func(r *rig) { r.box.rev, r.box.installFail = "d8c1744", true },
			"go install failed: go: network is unreachable", `ssh dev@box 'GOBIN="$HOME/.local/bin" GOTOOLCHAIN=go1.27.3 go install github.com/dinoderek/engine/cmd/engine@` + callerRev + `'`},
		{"another engine first on the PATH", "2. engine: OPEN", func(r *rig) { r.box.rev, r.box.installTo = "d8c1744", "d8c1744" },
			"the engine on the PATH is d8c1744", "~/.bashrc.new"},
		{"no engine at all and none installed", "2. engine: OPEN", func(r *rig) { r.box.rev, r.box.installTo = "", "-dirty" },
			"not the one installed", "~/.bashrc.new"},
		{"plugin update fails", "3. plugin: OPEN", func(r *rig) { r.box.plugin, r.box.pluginFail = "0.2.0", true },
			"claude plugin marketplace update engine failed: plugin: marketplace unreachable", "ssh dev@box 'claude plugin marketplace update engine'"},
		{"marketplace is older than the caller", "3. plugin: OPEN", func(r *rig) { r.box.plugin, r.box.pluginStuck = "0.2.0", "0.6.0" },
			"the plugin is 0.6.0 there and 0.7.1 here", "claude plugin update engine@engine --scope user"},
		{"write fails", "4. config: OPEN", func(r *rig) { r.box.writeFail, r.box.files = true, map[string]string{} },
			"write host failed: cat: No space left on device", "ssh dev@box 'mkdir -p ~/.config/engine' && scp -p "},
		{"signed in with an API key", "5. sign-in: OPEN", func(r *rig) { r.box.auth = `{"loggedIn":true,"authMethod":"api_key"}` },
			"not signed in with claude.ai", "ssh -t dev@box 'claude /login'"},
		{"signed out", "5. sign-in: OPEN", func(r *rig) { r.box.auth = "Not logged in" },
			"not signed in with claude.ai", "ssh -t dev@box 'claude /login'"},
		{"sleep not masked", "6. no suspend: OPEN", func(r *rig) { r.box.masked = 3 },
			"the host can suspend", "ssh -t dev@box 'sudo systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target'"},
		{"no systemd", "6. no suspend: OPEN", func(r *rig) { r.box.noSystemd = true },
			"the host can suspend", "sudo systemctl mask"},
		{"the check finds a prompt", "7. preflight: OPEN", func(r *rig) { r.check = func() (bool, error) { return false, nil } },
			"an unattended session there would stop", "ssh -t dev@box 'claude --permission-mode auto --remote-control'"},
		{"the check cannot run", "7. preflight: OPEN", func(r *rig) { r.check = func() (bool, error) { return false, errors.New("no protocol") } },
			"no protocol", "engine setup --host box"},
	}
	for _, c := range cases {
		r := newRig(t)
		r.box = settledBox(r.home)
		c.break_(r)
		if mustRun(t, r, "box") {
			t.Errorf("%s: closed", c.name)
		}
		out := r.out.String()
		if !strings.Contains(out, c.step) || !strings.Contains(out, c.detail) || !strings.Contains(out, c.command) {
			t.Errorf("%s: want %q, %q and %q in:\n%s", c.name, c.step, c.detail, c.command, out)
		}
	}
}

func TestPreflightWaitsForTheOtherItems(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	r.box.masked = 0
	mustRun(t, r, "box")
	if len(r.checked) != 0 || !strings.Contains(r.out.String(), "7. preflight: OPEN (not run while items above are open)") {
		t.Errorf("the check ran with an item open:\n%s", r.out.String())
	}
}

func TestMissingToolsBlockTheStepsThatNeedThem(t *testing.T) {
	r := newRig(t)
	r.box.missing = []string{"go", "claude"}
	mustRun(t, r, "box")
	out := r.out.String()
	for _, want := range []string{"2. engine: OPEN (go is missing (step 1))", "3. plugin: OPEN (claude is missing", "5. sign-in: OPEN (claude is missing"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	for _, m := range r.box.mutations {
		if strings.Contains(m, "go install") || strings.Contains(m, "claude plugin") {
			t.Errorf("ran %q without its tool", m)
		}
	}
}

func TestPluginWithoutMarketplaceIsAddedThenInstalled(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	r.box.plugin, r.box.marketplace = "", false
	if !mustRun(t, r, "box") {
		t.Fatalf("open:\n%s", r.out.String())
	}
	want := []string{"claude plugin marketplace add dinoderek/engine", "claude plugin install engine@engine --scope user"}
	if fmt.Sprint(r.box.mutations) != fmt.Sprint(want) {
		t.Errorf("changes %q, want %q", r.box.mutations, want)
	}
}

func TestPluginWithMarketplaceIsRefreshedThenUpdated(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	r.box.plugin = "0.2.0"
	mustRun(t, r, "box")
	want := []string{"claude plugin marketplace update engine", "claude plugin update engine@engine --scope user"}
	if fmt.Sprint(r.box.mutations) != fmt.Sprint(want) {
		t.Errorf("changes %q, want %q", r.box.mutations, want)
	}
}

func TestConfigFixesAWrongModeAndOverwritesADifferentFile(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	r.box.badMode["github-token"] = true
	r.box.files["hosts.toml"] = "old\n"
	r.box.files["host"] = "mac\n"
	if !mustRun(t, r, "box") {
		t.Fatalf("open:\n%s", r.out.String())
	}
	if r.box.files["hosts.toml"] != hostsTOML || r.box.files["host"] != "box\n" {
		t.Errorf("files not rewritten: %q", r.box.files)
	}
	var chmods, writes int
	for _, m := range r.box.mutations {
		switch {
		case strings.HasPrefix(m, "chmod 600"):
			chmods++
		case strings.HasPrefix(m, "umask 077"):
			writes++
		}
	}
	if chmods != 1 || writes != 2 {
		t.Errorf("%d chmods and %d writes: %q", chmods, writes, r.box.mutations)
	}
	for _, m := range r.box.mutations {
		if !strings.Contains(m, "chmod 600") {
			t.Errorf("a change that does not set mode 600: %q", m)
		}
	}
}

func TestRefusalsChangeNothing(t *testing.T) {
	cases := []struct {
		name  string
		host  string
		tweak func(r *rig)
		want  string
	}{
		{"an unknown host", "nope", func(*rig) {}, `host "nope" is not in hosts.toml; its machines are box, mac`},
		{"a dirty build", "box", func(r *rig) { r.info.Modified = true }, "modified working tree"},
		{"a build without a commit", "box", func(r *rig) { r.info.Revision = "" }, "has no commit"},
		{"a dirty go install build", "box", func(r *rig) {
			r.info.Revision, r.info.Version = "", "v0.0.0-20261025174939-2a08a0c1f2a2+dirty"
		}, "modified working tree"},
		{"a tagged build", "box", func(r *rig) { r.info.Revision, r.info.Version = "", "v1.2.3" }, "has no commit"},
		{"gh cannot say", "box", func(r *rig) {
			r.gh = func([]string, string, ...string) (string, error) { return "", errors.New("gh: Not Found (HTTP 404)") }
		}, "could not confirm commit " + callerRev + " is on GitHub (dinoderek/engine): gh: Not Found (HTTP 404)"},
		{"a different commit back", "box", func(r *rig) {
			r.gh = func([]string, string, ...string) (string, error) { return "deadbeef", nil }
		}, "is not on GitHub (dinoderek/engine); push it"},
		{"no plugin here", "box", func(r *rig) { r.local.plugin = "" }, "plugin is not installed for the user on this host"},
		{"no token file", "box", func(r *rig) {
			_ = os.Remove(filepath.Join(r.home, ".config", "engine", "github-token"))
		}, "no GH_TOKEN and no token file"},
		{"no host config", "box", func(r *rig) {
			_ = os.Remove(filepath.Join(r.home, ".config", "engine", "hosts.toml"))
		}, "read host config"},
	}
	for _, c := range cases {
		r := newRig(t)
		c.tweak(r)
		closed, err := r.run(c.host)
		if closed || err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasSuffix(err.Error(), "; nothing was changed") {
			t.Errorf("%s: closed %v, err %v; want %q", c.name, closed, err, c.want)
		}
		if len(r.ssh) != 0 || len(r.box.mutations) != 0 {
			t.Errorf("%s: reached the host: %q", c.name, r.ssh)
		}
	}
}

func TestAnUnreachableHostIsAnErrorNotAnOpenItem(t *testing.T) {
	r := newRig(t)
	r.box.down = true
	_, err := r.run("box")
	var down *remote.UnreachableError
	if !errors.As(err, &down) || down.Host != "box" || !strings.Contains(err.Error(), "No route to host") {
		t.Fatalf("err %v", err)
	}
	if len(r.ssh) != 1 {
		t.Errorf("kept calling a host that does not answer: %d calls", len(r.ssh))
	}
}

func TestACheckThatCannotReachTheHostIsAnError(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	r.check = func() (bool, error) {
		return false, &remote.UnreachableError{Host: "box", Address: boxAddress, Detail: "gone"}
	}
	if _, err := r.run("box"); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("err %v", err)
	}
}

func TestLocalModeOnlyChecks(t *testing.T) {
	for _, host := range []string{"", "mac"} {
		r := newRig(t)
		r.local.rev = "2a08a0c"
		if !mustRun(t, r, host) {
			t.Fatalf("host %q left items open:\n%s", host, r.out.String())
		}
		if len(r.ssh) != 0 || len(r.local.mutations) != 0 {
			t.Errorf("host %q reached out or changed: %q %q", host, r.ssh, r.local.mutations)
		}
		out := r.out.String()
		for _, want := range []string{"1. tools: ok", "2. engine: ok", "3. plugin: ok (0.7.1)", "5. sign-in: ok", "6. no suspend: ok (not Linux)", "7. preflight: ok"} {
			if !strings.Contains(out, want) {
				t.Errorf("host %q lacks %q:\n%s", host, want, out)
			}
		}
		if strings.Contains(out, "4. config") {
			t.Errorf("a local check copies config:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(r.home, ".config", "engine", "hosts.toml")); err != nil {
			t.Error(err)
		}
	}
}

func TestLocalModeNeedsNoPushedBuild(t *testing.T) {
	r := newRig(t)
	r.info.Modified = true
	r.gh = func([]string, string, ...string) (string, error) { return "", errors.New("must not be called") }
	if !mustRun(t, r, "") {
		t.Errorf("a dirty build checking itself failed:\n%s", r.out.String())
	}
}

func TestLocalModeOpenItems(t *testing.T) {
	r := newRig(t)
	r.local.missing = []string{"gh"}
	r.local.plugin = ""
	r.local.rev = ""
	r.local.auth = "Not logged in"
	r.check = func() (bool, error) { t.Error("the check ran with items open"); return true, nil }
	if mustRun(t, r, "") {
		t.Fatal("closed")
	}
	out := r.out.String()
	for _, want := range []string{
		"1. tools: OPEN (missing: gh", "~/.zshenv.new",
		"2. engine: OPEN (engine is not on the PATH)",
		"3. plugin: OPEN", "claude plugin marketplace add dinoderek/engine && claude plugin install engine@engine --scope user",
		"5. sign-in: OPEN", ": claude /login", "5 open item(s); run each command, then rerun engine setup:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

// The engine that runs setup may itself come from go install module@commit,
// which records the commit only in its pseudo-version.
func TestSetupFromAGoInstallBuild(t *testing.T) {
	r := newRig(t)
	r.info.Revision, r.info.Version = "", "v0.0.0-20261025105230-"+callerRev[:12]
	if !mustRun(t, r, "box") {
		t.Fatalf("open:\n%s", r.out.String())
	}
	want := `GOBIN="$HOME/.local/bin" GOTOOLCHAIN=go1.27.3 go install github.com/dinoderek/engine/cmd/engine@` + callerRev[:12]
	if r.box.mutations[0] != want {
		t.Errorf("first change %q, want %q", r.box.mutations[0], want)
	}
	r.box.mutations = nil
	mustRun(t, r, "box")
	if len(r.box.mutations) != 0 {
		t.Errorf("a go install build reinstalled: %q", r.box.mutations)
	}
}

// go install module@commit ignores the module's toolchain line, so the install
// line pins it, and a right commit built with another Go is not current.
func TestAHostOnTheRightCommitWithAnotherGoIsReinstalled(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	r.box.goVer = "go1.27.1"
	if !mustRun(t, r, "box") {
		t.Fatalf("open:\n%s", r.out.String())
	}
	if !strings.Contains(r.out.String(), "2. engine: changed (2a08a0c go1.27.3, was 2a08a0c go1.27.1)") {
		t.Errorf("not reported changed:\n%s", r.out.String())
	}
	want := `GOBIN="$HOME/.local/bin" GOTOOLCHAIN=go1.27.3 go install github.com/dinoderek/engine/cmd/engine@` + callerRev
	if len(r.box.mutations) != 1 || r.box.mutations[0] != want {
		t.Errorf("changes %q, want only %q", r.box.mutations, want)
	}
}

func TestAHostOnTheRightCommitAndGoIsNotReinstalled(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	if !mustRun(t, r, "box") || !strings.Contains(r.out.String(), "2. engine: ok (2a08a0c go1.27.3)") || len(r.box.mutations) != 0 {
		t.Errorf("changes %q:\n%s", r.box.mutations, r.out.String())
	}
}

func TestAToolchainThatCannotBeReadLeavesTheEngineOpen(t *testing.T) {
	cases := []struct {
		name  string
		tweak func(r *rig)
		want  string
	}{
		{"no toolchain line", func(r *rig) { r.goMod = "module x\n\ngo 1.27.0\n" }, "go.mod at 2a08a0c has no toolchain line"},
		{"not a Go release", func(r *rig) { r.goMod = "module x\n\ntoolchain local\n" }, "is not a Go release"},
		{"GitHub cannot serve go.mod", func(r *rig) {
			gh := r.gh
			r.gh = func(env []string, name string, args ...string) (string, error) {
				if strings.Contains(strings.Join(args, " "), "go.mod") {
					return "", errors.New("gh: Not Found (HTTP 404)")
				}
				return gh(env, name, args...)
			}
		}, "could not read go.mod at 2a08a0c (dinoderek/engine): gh: Not Found (HTTP 404)"},
	}
	for _, c := range cases {
		r := newRig(t)
		c.tweak(r)
		if mustRun(t, r, "box") {
			t.Errorf("%s: closed", c.name)
		}
		out := r.out.String()
		if !strings.Contains(out, "2. engine: OPEN (") || !strings.Contains(out, c.want) || !strings.Contains(out, "2. engine: engine setup --host box") {
			t.Errorf("%s: want %q open with the rerun command:\n%s", c.name, c.want, out)
		}
		for _, m := range r.box.mutations {
			if strings.Contains(m, "go install") {
				t.Errorf("%s: installed without a toolchain: %q", c.name, m)
			}
		}
	}
}

func TestADirtyEngineOnTheHostIsReplaced(t *testing.T) {
	r := newRig(t)
	r.box = settledBox(r.home)
	r.box.rev = "d8c1744-dirty"
	if !mustRun(t, r, "box") || !strings.Contains(r.out.String(), "2. engine: changed (2a08a0c go1.27.3, was d8c1744-dirty go1.27.3)") {
		t.Errorf("open or unchanged:\n%s", r.out.String())
	}
}

// The PATH line is run for real: it must put the line first, keep the rest of
// the file, make the file when it is missing, and add nothing the second time.
func TestPathLineInARealShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
	r := &runner{d: Deps{Getenv: func(string) string { return "/bin/bash" }}, t: target{name: "this"}}
	cmd := r.pathLine()
	const line = "export PATH=$HOME/.local/bin:$PATH\n"
	for name, before := range map[string]string{"missing": "", "empty": "", "existing": "case $- in *i*) ;; *) return;; esac\nalias l=ls\n"} {
		home := t.TempDir()
		rc := filepath.Join(home, ".bashrc")
		if name != "missing" {
			if err := os.WriteFile(rc, []byte(before), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 2; i++ {
			c := exec.Command("sh", "-c", cmd)
			c.Env = append(os.Environ(), "HOME="+home)
			if out, err := c.CombinedOutput(); err != nil {
				t.Fatalf("%s run %d: %v: %s", name, i, err, out)
			}
		}
		got, err := os.ReadFile(rc)
		if err != nil || string(got) != line+before {
			t.Errorf("%s: %q, %v; want %q", name, got, err, line+before)
		}
	}
}

func TestPathLineFollowsTheShellOfThisHost(t *testing.T) {
	for shell, want := range map[string]string{"/bin/zsh": "~/.zshenv", "/bin/bash": "~/.bashrc", "": "~/.bashrc"} {
		r := &runner{d: Deps{Getenv: func(string) string { return shell }}, t: target{name: "this"}}
		if got := r.pathLine(); !strings.Contains(got, want+".new") {
			t.Errorf("SHELL=%q: %s, want %s", shell, got, want)
		}
	}
	r := &runner{d: Deps{Getenv: func(string) string { return "/bin/zsh" }}, t: target{name: "box", address: boxAddress}}
	if got := r.pathLine(); !strings.HasPrefix(got, "ssh dev@box ") || !strings.Contains(got, "~/.bashrc.new") {
		t.Errorf("another host: %s", got)
	}
}
