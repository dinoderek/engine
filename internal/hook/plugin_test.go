package hook

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/harness/claude"
)

// hookGroups is a hooks table: event name to its matcher groups.
type hookGroups map[string][]struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"hooks"`
}

// commands returns, per event and matcher, the engine commands registered.
func (h hookGroups) commands(event string) map[string][]string {
	out := map[string][]string{}
	for _, g := range h[event] {
		for _, c := range g.Hooks {
			out[g.Matcher] = append(out[g.Matcher], c.Command)
		}
	}
	return out
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// guards are cmds without the state hook, which each harness registers on
// its own events, and without the harness each names.
func guards(cmds []string) []string {
	var out []string
	for _, c := range cmds {
		if !strings.HasPrefix(c, "engine hook state ") {
			out = append(out, strings.Fields(c)[2])
		}
	}
	return out
}

// kindOf is what the harness's hooks make of an event called name.
func kindOf(t *testing.T, hooks harness.Hooks, name string) harness.EventKind {
	return parse(t, hooks, `{"hook_event_name":"`+name+`"}`).Kind
}

// Every command either plugin registers runs an entry of the harness it
// names: a core entry or one of the adapter's own. So does the status line
// the Claude launch sets.
func TestPluginCommandsNameTheirHarnessAndAnEntryItHas(t *testing.T) {
	var claudePlugin struct{ Hooks hookGroups }
	readJSON(t, "../../plugins/engine/hooks/hooks.json", &claudePlugin)
	var codexPlugin struct{ Hooks struct{ Hooks hookGroups } }
	readJSON(t, "../../plugins/engine/.codex-plugin/plugin.json", &codexPlugin)
	launch := claude.New(harness.Env{Getenv: func(string) string { return "" }}).Session("r").Args(harness.Launch{Kickoff: "k"})
	_, settings, _ := strings.Cut(launch, `"command":"`)
	statusLine, _, _ := strings.Cut(settings, `"`)
	for name, tc := range map[string]struct {
		groups hookGroups
		hooks  harness.Hooks
		extra  []string
	}{"claude": {claudePlugin.Hooks, claudeHooks, []string{statusLine}}, "codex": {codexPlugin.Hooks.Hooks, codexHooks, nil}} {
		cmds := tc.extra
		for event := range tc.groups {
			for _, c := range tc.groups.commands(event) {
				cmds = append(cmds, c...)
			}
		}
		for _, c := range cmds {
			f := strings.Fields(c)
			if len(f) != 5 || f[0] != "engine" || f[1] != "hook" || f[3] != "--harness" || f[4] != name {
				t.Errorf("%s: %q is not engine hook <entry> --harness %s", name, c, name)
				continue
			}
			if _, own := tc.hooks.Entries()[f[2]]; !own && !slices.Contains(harness.CoreEntries, f[2]) {
				t.Errorf("%s: %q runs an entry %s has not got", name, c, name)
			}
		}
	}
}

// The Codex plugin registers the same permission and merge-guard commands on
// the same events as the Claude plugin, so the two harnesses are guarded alike.
func TestCodexPluginGuardsMatchClaude(t *testing.T) {
	var claude struct{ Hooks hookGroups }
	readJSON(t, "../../plugins/engine/hooks/hooks.json", &claude)
	var codex struct {
		Skills string
		Hooks  struct{ Hooks hookGroups }
	}
	readJSON(t, "../../plugins/engine/.codex-plugin/plugin.json", &codex)

	for _, event := range []string{"PreToolUse", "PermissionRequest"} {
		want := claude.Hooks.commands(event)
		got := codex.Hooks.Hooks.commands(event)
		for matcher, cmds := range want {
			if g, w := guards(got[matcher]), guards(cmds); !reflect.DeepEqual(g, w) {
				t.Errorf("%s[%q]: Codex runs %q, Claude runs %q", event, matcher, g, w)
			}
		}
	}
	if codex.Skills != "./skills/" {
		t.Errorf("skills = %q, want the shared ./skills/ tree", codex.Skills)
	}
}

// Every event the state hook reads is registered for it, and only it runs
// there beyond the guards.
func TestCodexPluginRegistersStateHook(t *testing.T) {
	var codex struct{ Hooks struct{ Hooks hookGroups } }
	readJSON(t, "../../plugins/engine/.codex-plugin/plugin.json", &codex)

	var events []string
	for event := range codex.Hooks.Hooks {
		for _, cmds := range codex.Hooks.Hooks.commands(event) {
			for _, c := range cmds {
				if c == "engine hook state --harness codex" {
					events = append(events, event)
				}
			}
		}
	}
	sort.Strings(events)
	want := []string{"Interrupt", "PermissionRequest", "PostToolUse", "SessionEnd", "SessionStart", "Stop", "UserPromptSubmit"}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("engine hook state is on %q, want %q", events, want)
	}
	for _, event := range want {
		if stateFor(kindOf(t, codexHooks, event), false) == "" {
			t.Errorf("%s is registered but the state hook ignores it", event)
		}
	}
}

// The Codex marketplace lists the engine plugin from the directory that holds
// its Codex manifest.
func TestCodexMarketplaceListsThePlugin(t *testing.T) {
	var market struct {
		Name    string
		Plugins []struct {
			Name   string
			Source struct{ Source, Path string }
		}
	}
	readJSON(t, "../../.agents/plugins/marketplace.json", &market)
	if market.Name != "engine" || len(market.Plugins) != 1 {
		t.Fatalf("marketplace = %+v, want the one marketplace engine with one plugin", market)
	}
	p := market.Plugins[0]
	if p.Name != "engine" || p.Source.Source != "local" || p.Source.Path != "./plugins/engine" {
		t.Errorf("plugin = %+v, want engine from the local ./plugins/engine", p)
	}
	var manifest struct{ Name string }
	readJSON(t, "../../"+p.Source.Path+"/.codex-plugin/plugin.json", &manifest)
	if manifest.Name != p.Name {
		t.Errorf("manifest names %q, marketplace lists %q", manifest.Name, p.Name)
	}
}

func TestClaudePluginRegistersLimitHook(t *testing.T) {
	var plugin struct{ Hooks hookGroups }
	readJSON(t, "../../plugins/engine/hooks/hooks.json", &plugin)
	for event, matcher := range map[string]string{
		"StopFailure":      "rate_limit|billing_error",
		"Notification":     "quota_auto_resume_fired|quota_auto_resume_stale|quota_auto_resume_disabled",
		"UserPromptSubmit": "",
	} {
		want := map[string][]string{matcher: {"engine hook limit --harness claude"}}
		got := plugin.Hooks.commands(event)
		for m, cmds := range got {
			got[m] = slices.DeleteFunc(cmds, func(c string) bool { return strings.HasPrefix(c, "engine hook state ") })
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", event, got, want)
		}
	}
}

// The Claude plugin runs the state hook on exactly the events that tell
// whether a turn has ended: Stop ends it, and a prompt or a tool call is in
// one. Claude Code's session list says the rest.
func TestClaudePluginRegistersStateHook(t *testing.T) {
	var plugin struct{ Hooks hookGroups }
	readJSON(t, "../../plugins/engine/hooks/hooks.json", &plugin)
	var events []string
	for event := range plugin.Hooks {
		for matcher, cmds := range plugin.Hooks.commands(event) {
			if slices.Contains(cmds, "engine hook state --harness claude") {
				events = append(events, event)
				if matcher != "" {
					t.Errorf("%s: the state hook runs only for %q, want every tool", event, matcher)
				}
			}
		}
	}
	sort.Strings(events)
	want := []string{"PostToolUse", "PreToolUse", "Stop", "UserPromptSubmit"}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("engine hook state is on %q, want %q", events, want)
	}
	for _, event := range want {
		if stateFor(kindOf(t, claudeHooks, event), false) == "" {
			t.Errorf("%s is registered but the state hook ignores it", event)
		}
	}
}
