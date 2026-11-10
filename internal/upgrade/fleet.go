package upgrade

import (
	"fmt"
	"io"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/setup"
	"github.com/dinoderek/engine/internal/version"
)

// install builds the target into the caller's ~/.local/bin/engine, the
// command setup's engine step runs, and checks the result: every setup after
// it runs that binary.
//
// Replacing the binary this process runs from is safe. cmd/go installs a
// built file by renaming it over the path, or by removing the old file and
// creating a new one (cmd/go/internal/work's moveOrCopyFile and CopyFile,
// which calls mayberemovefile first); it never writes into the old file. On
// macOS and Linux the path then names a new inode, and a running process
// keeps the one it was started from.
func (f *fleet) install(out io.Writer) bool {
	if !f.runOn(f.caller, setup.InstallCommand(f.toolchain, f.commit), "go install", out) {
		return false
	}
	check := `"$HOME/` + newBinary + `" version`
	res, err := f.shell(f.caller).Sh(check, "")
	if err != nil || res.Code != 0 {
		f.openf(out, check, "the installed engine does not run: %s", whyOf(res, err))
		return false
	}
	got := version.Parse(res.Out).Commit()
	if len(got) < shortRev || !strings.HasPrefix(f.commit, got) {
		f.openf(out, "engine upgrade", "~/%s is %s after the install, not %s", newBinary, harness.OrNone(short(got)), short(f.commit))
		return false
	}
	return true
}

// whyOf is why a command failed: its error, else what it said.
func whyOf(res harness.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	return res.Why()
}

// pluginActions bring the caller's own plugins to the marketplace's latest,
// for every harness its host lists, through the adapters. A failed command
// stays open and the run goes on: setup then brings the other hosts to the
// version the caller has.
func (f *fleet) pluginActions() []action {
	var acts []action
	for _, a := range listed(f.d.Harnesses, f.caller) {
		cmds, err := a.Setup().PluginUpdate(f.caller, f.d.Home)
		if err != nil {
			why := a.Name() + "'s plugin update: " + err.Error()
			acts = append(acts, action{where: "here", show: why, do: func(out io.Writer) bool {
				f.openf(out, "engine setup", "%s", why)
				return true
			}})
			continue
		}
		for _, cmd := range cmds {
			acts = append(acts, action{where: "here", show: cmd, do: func(out io.Writer) bool {
				f.runOn(f.caller, cmd, cmd, out)
				return true
			}})
		}
	}
	return acts
}

// listed are the adapters of the harnesses h lists, in the registry's order.
func listed(set harness.Adapters, h hosts.Host) []harness.Adapter {
	if set == nil {
		return nil
	}
	var out []harness.Adapter
	for _, name := range set.Names() {
		if a, ok := set.Get(name); ok && h.Runs(name) {
			out = append(out, a)
		}
	}
	return out
}

// setupArgs are the new engine's arguments that set up h.
func (f *fleet) setupArgs(h hosts.Host) []string {
	if f.local(h) {
		return []string{"setup"}
	}
	return []string{"setup", "--host", h.Name}
}

// setupLine is h's setup as the operator would run it.
func (f *fleet) setupLine(h hosts.Host) string {
	return "~/" + newBinary + " " + strings.Join(f.setupArgs(h), " ")
}

// setupAction sets up h with the new engine, as a subprocess: setup brings a
// host to the commit of the binary that runs it, and this process is still
// the old one. A setup that stays open does not stop the run.
func (f *fleet) setupAction(h hosts.Host) action {
	return action{where: "here", show: f.setupLine(h), do: func(out io.Writer) bool {
		f.setupHost(h, out)
		return true
	}}
}

// setupHost runs h's setup, its report going to out, then reads what h has.
func (f *fleet) setupHost(h hosts.Host, out io.Writer) {
	r := f.results[h.Name]
	if r.unreachable != "" {
		fmt.Fprintf(out, "   skipped: %s could not be reached at the preflight\n", h.Name)
		return
	}
	r.ran = true
	code, err := f.d.Exec(nil, out, out, f.newBinary(), f.setupArgs(h)...)
	switch {
	case err != nil:
		r.open = "setup did not run: " + err.Error()
	case code != 0:
		r.open = "setup ended open; its report is above"
	}
	after, err := f.engineOn(h)
	if err != nil {
		after = "unknown"
	}
	r.after, r.plugin = after, f.pluginOn(h)
}

// pluginOn is the version of engine's plugin the user has on h: the first of
// h's harnesses that keeps one, else none.
func (f *fleet) pluginOn(h hosts.Host) string {
	for _, a := range listed(f.d.Harnesses, h) {
		version, found, err := a.Setup().Plugin(f.shell(h))
		switch {
		case err != nil:
			return "unknown"
		case found:
			return version
		}
	}
	return "none"
}
