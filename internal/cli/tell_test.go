package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
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
		case args[1] == "user":
			return "developer", nil
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
				"created_at": "2026-10-24T00:00:00Z", "user": map[string]string{"login": "developer"}}}), nil
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
				Getenv: func(string) string { return "" }, Home: home,
				Local: func(name string, _ ...string) (string, error) {
					return "", &session.ExitError{Command: name, Code: 255, Stderr: "no route to host"}
				},
			}
			// The claim is on a host ssh cannot reach, so the run ends in an error once the comment is posted.
			if code := Run(tt.args, env); code != 1 {
				t.Errorf("exit code %d, want 1; stderr %s", code, errOut.String())
			}
			if !strings.Contains(errOut.String(), "listing the windows on devbox failed") {
				t.Errorf("stderr %s", errOut.String())
			}
			if posted != tt.want {
				t.Errorf("commented %q, want %q; stderr %s", posted, tt.want, errOut.String())
			}
		})
	}
}

// A session with no claim is reached through this host's own status: its run
// record names the task and the window the pointer is typed into.
func TestTellReachesAnUnclaimedSessionOnThisHost(t *testing.T) {
	home := t.TempDir()
	dir := hosts.ConfigDir(home)
	for name, body := range map[string]string{"hosts.toml": tellHosts, "host": "mac\n"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := session.Record{Schema: 1, RunID: "r-7-20261026T225849Z", Repo: "o/r", Task: "o/r#" + "7", Host: "mac", Harness: "claude",
		Name: "r-7", Tmux: "engine:@4", Spawned: time.Date(2026, 10, 26, 22, 58, 49, 0, time.UTC)}
	if err := session.WriteRecord(filepath.Join(home, filepath.FromSlash(session.RunsDir), rec.RunID), rec); err != nil {
		t.Fatal(err)
	}
	var posted string
	var typed []string
	local := func(name string, args ...string) (string, error) {
		switch {
		case name == "tmux" && args[0] == "list-windows":
			return "@4 %9 0 r-7\n", nil
		case name == "tmux" && args[0] == "send-keys":
			typed = append(typed, strings.Join(args, " "))
			return "", nil
		case name == "tmux":
			return "", nil
		}
		return "", errors.New("not here")
	}
	gh := func(_ []string, _ string, args ...string) (string, error) {
		switch {
		case args[1] == "user":
			return "developer", nil
		case args[1] == "search/issues":
			return `{"incomplete_results":false,"items":[]}`, nil
		case args[1] == "repos/o/r/"+"issues/7":
			return `{"number":7,"title":"task","state":"open","labels":[],"body":""}`, nil
		case strings.HasSuffix(args[1], "/blocked_by"):
			return "[]", nil
		case args[1] == tellComments && len(args) == 4:
			posted = strings.TrimPrefix(args[3], "body=")
			return `{"html_url":"https://example.test/c/1"}`, nil
		}
		t.Errorf("unexpected gh %v", args)
		return "", errors.New("unexpected")
	}
	var out, errOut bytes.Buffer
	env := Env{
		In: strings.NewReader(""), Out: &out, Err: &errOut, Exec: gh, Stream: noStream(t),
		Getenv: func(string) string { return "" }, Home: home, Local: local,
		Tmux: &session.Tmux{Run: local, Bin: "tmux", Sleep: func(time.Duration) {}},
		SSH: func(_ io.Reader, _, errOut io.Writer, _ string, _ ...string) (int, error) {
			fmt.Fprintln(errOut, "ssh: connect to host: no route")
			return 255, nil
		},
	}
	if code := Run([]string{"tell", "r-7", "carry", "on"}, env); code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut.String())
	}
	if posted != "carry on" || !strings.Contains(out.String(), "typed a pointer into engine:@4 on mac") || len(typed) != 2 {
		t.Errorf("posted %q, out %s, typed %q", posted, out.String(), typed)
	}
}
