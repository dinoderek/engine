package setup

import (
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

func TestEngineBlockNamesEnginesClones(t *testing.T) {
	if reposDir != session.ReposDir {
		t.Errorf("setup's clones directory %q is not the session's %q", reposDir, session.ReposDir)
	}
	block := engineBlock("/home/dev")
	for _, want := range []string{
		"approval_policy = \"on-request\"\n", "approvals_reviewer = \"auto_review\"\n", "sandbox_mode = \"workspace-write\"\n",
		"[sandbox_workspace_write]\nnetwork_access = true\n", "writable_roots = [\"/home/dev/.local/share/engine/repos\"]\n",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("want %q in:\n%s", want, block)
		}
	}
	if strings.Contains(block, "hook") {
		t.Errorf("hook trust belongs to the launch:\n%s", block)
	}
}

func TestWithEngineBlock(t *testing.T) {
	block := engineBlock("/home/dev")
	codexTables := "[marketplaces.engine]\nsource = \"x\"\n\n[plugins.\"engine@engine\"]\nenabled = true\n"
	cases := []struct{ name, config, want string }{
		{"no file", "", block},
		{"codex's tables only", codexTables, block + "\n" + codexTables},
		{"the block is current", block + "\n" + codexTables, block + "\n" + codexTables},
		{"the block alone", block, block},
		{"an old block is replaced", engineBlock("/Users/old") + "\n" + codexTables, block + "\n" + codexTables},
		{"an older block with other keys", configHeader + "sandbox_mode = \"read-only\"\n\n[sandbox_workspace_write]\nnetwork_access = false\nextra = 1\n\n" + codexTables,
			block + "\n" + codexTables},
		{"settings that are not the block's stay", "[projects.\"/w\"]\ntrust_level = \"trusted\"\n", block + "\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n"},
	}
	for _, c := range cases {
		got := withEngineBlock(c.config, block)
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		if again := withEngineBlock(got, block); again != got {
			t.Errorf("%s: not idempotent:\n%q\n%q", c.name, got, again)
		}
	}
}

func TestInstalledCodexPlugin(t *testing.T) {
	cases := []struct {
		name, json, version string
		found               bool
	}{
		{"installed", codexPluginList("0.7.1"), "0.7.1", true},
		{"not installed", codexPluginList(""), "", false},
		{"disabled", `{"installed":[{"pluginId":"engine@engine","version":"0.7.1","enabled":false}]}`, "", false},
		{"another plugin", `{"installed":[{"pluginId":"other@engine","version":"1.0.0","enabled":true}]}`, "", false},
		{"not JSON", "Error: boom", "", false},
	}
	for _, c := range cases {
		version, found := installedCodexPlugin(c.json)
		if version != c.version || found != c.found {
			t.Errorf("%s: got %q %v, want %q %v", c.name, version, found, c.version, c.found)
		}
	}
}

func TestRemoteState(t *testing.T) {
	cases := []struct {
		name string
		res  result
		want string
	}{
		{"connected", result{out: `{"status":"connected"}`}, "connected"},
		{"disabled", result{out: `{"status":"disabled"}`}, "disabled"},
		{"errored, with the log line before it", result{errOut: "\x1b[31mERROR\x1b[0m ignoring\nError: Remote control is enabled on Dinos-MacBook-Pro.local but the connection is errored.\n", code: 1}, remoteErrored},
		{"no JSON", result{out: "something else"}, "unreadable"},
		{"JSON without a status", result{out: `{"other":1}`}, "unreadable"},
		{"no output", result{code: 1}, "unreadable"},
	}
	for _, c := range cases {
		if got := remoteState(c.res); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestOperatorNotes(t *testing.T) {
	ignored := "  user (/Users/me/.codex/config.toml): `features.rcmp_client` is ignored.\n  user (/Users/me/.codex/config.toml): `features.other` is ignored.\n"
	cases := []struct {
		name string
		res  result
		want string
	}{
		{"connected and clean", result{out: `{"status":"connected"}`}, ""},
		{"connecting", result{out: `{"status":"connecting"}`}, "the operator's remote control is connecting"},
		{"errored with ignored keys", result{errOut: ignored + "Error: Remote control is enabled on x but the connection is errored.", code: 1},
			"the operator's remote control is errored; features.rcmp_client is ignored in /Users/me/.codex/config.toml; features.other is ignored in /Users/me/.codex/config.toml"},
	}
	for _, c := range cases {
		if got := strings.Join(operatorNotes(c.res), "; "); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAtLeast(t *testing.T) {
	cases := []struct {
		v, want string
		ok      bool
	}{
		{"0.7.6", "", true}, {"", "", true}, {"0.7.6", "0.7.6", true}, {"0.7.8", "0.7.6", true}, {"0.7.10", "0.7.9", true},
		{"0.8", "0.7.6", true}, {"0.7", "0.7.0", true}, {"0.7.5", "0.7.6", false}, {"0.6.9", "0.7.0", false}, {"", "0.7.6", false},
		{"0.7.6-rc1", "0.7.6", false}, {"0.7.6", "0.7.x", false}, {"0.7.-1", "0.7.0", false},
	}
	for _, c := range cases {
		if got := atLeast(c.v, c.want); got != c.ok {
			t.Errorf("atLeast(%q, %q) = %v, want %v", c.v, c.want, got, c.ok)
		}
	}
}
