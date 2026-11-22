package codex

import (
	"fmt"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
)

// pluginHome is one Codex home that engine's plugin goes into: engine's own,
// or the operator's, which embedded sessions use when they cannot join
// engine's daemon. Codex's own plugin commands are the only thing that
// touches either.
type pluginHome struct {
	name string                   // how the home reads in a report
	cmd  func(args string) string // a codex command in this home
	// home is the home as a shell word, where setup keeps the versions an
	// add removes (retain.go): engine's own only, since setup changes the
	// operator's only through Codex's commands.
	home string
}

func (r *setupRun) engineHomeForPlugin() pluginHome {
	return pluginHome{name: "engine's Codex home", cmd: r.inEngine, home: hosts.ShellWord(r.engineHome())}
}

func (r *setupRun) operatorHomeForPlugin() pluginHome {
	return pluginHome{name: "the operator's Codex home", cmd: r.run}
}

// plugin is the version of engine's plugin in the home, and whether it is
// installed and enabled.
func (r *setupRun) plugin(h pluginHome) (string, bool, error) {
	res, err := r.t.Sh(h.cmd("plugin list --json"), "")
	if err != nil {
		return "", false, err
	}
	version, found := installedCodexPlugin(res.Out)
	return version, found, nil
}

// codexPluginStep brings engine's plugin in engine's Codex home and in the
// operator's to the caller's version. The operator's copy matters for embedded
// sessions, whose hooks report their state.
func (r *setupRun) codexPluginStep() (harness.Outcome, error) {
	if r.missing {
		return r.codexBlocked(), nil
	}
	res, err := r.installPlugin(r.engineHomeForPlugin())
	if err != nil || res.State == harness.StepOpen {
		return res, err
	}
	operator, err := r.installPlugin(r.operatorHomeForPlugin())
	if err != nil || operator.State == harness.StepOpen {
		return operator, err
	}
	switch {
	case operator.State != harness.StepChanged:
		return res, nil
	case res.State == harness.StepChanged:
		return harness.Changed(fmt.Sprintf("%s; the operator's home %s", res.Detail, operator.Detail)), nil
	}
	return harness.Changed("the operator's home " + operator.Detail), nil
}

// installPlugin installs engine's plugin from this repo's marketplace into the
// home, or brings it to the caller's version, keeping the versions the add
// removes in a home that keeps them (retain.go). With no caller's version,
// an installed copy stays. Either way, versions kept by an earlier install
// go once no window needs them.
func (r *setupRun) installPlugin(h pluginHome) (harness.Outcome, error) {
	have, installed, err := r.plugin(h)
	switch {
	case err != nil:
		return harness.Outcome{}, err
	case installed && atLeast(have, r.t.Plugin()):
		return harness.Passed(have), r.prune(h, have)
	case h.home == "":
		out, _, err := r.addPlugin(h, have)
		return out, err
	}
	if res, err := r.t.Sh(keepCommand(h.home), ""); err != nil || res.Code != 0 {
		return r.shOpen(h, "could not keep the plugin's versions", keepCommand(h.home), res, err)
	}
	// The versions go back whether the add worked or not; a failed add is
	// the open item to report first.
	// The prune keeps the version the add confirmed: Codex lists one version
	// with several on disk.
	out, after, err := r.addPlugin(h, have)
	res, rerr := r.t.Sh(restoreCommand(h.home), "")
	switch {
	case err != nil || out.State == harness.StepOpen:
		return out, err
	case rerr != nil || res.Code != 0:
		return r.shOpen(h, "could not put back the plugin's earlier versions", restoreCommand(h.home), res, rerr)
	}
	return out, r.prune(h, after)
}

// addPlugin runs Codex's commands that add the plugin to the home, and
// checks the version they leave, which it returns.
func (r *setupRun) addPlugin(h pluginHome, have string) (harness.Outcome, string, error) {
	// Adding the marketplace again is harmless, so no check for it comes first.
	for _, args := range []string{"plugin marketplace add " + harness.PluginRepo, "plugin marketplace upgrade engine", "plugin add " + harness.PluginID} {
		res, err := r.t.Sh(h.cmd(args), "")
		if err != nil {
			return harness.Outcome{}, "", err
		}
		if res.Code != 0 {
			return harness.Open("codex "+args+" failed in "+h.name+": "+res.Why(), r.t.OnHost(h.cmd(args), false)), "", nil
		}
	}
	after, found, err := r.plugin(h)
	switch {
	case err != nil:
		return harness.Outcome{}, "", err
	case !found:
		return harness.Open(harness.PluginID+" is not enabled in "+h.name+" after the install", r.t.Rerun()), "", nil
	case !atLeast(after, r.t.Plugin()):
		return harness.Open(fmt.Sprintf("the marketplace gives Codex plugin %s in %s, older than this host's %s; merge the plugin change first", after, h.name, r.t.Plugin()), r.t.Rerun()), "", nil
	}
	return harness.Changed(fmt.Sprintf("%s, was %s", after, harness.OrNone(have))), after, nil
}

// shOpen is a shell command in the home that failed: an error, or an open
// item to rerun.
func (r *setupRun) shOpen(h pluginHome, what, cmd string, res harness.Result, err error) (harness.Outcome, error) {
	if err != nil {
		return harness.Outcome{}, err
	}
	return harness.Open(what+" in "+h.name+": "+res.Why(), r.t.OnHost(cmd, false)), nil
}
