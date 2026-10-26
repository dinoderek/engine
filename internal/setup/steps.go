package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/version"
)

// tools are what a host needs on its non-interactive PATH.
const tools = "go tmux gh git claude"

// engineModule is the package `go install` builds.
const engineModule = "github.com/dinoderek/engine/cmd/engine"

// rerun is the command that repeats this setup.
func (r *runner) rerun() string { return "engine setup" + r.t.flag() }

// blocked is a step that needs a tool step 1 did not find.
func (r *runner) blocked(tool string) outcome {
	return open(tool+" is missing (step 1)", r.rerun())
}

// pathLine is the command that puts ~/.local/bin on the PATH that
// non-interactive shells get: a line at the top of the shell's rc file, above
// any line that returns for them. It does nothing when the line is there.
func (r *runner) pathLine() string {
	rc := "~/.bashrc"
	if !r.t.remote() && strings.HasSuffix(r.d.Getenv("SHELL"), "zsh") {
		rc = "~/.zshenv"
	}
	line := `export PATH=\$HOME/.local/bin:\$PATH`
	cmd := fmt.Sprintf(`grep -qs "^export PATH=.HOME/.local/bin" %[1]s || { { echo "%[2]s"; { cat %[1]s 2>/dev/null || :; }; } > %[1]s.new && mv %[1]s.new %[1]s; }`, rc, line)
	return r.t.onHost(cmd, false)
}

// tools lists the tools the host lacks, and the PATH line for the usual cause.
func (r *runner) tools() (outcome, error) {
	res, err := r.t.sh(`for t in `+tools+`; do command -v "$t" >/dev/null 2>&1 || echo "$t"; done`, "")
	if err != nil {
		return outcome{}, err
	}
	missing := res.lines()
	for _, m := range missing {
		r.missing[m] = true
	}
	if len(missing) == 0 {
		return passed(""), nil
	}
	return open("missing: "+strings.Join(missing, ", ")+"; install any the PATH line does not reveal", r.pathLine()), nil
}

// engineBuild is the engine on the target's PATH: its short commit, with
// "-dirty" when it was built from a modified tree, and the Go that built it.
// Both are empty when the target has no engine.
type engineBuild struct{ rev, goVersion string }

func (e engineBuild) String() string {
	if e.rev == "" {
		return "none"
	}
	if e.goVersion == "" {
		return e.rev
	}
	return e.rev + " " + e.goVersion
}

// hostEngine reads the engine on the target's PATH.
func (r *runner) hostEngine() (engineBuild, error) {
	res, err := r.t.sh("engine version", "")
	if err != nil || res.code != 0 {
		return engineBuild{}, err
	}
	info := version.Parse(res.out)
	commit := info.Commit()
	if len(commit) < shortRev {
		return engineBuild{}, nil
	}
	rev := commit[:shortRev]
	if info.Dirty() {
		rev += "-dirty"
	}
	return engineBuild{rev: rev, goVersion: info.Go}, nil
}

// shortRev is how much of a commit setup compares.
const shortRev = 7

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// installEngine puts the caller's commit at ~/.local/bin/engine, built with
// the Go go.mod names at that commit, and checks that it is the engine the
// PATH finds. An engine on the right commit built with another Go is replaced.
func (r *runner) installEngine() (outcome, error) {
	if r.want.toolchainErr != nil {
		return open(r.want.toolchainErr.Error()+"; setup cannot tell which Go the engine needs", r.rerun()), nil
	}
	want := engineBuild{rev: r.want.commit[:shortRev], goVersion: r.want.toolchain}
	have, err := r.hostEngine()
	if err != nil || have == want {
		return passed(want.String()), err
	}
	if r.missing["go"] {
		return r.blocked("go"), nil
	}
	install := fmt.Sprintf(`GOBIN="$HOME/.local/bin" GOTOOLCHAIN=%s go install %s@%s`, want.goVersion, engineModule, r.want.commit)
	res, err := r.t.sh(install, "")
	if err != nil {
		return outcome{}, err
	}
	if res.code != 0 {
		return open("go install failed: "+res.why(), r.t.onHost(install, false)), nil
	}
	after, err := r.hostEngine()
	if err != nil {
		return outcome{}, err
	}
	if after != want {
		return open("the engine on the PATH is "+after.String()+", not the one installed in ~/.local/bin ("+want.String()+")", r.pathLine()), nil
	}
	return changed(fmt.Sprintf("%s, was %s", want, have)), nil
}

// enginePath checks that this host finds engine on its PATH.
func (r *runner) enginePath() (outcome, error) {
	res, err := r.t.sh("command -v engine", "")
	if err != nil {
		return outcome{}, err
	}
	if res.code != 0 {
		return open("engine is not on the PATH", r.pathLine()), nil
	}
	return passed(strings.TrimSpace(res.out)), nil
}

// pluginEntry is one row of `claude plugin list --json`.
type pluginEntry struct{ ID, Version, Scope string }

// pluginID is engine's plugin, from its own marketplace.
const pluginID = "engine@engine"

// userPlugin is the version of the engine plugin installed for the user, and
// whether there is one. Project copies are not the user's.
func userPlugin(listJSON string) (string, bool) {
	var entries []pluginEntry
	if json.Unmarshal([]byte(listJSON), &entries) != nil {
		return "", false
	}
	for _, e := range entries {
		if e.ID == pluginID && e.Scope == "user" {
			return e.Version, true
		}
	}
	return "", false
}

// hostPlugin is the version of the user's engine plugin on the target.
func (r *runner) hostPlugin() (string, bool, error) {
	res, err := r.t.sh("claude plugin list --json", "")
	if err != nil {
		return "", false, err
	}
	version, found := userPlugin(res.out)
	return version, found, nil
}

// hasMarketplace reports whether engine's marketplace is added on the target.
func (r *runner) hasMarketplace() (bool, error) {
	res, err := r.t.sh("claude plugin marketplace list --json", "")
	if err != nil {
		return false, err
	}
	var markets []struct{ Name string }
	if json.Unmarshal([]byte(res.out), &markets) != nil {
		return false, nil
	}
	for _, m := range markets {
		if m.Name == "engine" {
			return true, nil
		}
	}
	return false, nil
}

// pluginCommands bring the user's engine plugin to the marketplace's latest:
// add or refresh the marketplace, then install or update the plugin.
func pluginCommands(marketplace, installed bool) []string {
	cmds := []string{"claude plugin marketplace add dinoderek/engine"}
	if marketplace {
		cmds = []string{"claude plugin marketplace update engine"}
	}
	if installed {
		return append(cmds, "claude plugin update "+pluginID+" --scope user")
	}
	return append(cmds, "claude plugin install "+pluginID+" --scope user")
}

// installPlugin brings the user's engine plugin to the caller's version.
func (r *runner) installPlugin() (outcome, error) {
	if r.missing["claude"] {
		return r.blocked("claude"), nil
	}
	have, installed, err := r.hostPlugin()
	if err != nil || (installed && have == r.want.plugin) {
		return passed(r.want.plugin), err
	}
	marketplace, err := r.hasMarketplace()
	if err != nil {
		return outcome{}, err
	}
	for _, cmd := range pluginCommands(marketplace, installed) {
		res, err := r.t.sh(cmd, "")
		if err != nil {
			return outcome{}, err
		}
		if res.code != 0 {
			return open(cmd+" failed: "+res.why(), r.t.onHost(cmd, false)), nil
		}
	}
	after, _, err := r.hostPlugin()
	if err != nil {
		return outcome{}, err
	}
	if after != r.want.plugin {
		return open(fmt.Sprintf("the plugin is %s there and %s here; update it here first", orNone(after), r.want.plugin),
			"claude plugin update "+pluginID+" --scope user"), nil
	}
	return changed(fmt.Sprintf("%s, was %s", after, orNone(have))), nil
}

// pluginInstalled checks that the user has the engine plugin on this host.
func (r *runner) pluginInstalled() (outcome, error) {
	have, found, err := r.hostPlugin()
	if err != nil {
		return outcome{}, err
	}
	if !found {
		return open(pluginID+" is not installed for the user",
			"claude plugin marketplace add dinoderek/engine && claude plugin install "+pluginID+" --scope user"), nil
	}
	return passed(have), nil
}

// signIn checks that Claude is signed in with a claude.ai account, which
// Remote Control needs.
func (r *runner) signIn() (outcome, error) {
	if r.missing["claude"] {
		return r.blocked("claude"), nil
	}
	res, err := r.t.sh("claude auth status", "")
	if err != nil {
		return outcome{}, err
	}
	var status struct {
		LoggedIn   bool
		AuthMethod string
	}
	_ = json.Unmarshal([]byte(res.out), &status) // unreadable output reads as signed out
	if status.LoggedIn && status.AuthMethod == "claude.ai" {
		return passed("claude.ai"), nil
	}
	return open("not signed in with claude.ai", r.t.onHost("claude /login", true)), nil
}

// sleepTargets are what a Linux host masks so it never suspends.
const sleepTargets = "sleep.target suspend.target hibernate.target hybrid-sleep.target"

// noSuspend checks, on Linux, that the sleep targets are masked.
func (r *runner) noSuspend() (outcome, error) {
	res, err := r.t.sh("uname -s; systemctl is-enabled "+sleepTargets+" 2>/dev/null", "")
	if err != nil {
		return outcome{}, err
	}
	lines := res.lines()
	if len(lines) == 0 || lines[0] != "Linux" {
		return passed("not Linux"), nil
	}
	masked := 0
	for _, l := range lines[1:] {
		if l == "masked" {
			masked++
		}
	}
	if masked == len(strings.Fields(sleepTargets)) {
		return passed("masked"), nil
	}
	return open("the host can suspend", r.t.onHost("sudo systemctl mask "+sleepTargets, true)), nil
}

// preflight runs `engine start --check` on the target, once nothing else is
// open: a check cannot pass past a missing tool, a signed-out Claude or a
// missing Codex. A host that lists codex is checked for Codex as well.
func (r *runner) preflight(out io.Writer) (outcome, error) {
	if r.opened > 0 {
		return open("not run while items above are open", r.rerun()), nil
	}
	harnesses := []string{""}
	if r.codex != nil && r.defaultHarness != codexHarness {
		harnesses = append(harnesses, codexHarness)
	}
	for _, harness := range harnesses {
		if harness != "" {
			fmt.Fprintf(out, "checking --harness %s\n", harness)
		}
		res, err := r.check(harness, out)
		if err != nil || res.state == stateOpen {
			return res, err
		}
	}
	return passed("engine start --check passed"), nil
}

// check runs one harness's check, and is open when it fails or finds the
// session would stop. A host that does not answer is an error.
func (r *runner) check(harness string, out io.Writer) (outcome, error) {
	clean, err := r.d.Check(harness, out)
	var unreachable *remote.UnreachableError
	switch {
	case errors.As(err, &unreachable):
		return outcome{}, err
	case err != nil:
		return open(err.Error(), r.rerun()), nil
	case !clean && harness == codexHarness:
		return open("an unattended Codex session there would stop; read the report above, then rerun",
			"engine start --check --harness codex"+r.t.flag()), nil
	case !clean:
		return open("an unattended session there would stop; answer each prompt in a folder you trust, then rerun",
			r.t.onHost("claude --permission-mode auto --remote-control", true)), nil
	}
	return passed(""), nil
}
