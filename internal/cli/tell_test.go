package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hosts"
)

const tellHosts = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 8
launch = "{bin} --model {model}"
tiers.deep     = { model = "opus", effort = "high" }
tiers.standard = { model = "sonnet", effort = "high" }
tiers.light    = { model = "haiku", effort = "medium" }

[host.mac]
address = "dino@mac.example.ts.net"
harnesses = ["claude"]
ram_gb = 48
capabilities = ["macos"]
bin = { claude = "~/.local/bin/claude" }

[host.devbox]
address = "dino@devbox.example.ts.net"
harnesses = ["claude"]
ram_gb = 32
capabilities = ["linux"]
bin = { claude = "~/.local/bin/claude" }
`

// The paths are split so this file holds no tracker reference.
const tellComments = "repos/o/r/" + "issues/7/comments"

// tellGh answers the claim search for task 7 of o/r, claimed on another host,
// and records the comment it is asked to post.
func tellGh(t *testing.T, posted *string) func([]string, string, ...string) (string, error) {
	t.Helper()
	jsonOf := func(v any) string {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	claim := "Claim: r-7\nRun: r-7-20261024T000000Z\nHost: devbox\nHarness: claude-code 2.1 (sonnet)\nMode: hands-off\nStarted by: engine start\nTmux: engine:r-7\nRemote Control: r-7\nBranch: task/r-7"
	return func(_ []string, name string, args ...string) (string, error) {
		switch {
		case args[1] == "search/issues":
			return jsonOf(map[string]any{"incomplete_results": false, "items": []map[string]any{{
				"number": 7, "title": "task", "state": "open", "repository_url": "https://api.github.com/repos/o/r",
				"labels": []map[string]string{{"name": "engine:running"}},
			}}}), nil
		case args[1] == tellComments && len(args) == 4 && args[2] == "-f":
			*posted = strings.TrimPrefix(args[3], "body=")
			return jsonOf(map[string]string{"html_url": "https://example.test/c/1"}), nil
		case args[1] == tellComments || slices.Contains(args, "--paginate"):
			return jsonOf([]map[string]any{{"body": claim, "html_url": "https://example.test/c/0",
				"created_at": "2026-10-24T00:00:00Z", "user": map[string]string{"login": "dinoderek-agents"}}}), nil
		}
		t.Errorf("unexpected %s %v", name, args)
		return "", errors.New("unexpected")
	}
}

func TestTellText(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"words", []string{"tell", "r-7", "go", "ahead"}, "", "go ahead"},
		{"stdin keeps its lines", []string{"tell", "r-7", "-"}, "first\n\nsecond \"quoted\"\n", "first\n\nsecond \"quoted\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			dir := hosts.ConfigDir(home)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"hosts.toml": tellHosts, "host": "mac\n"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var posted string
			var out, errOut bytes.Buffer
			env := Env{
				In: strings.NewReader(tt.stdin), Out: &out, Err: &errOut, Exec: tellGh(t, &posted), Stream: noStream(t),
				Getenv: func(k string) string { return map[string]string{"GH_TOKEN": "tok"}[k] }, Home: home,
			}
			// The claim is on another host, so the run ends in an error once the comment is posted.
			if code := Run(tt.args, env); code != 1 {
				t.Errorf("exit code %d, want 1; stderr %s", code, errOut.String())
			}
			if posted != tt.want {
				t.Errorf("commented %q, want %q; stderr %s", posted, tt.want, errOut.String())
			}
		})
	}
}
