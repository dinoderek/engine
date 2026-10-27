package hook

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
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
			var guards []string
			for _, c := range got[matcher] {
				if c != "engine hook state" {
					guards = append(guards, c)
				}
			}
			if !reflect.DeepEqual(guards, cmds) {
				t.Errorf("%s[%q]: Codex runs %q, Claude runs %q", event, matcher, guards, cmds)
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
				if c == "engine hook state" {
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
		if stateFor(event, false) == "" {
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
		want := map[string][]string{matcher: {"engine hook limit"}}
		if got := plugin.Hooks.commands(event); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", event, got, want)
		}
	}
}
