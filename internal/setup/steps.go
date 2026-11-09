package setup

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/version"
)

// coreTools are what every host needs on its non-interactive PATH; each
// harness it lists may add its own.
var coreTools = []string{"go", "tmux", "gh", "git"}

// engineModule is the package `go install` builds.
const engineModule = "github.com/dinoderek/engine/cmd/engine"

// rerun is the command that repeats this setup.
func (r *runner) rerun() string { return "engine setup" + r.t.flag() }

// blocked is a step that needs a tool the tools step did not find.
func (r *runner) blocked(tool string) harness.Outcome {
	return harness.Open(tool+" is missing ("+stepTarget{r}.Ref("tools")+")", r.rerun())
}

// toolList is the tools the host needs: the core's, then its harnesses'.
func (r *runner) toolList() string {
	list := append([]string(nil), coreTools...)
	for _, a := range r.adapters {
		list = append(list, a.Setup().Tools()...)
	}
	return strings.Join(list, " ")
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
func (r *runner) tools() (harness.Outcome, error) {
	res, err := r.t.Sh(`for t in `+r.toolList()+`; do command -v "$t" >/dev/null 2>&1 || echo "$t"; done`, "")
	if err != nil {
		return harness.Outcome{}, err
	}
	missing := res.Lines()
	for _, m := range missing {
		r.missing[m] = true
	}
	if len(missing) == 0 {
		return harness.Passed(""), nil
	}
	return harness.Open("missing: "+strings.Join(missing, ", ")+"; install any the PATH line does not reveal", r.pathLine()), nil
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
	res, err := r.t.Sh("engine version", "")
	if err != nil || res.Code != 0 {
		return engineBuild{}, err
	}
	info := version.Parse(res.Out)
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

// installEngine puts the caller's commit at ~/.local/bin/engine, built with
// the Go go.mod names at that commit, and checks that it is the engine the
// PATH finds. An engine on the right commit built with another Go is replaced.
func (r *runner) installEngine() (harness.Outcome, error) {
	if r.want.toolchainErr != nil {
		return harness.Open(r.want.toolchainErr.Error()+"; setup cannot tell which Go the engine needs", r.rerun()), nil
	}
	want := engineBuild{rev: r.want.commit[:shortRev], goVersion: r.want.toolchain}
	have, err := r.hostEngine()
	if err != nil || have == want {
		return harness.Passed(want.String()), err
	}
	if r.missing["go"] {
		return r.blocked("go"), nil
	}
	install := fmt.Sprintf(`GOBIN="$HOME/.local/bin" GOTOOLCHAIN=%s go install %s@%s`, want.goVersion, engineModule, r.want.commit)
	res, err := r.t.Sh(install, "")
	if err != nil {
		return harness.Outcome{}, err
	}
	if res.Code != 0 {
		return harness.Open("go install failed: "+res.Why(), r.t.onHost(install, false)), nil
	}
	after, err := r.hostEngine()
	if err != nil {
		return harness.Outcome{}, err
	}
	if after != want {
		return harness.Open("the engine on the PATH is "+after.String()+", not the one installed in ~/.local/bin ("+want.String()+")", r.pathLine()), nil
	}
	return harness.Changed(fmt.Sprintf("%s, was %s", want, have)), nil
}

// enginePath checks that this host finds engine on its PATH.
func (r *runner) enginePath() (harness.Outcome, error) {
	res, err := r.t.Sh("command -v engine", "")
	if err != nil {
		return harness.Outcome{}, err
	}
	if res.Code != 0 {
		return harness.Open("engine is not on the PATH", r.pathLine()), nil
	}
	return harness.Passed(strings.TrimSpace(res.Out)), nil
}

// sleepTargets are what a Linux host masks so it never suspends.
const sleepTargets = "sleep.target suspend.target hibernate.target hybrid-sleep.target"

// noSuspend checks, on Linux, that the sleep targets are masked.
func (r *runner) noSuspend() (harness.Outcome, error) {
	res, err := r.t.Sh("uname -s; systemctl is-enabled "+sleepTargets+" 2>/dev/null", "")
	if err != nil {
		return harness.Outcome{}, err
	}
	lines := res.Lines()
	if len(lines) == 0 || lines[0] != "Linux" {
		return harness.Passed("not Linux"), nil
	}
	masked := 0
	for _, l := range lines[1:] {
		if l == "masked" {
			masked++
		}
	}
	if masked == len(strings.Fields(sleepTargets)) {
		return harness.Passed("masked"), nil
	}
	return harness.Open("the host can suspend", r.t.onHost("sudo systemctl mask "+sleepTargets, true)), nil
}

// preflight runs `engine start --check` on the target for every harness it
// lists, the default first, once nothing else is open: a check cannot pass
// past a missing tool or a signed-out harness. The first that fails ends it.
func (r *runner) preflight(out io.Writer) (harness.Outcome, error) {
	if r.opened > 0 {
		return harness.Open("not run while items above are open", r.rerun()), nil
	}
	for i, name := range r.checked() {
		if i > 0 {
			fmt.Fprintf(out, "checking --harness %s\n", name)
		}
		res, err := r.check(name, out)
		if err != nil || res.State == harness.StepOpen {
			return res, err
		}
	}
	return harness.Passed("engine start --check passed"), nil
}

// checked are the harnesses the preflight checks: the host's, the default
// first; the default alone when the host lists none.
func (r *runner) checked() []string {
	list := []string{}
	if r.defaultHarness != "" && (len(r.host.Harnesses) == 0 || r.host.Runs(r.defaultHarness)) {
		list = append(list, r.defaultHarness)
	}
	for _, name := range r.host.Harnesses {
		if !slices.Contains(list, name) {
			list = append(list, name)
		}
	}
	if len(list) == 0 {
		list = append(list, "")
	}
	return list
}

// check runs one harness's check, and is open when it fails or finds the
// session would stop, with the harness's own fix. A host that does not
// answer is an error.
func (r *runner) check(name string, out io.Writer) (harness.Outcome, error) {
	clean, err := r.d.Check(name, out)
	var unreachable *remote.UnreachableError
	switch {
	case errors.As(err, &unreachable):
		return harness.Outcome{}, err
	case err != nil:
		return harness.Open(err.Error(), r.rerun()), nil
	case !clean:
		a, ok := lookup(r.d.Harnesses, name)
		if !ok {
			return harness.Open("an unattended "+name+" session there would stop; read the report above, then rerun", r.rerun()), nil
		}
		detail, command := a.Setup().CheckFailed(stepTarget{r})
		return harness.Open(detail, command), nil
	}
	return harness.Passed(""), nil
}
