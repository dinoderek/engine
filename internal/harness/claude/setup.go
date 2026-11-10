package claude

import (
	"encoding/json"
	"fmt"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
)

// Setup is Claude Code's part of engine setup: engine's plugin installed for
// the user, and a claude.ai sign-in, which Remote Control needs. On another
// host setup installs or updates the plugin; on this one it only checks.
func (Adapter) Setup() harness.Setup { return setup{} }

type setup struct{}

// Tools: claude must be on the host's non-interactive PATH.
func (setup) Tools() []string { return []string{Name} }

// Plugin is the version of the user's engine plugin that sh's host has.
func (setup) Plugin(sh harness.Shell) (string, bool, error) {
	res, err := sh.Sh("claude plugin list --json", "")
	if err != nil {
		return "", false, err
	}
	version, found := userPlugin(res.Out)
	return version, found, nil
}

func (s setup) Steps(t harness.Target) []harness.Step {
	plugin := func() (harness.Outcome, error) { return pluginInstalled(t) }
	if t.Remote() {
		plugin = func() (harness.Outcome, error) { return installPlugin(t) }
	}
	return []harness.Step{
		{Name: "plugin", Run: plugin},
		{Name: "sign-in", Run: func() (harness.Outcome, error) { return signIn(t) }},
	}
}

// CheckFailed: a prompt the check met is answered once, interactively, in a
// folder the operator trusts.
func (setup) CheckFailed(t harness.Target) (string, string) {
	return "an unattended session there would stop; answer each prompt in a folder you trust, then rerun",
		t.OnHost("claude --permission-mode auto --remote-control", true)
}

// PluginUpdate refreshes engine's marketplace, then updates the user's copy
// of the plugin from it.
func (setup) PluginUpdate(hosts.Host, string) ([]string, error) {
	return []string{"claude plugin marketplace update engine", "claude plugin update " + harness.PluginID + " --scope user"}, nil
}

// blocked is a step that needs claude, which the tools step did not find.
func blocked(t harness.Target) harness.Outcome {
	return harness.Open("claude is missing ("+t.Ref("tools")+")", t.Rerun())
}

// pluginEntry is one row of `claude plugin list --json`.
type pluginEntry struct{ ID, Version, Scope string }

// userPlugin is the version of the engine plugin installed for the user, and
// whether there is one. Project copies are not the user's.
func userPlugin(listJSON string) (string, bool) {
	var entries []pluginEntry
	if json.Unmarshal([]byte(listJSON), &entries) != nil {
		return "", false
	}
	for _, e := range entries {
		if e.ID == harness.PluginID && e.Scope == "user" {
			return e.Version, true
		}
	}
	return "", false
}

// hasMarketplace reports whether engine's marketplace is added on the target.
func hasMarketplace(t harness.Target) (bool, error) {
	res, err := t.Sh("claude plugin marketplace list --json", "")
	if err != nil {
		return false, err
	}
	var markets []struct{ Name string }
	if json.Unmarshal([]byte(res.Out), &markets) != nil {
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
	cmds := []string{"claude plugin marketplace add " + harness.PluginRepo}
	if marketplace {
		cmds = []string{"claude plugin marketplace update engine"}
	}
	if installed {
		return append(cmds, "claude plugin update "+harness.PluginID+" --scope user")
	}
	return append(cmds, "claude plugin install "+harness.PluginID+" --scope user")
}

// installPlugin brings the user's engine plugin to the caller's version.
func installPlugin(t harness.Target) (harness.Outcome, error) {
	if t.Missing(Name) {
		return blocked(t), nil
	}
	have, installed, err := setup{}.Plugin(t)
	if err != nil || (installed && have == t.Plugin()) {
		return harness.Passed(t.Plugin()), err
	}
	marketplace, err := hasMarketplace(t)
	if err != nil {
		return harness.Outcome{}, err
	}
	for _, cmd := range pluginCommands(marketplace, installed) {
		res, err := t.Sh(cmd, "")
		if err != nil {
			return harness.Outcome{}, err
		}
		if res.Code != 0 {
			return harness.Open(cmd+" failed: "+res.Why(), t.OnHost(cmd, false)), nil
		}
	}
	after, _, err := setup{}.Plugin(t)
	if err != nil {
		return harness.Outcome{}, err
	}
	if after != t.Plugin() {
		return harness.Open(fmt.Sprintf("the plugin is %s there and %s here; update it here first", harness.OrNone(after), t.Plugin()),
			"claude plugin update "+harness.PluginID+" --scope user"), nil
	}
	return harness.Changed(fmt.Sprintf("%s, was %s", after, harness.OrNone(have))), nil
}

// pluginInstalled checks that the user has the engine plugin on this host.
func pluginInstalled(t harness.Target) (harness.Outcome, error) {
	have, found, err := setup{}.Plugin(t)
	if err != nil {
		return harness.Outcome{}, err
	}
	if !found {
		return harness.Open(harness.PluginID+" is not installed for the user",
			"claude plugin marketplace add "+harness.PluginRepo+" && claude plugin install "+harness.PluginID+" --scope user"), nil
	}
	return harness.Passed(have), nil
}

// signIn checks that Claude is signed in with a claude.ai account, which
// Remote Control needs.
func signIn(t harness.Target) (harness.Outcome, error) {
	if t.Missing(Name) {
		return blocked(t), nil
	}
	res, err := t.Sh("claude auth status", "")
	if err != nil {
		return harness.Outcome{}, err
	}
	var status struct {
		LoggedIn   bool
		AuthMethod string
	}
	_ = json.Unmarshal([]byte(res.Out), &status) // unreadable output reads as signed out
	if status.LoggedIn && status.AuthMethod == "claude.ai" {
		return harness.Passed("claude.ai"), nil
	}
	return harness.Open("not signed in with claude.ai", t.OnHost("claude /login", true)), nil
}
