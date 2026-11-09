package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/harnesstest"
	"github.com/dinoderek/engine/internal/hosts"
)

const callerPlugin = "0.7.1"

// box is a host's Claude as setup's steps reach it. mutations lists the
// commands that changed something.
type box struct {
	marketplace bool
	plugin      string // version of the user's plugin; "" is not installed
	pluginFail  bool
	pluginStuck string // what the plugin update leaves, when set
	auth        string // claude auth status output
	mutations   []string
}

// staleBox has an old plugin from engine's marketplace, and is signed in.
func staleBox() *box {
	return &box{marketplace: true, plugin: "0.2.0", auth: `{"loggedIn":true,"authMethod":"claude.ai"}`}
}

func pluginList(version string) string {
	if version == "" {
		return "[]"
	}
	data, _ := json.Marshal([]map[string]string{
		{"id": "engine@engine", "version": "0.3.2", "scope": "project"},
		{"id": "engine@engine", "version": version, "scope": "user"},
	})
	return string(data)
}

func (b *box) sh(cmd, _ string) (harness.Result, error) {
	ok := func(out string) (harness.Result, error) { return harness.Result{Out: out}, nil }
	switch {
	case cmd == "claude plugin list --json":
		return ok(pluginList(b.plugin))
	case cmd == "claude plugin marketplace list --json":
		if b.marketplace {
			return ok(`[{"name":"engine"}]`)
		}
		return ok(`[{"name":"other"}]`)
	case strings.HasPrefix(cmd, "claude plugin "):
		b.mutations = append(b.mutations, cmd)
		if b.pluginFail {
			return harness.Result{ErrOut: "plugin: marketplace unreachable", Code: 1}, nil
		}
		if strings.Contains(cmd, "marketplace") {
			b.marketplace = true
		} else {
			b.plugin = callerPlugin
			if b.pluginStuck != "" {
				b.plugin = b.pluginStuck
			}
		}
		return ok("")
	case cmd == "claude auth status":
		return ok(b.auth)
	}
	return harness.Result{ErrOut: fmt.Sprintf("box: unexpected command %q", cmd), Code: 99}, nil
}

// remote is the devbox, numbered as setup numbers another host: the core's
// tools, engine, config and no suspend steps come first.
func remote(b *box) *harnesstest.Target {
	return &harnesstest.Target{Shell: b.sh, Address: "dev@box", Version: callerPlugin,
		Entry:  hosts.Host{Name: "box", Harnesses: []string{Name}},
		Before: []string{"tools", "engine", "config", "no suspend"}}
}

// local is this host checking itself: it has no config step.
func local(b *box) *harnesstest.Target {
	return &harnesstest.Target{Shell: b.sh, Entry: hosts.Host{Name: "mac", Harnesses: []string{Name}},
		Before: []string{"tools", "engine", "no suspend"}}
}

func run(t *testing.T, tgt *harnesstest.Target) string {
	t.Helper()
	out, err := harnesstest.RunSteps(New(harness.Env{}), tgt)
	if err != nil {
		t.Fatalf("setup failed: %v\n%s", err, out)
	}
	return out
}

func TestSetupBringsAStalePluginToTheCallers(t *testing.T) {
	b := staleBox()
	out := run(t, remote(b))
	for _, want := range []string{"5. plugin: changed (0.7.1, was 0.2.0)", "6. sign-in: ok (claude.ai)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	b.mutations = nil
	if out := run(t, remote(b)); strings.Contains(out, "changed") || len(b.mutations) != 0 {
		t.Errorf("the second run changed %q:\n%s", b.mutations, out)
	}
}

func TestEachClaudeStepFailsOpenWithItsCommand(t *testing.T) {
	cases := []struct {
		name, step, detail, command string
		break_                      func(b *box)
	}{
		{"plugin update fails", "5. plugin: OPEN", "claude plugin marketplace update engine failed: plugin: marketplace unreachable",
			"ssh dev@box 'claude plugin marketplace update engine'", func(b *box) { b.pluginFail = true }},
		{"marketplace is older than the caller", "5. plugin: OPEN", "the plugin is 0.6.0 there and 0.7.1 here",
			"claude plugin update engine@engine --scope user", func(b *box) { b.pluginStuck = "0.6.0" }},
		{"signed in with an API key", "6. sign-in: OPEN", "not signed in with claude.ai", "ssh -t dev@box 'claude /login'",
			func(b *box) { b.auth = `{"loggedIn":true,"authMethod":"api_key"}` }},
		{"signed out", "6. sign-in: OPEN", "not signed in with claude.ai", "ssh -t dev@box 'claude /login'",
			func(b *box) { b.auth = "Not logged in" }},
	}
	for _, c := range cases {
		b := staleBox()
		c.break_(b)
		out := run(t, remote(b))
		if !strings.Contains(out, c.step) || !strings.Contains(out, c.detail) || !strings.Contains(out, c.command) {
			t.Errorf("%s: want %q, %q and %q in:\n%s", c.name, c.step, c.detail, c.command, out)
		}
	}
}

func TestMissingClaudeBlocksItsSteps(t *testing.T) {
	b := staleBox()
	tgt := remote(b)
	tgt.Absent = []string{"go", Name}
	out := run(t, tgt)
	for _, want := range []string{"5. plugin: OPEN (claude is missing (step 1))", "6. sign-in: OPEN (claude is missing (step 1))", "engine setup --host box"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if len(b.mutations) != 0 {
		t.Errorf("ran %q without claude", b.mutations)
	}
}

func TestPluginWithoutMarketplaceIsAddedThenInstalled(t *testing.T) {
	b := staleBox()
	b.plugin, b.marketplace = "", false
	run(t, remote(b))
	want := []string{"claude plugin marketplace add dinoderek/engine", "claude plugin install engine@engine --scope user"}
	if fmt.Sprint(b.mutations) != fmt.Sprint(want) {
		t.Errorf("changes %q, want %q", b.mutations, want)
	}
}

func TestPluginWithMarketplaceIsRefreshedThenUpdated(t *testing.T) {
	b := staleBox()
	run(t, remote(b))
	want := []string{"claude plugin marketplace update engine", "claude plugin update engine@engine --scope user"}
	if fmt.Sprint(b.mutations) != fmt.Sprint(want) {
		t.Errorf("changes %q, want %q", b.mutations, want)
	}
}

func TestLocalModeOnlyChecksClaude(t *testing.T) {
	b := staleBox()
	b.plugin = callerPlugin
	out := run(t, local(b))
	for _, want := range []string{"4. plugin: ok (0.7.1)", "5. sign-in: ok (claude.ai)"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if len(b.mutations) != 0 {
		t.Errorf("a check changed %q", b.mutations)
	}
	b.plugin, b.auth = "", "Not logged in"
	out = run(t, local(b))
	for _, want := range []string{"4. plugin: OPEN", "claude plugin marketplace add dinoderek/engine && claude plugin install engine@engine --scope user",
		"5. sign-in: OPEN", ": claude /login"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if len(b.mutations) != 0 {
		t.Errorf("a check changed %q", b.mutations)
	}
}

func TestClaudeCheckFailedAndTools(t *testing.T) {
	s := New(harness.Env{}).Setup()
	detail, command := s.CheckFailed(remote(staleBox()))
	if !strings.Contains(detail, "an unattended session there would stop") || command != "ssh -t dev@box 'claude --permission-mode auto --remote-control'" {
		t.Errorf("remote: %q %q", detail, command)
	}
	if _, command := s.CheckFailed(local(staleBox())); command != "claude --permission-mode auto --remote-control" {
		t.Errorf("local: %q", command)
	}
	if tools := s.Tools(); fmt.Sprint(tools) != "[claude]" {
		t.Errorf("tools %q", tools)
	}
}

// The caller's plugin is the user's, never a project copy.
func TestPluginReadsTheUsersVersion(t *testing.T) {
	b := staleBox()
	v, found, err := New(harness.Env{}).Setup().Plugin(local(b))
	if v != "0.2.0" || !found || err != nil {
		t.Errorf("%q %v %v", v, found, err)
	}
	if _, found := userPlugin(`[{"id":"engine@engine","version":"0.3.2","scope":"project"}]`); found {
		t.Error("found a plugin with only a project copy")
	}
	b.plugin = ""
	if _, found, _ := New(harness.Env{}).Setup().Plugin(local(b)); found {
		t.Error("found a plugin with none installed")
	}
}
