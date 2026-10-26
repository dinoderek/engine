package tell

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

const hostsTOML = `default_harness = "claude"
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

func home(t *testing.T, self string) string {
	t.Helper()
	h := t.TempDir()
	dir := hosts.ConfigDir(h)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"hosts.toml": hostsTOML, "host": self + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// The paths are split so this file holds no tracker reference.
const (
	comments   = "repos/o/r/" + "issues/7/comments"
	commentURL = "https://github.com/o/r/" + "issues/7#issuecomment-1"
)

// gh answers the claim search for task 7 of o/r, claimed on host, and records the
// comment it is asked to post. commentErr fails the post.
type gh struct {
	t          *testing.T
	host       string
	harness    string
	commentErr error
	posted     string
}

func (g *gh) run(env []string, name string, args ...string) (string, error) {
	if name != "gh" || !slices.Contains(env, "GH_TOKEN=tok") || len(args) < 2 || args[0] != "api" {
		g.t.Errorf("unexpected command %s %v", name, args)
		return "", errors.New("unexpected")
	}
	claim := fmt.Sprintf("Claim: r-7\nRun: r-7-20261024T000000Z\nHost: %s\nHarness: "+cmp.Or(g.harness, "claude-code 2.1 (sonnet)")+"\nMode: hands-off\nStarted by: engine start\nTmux: engine:r-7\nRemote Control: r-7\nBranch: task/r-7", g.host)
	switch {
	case args[1] == "search/issues":
		return jsonOf(g.t, map[string]any{"incomplete_results": false, "items": []map[string]any{{
			"number": 7, "title": "task", "state": "open", "repository_url": "https://api.github.com/repos/o/r",
			"labels": []map[string]string{{"name": "engine:running"}},
		}}}), nil
	case args[1] == comments && len(args) == 2 || slices.Contains(args, "--paginate"):
		return jsonOf(g.t, []map[string]any{{"body": claim, "html_url": commentURL,
			"created_at": time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC).Format(time.RFC3339), "user": map[string]string{"login": "dinoderek-agents"}}}), nil
	case args[1] == comments && len(args) == 4 && args[2] == "-f":
		if g.commentErr != nil {
			return "HTTP 403", g.commentErr
		}
		g.posted = strings.TrimPrefix(args[3], "body=")
		return jsonOf(g.t, map[string]string{"html_url": commentURL}), nil
	}
	g.t.Errorf("unexpected gh api %v", args)
	return "", errors.New("unexpected gh api")
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// tmux answers has-session and list-windows, and records send-keys.
type tmux struct {
	windows string
	typeErr error
	keys    [][]string
}

func (f *tmux) run(_ string, args ...string) (string, error) {
	switch args[0] {
	case "has-session", "display-message":
		return "", nil
	case "list-windows":
		return f.windows, nil
	case "send-keys":
		f.keys = append(f.keys, args)
		return "", f.typeErr
	}
	return "", fmt.Errorf("unexpected tmux %v", args)
}

// ssh answers a tmux command run over ssh by handing the unquoted command to
// the tmux fake, or fails the way ssh does when it cannot connect.
type ssh struct {
	tm    *tmux
	err   error
	calls [][]string
}

func (f *ssh) run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if name != "ssh" || len(args) < 2 {
		return "", fmt.Errorf("unexpected %s %v", name, args)
	}
	if f.err != nil {
		return "", f.err
	}
	// ShellJoin quotes every word as '...', with ' written '\''.
	words := strings.Split(strings.Trim(args[len(args)-1], "'"), "' '")
	for i, w := range words {
		words[i] = strings.ReplaceAll(w, `'\''`, "'")
	}
	return f.tm.run(words[0], words[1:]...)
}

func rig(t *testing.T, self, claimHost string, tm *tmux) (Deps, *gh, *[]time.Duration) {
	g := &gh{t: t, host: claimHost}
	var slept []time.Duration
	d := Deps{
		Gh:     g.run,
		Run:    (&ssh{tm: tm}).run,
		Tmux:   &session.Tmux{Run: tm.run, Bin: "tmux", Sleep: func(d time.Duration) { slept = append(slept, d) }},
		Getenv: func(k string) string { return map[string]string{"GH_TOKEN": "tok"}[k] },
		Home:   home(t, self),
	}
	return d, g, &slept
}

func TestTellCommentsAndTypes(t *testing.T) {
	tm := &tmux{windows: "@1 %1 0 other\n@4 %9 0 r-7\n"}
	d, g, slept := rig(t, "mac", "mac", tm)
	told, err := Tell("r-7", "Use the second option.", d)
	if err != nil {
		t.Fatal(err)
	}
	if g.posted != "Use the second option." || told.URL != commentURL || told.Window != "engine:@4" || told.Task != (tracker.Ref{Repo: "o/r", Number: 7}) {
		t.Errorf("told %+v, posted %q", told, g.posted)
	}
	want := [][]string{{"send-keys", "-t", "=engine:@4", "-l", Pointer(commentURL)}, {"send-keys", "-t", "=engine:@4", "Enter"}}
	if fmt.Sprint(tm.keys) != fmt.Sprint(want) || len(*slept) != 1 || (*slept)[0] <= 0 {
		t.Errorf("keys %q, slept %v", tm.keys, *slept)
	}
}

// A Codex window takes the same keys as a Claude one, on this host and over
// ssh: the TUI submits the pointer typed as text and then an Enter pressed
// after a pause.
func TestTellTypesIntoACodexWindow(t *testing.T) {
	for _, claimHost := range []string{"mac", "devbox"} {
		t.Run(claimHost, func(t *testing.T) {
			tm := &tmux{windows: "@1 %1 0 other\n@4 %9 0 r-7\n"}
			d, g, slept := rig(t, "mac", claimHost, tm)
			g.harness = "codex-cli 0.163.0 (gpt-6.1-sol)"
			conn := &ssh{tm: tm}
			d.Run = conn.run
			told, err := Tell("r-7", "Use the second option.", d)
			if err != nil {
				t.Fatal(err)
			}
			if remote := claimHost == "devbox"; told.Host != claimHost || (len(conn.calls) > 0) != remote {
				t.Errorf("host %q, ssh calls %q", told.Host, conn.calls)
			}
			want := [][]string{{"send-keys", "-t", "=engine:@4", "-l", Pointer(commentURL)}, {"send-keys", "-t", "=engine:@4", "Enter"}}
			if told.Window != "engine:@4" || fmt.Sprint(tm.keys) != fmt.Sprint(want) || len(*slept) != 1 || (*slept)[0] <= 0 {
				t.Errorf("told %+v, keys %q, slept %v", told, tm.keys, *slept)
			}
		})
	}
}

func TestTellFailures(t *testing.T) {
	tests := []struct {
		name, task, self, claimHost, windows string
		commentErr, typeErr                  error
		want                                 string
		commented                            bool
	}{
		{"no such task", "r-8", "mac", "mac", "", nil, nil, "no claimed task named r-8", false},
		{"host not in hosts.toml", "r-7", "mac", "laptop", "", nil, nil, `host "laptop" is not in hosts.toml`, true},
		{"no window on the other host", "r-7", "mac", "devbox", "@1 %1 0 other\n", nil, nil, "no live tmux window named r-7 in the session engine on devbox", true},
		{"typing fails on the other host", "r-7", "mac", "devbox", "@4 %9 0 r-7\n", nil, errors.New("tmux died"), "typing into the window on devbox failed", true},
		{"no window", "r-7", "mac", "mac", "@1 %1 0 other\n", nil, nil, "no live tmux window named r-7", true},
		{"dead window", "r-7", "mac", "mac", "@4 %9 1 r-7\n", nil, nil, "no live tmux window named r-7", true},
		{"comment fails", "r-7", "mac", "mac", "@4 %9 0 r-7\n", errors.New("exit 1"), nil, "comment on o/r", false},
		{"claim names no host", "r-7", "mac", "", "@4 %9 0 r-7\n", nil, nil, "names no host", false},
		{"typing fails", "r-7", "mac", "mac", "@4 %9 0 r-7\n", nil, errors.New("tmux died"), "typing into the window on mac failed", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := &tmux{windows: tt.windows, typeErr: tt.typeErr}
			d, g, _ := rig(t, tt.self, tt.claimHost, tm)
			g.commentErr = tt.commentErr
			_, err := Tell(tt.task, "hi", d)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v, want %q", err, tt.want)
			}
			if (g.posted != "") != tt.commented {
				t.Errorf("commented %q, want %v", g.posted, tt.commented)
			}
		})
	}
}

// A claim on another host gets the pointer typed over ssh, to the host's
// address, with the same keys as a local window.
func TestTellTypesOverSSH(t *testing.T) {
	tm := &tmux{windows: "@1 %1 0 other\n@4 %9 0 r-7\n"}
	d, g, slept := rig(t, "mac", "devbox", tm)
	conn := &ssh{tm: tm}
	d.Run = conn.run
	told, err := Tell("r-7", "Use the second option.", d)
	if err != nil {
		t.Fatal(err)
	}
	if g.posted != "Use the second option." || told.Window != "engine:@4" || told.Host != "devbox" {
		t.Errorf("told %+v, posted %q", told, g.posted)
	}
	want := [][]string{{"send-keys", "-t", "=engine:@4", "-l", Pointer(commentURL)}, {"send-keys", "-t", "=engine:@4", "Enter"}}
	if fmt.Sprint(tm.keys) != fmt.Sprint(want) || len(*slept) != 1 {
		t.Errorf("keys %q, slept %v", tm.keys, *slept)
	}
	for _, c := range conn.calls {
		if c[0] != "ssh" || !slices.Contains(c, "dino@devbox.example.ts.net") || !slices.Contains(c, "BatchMode=yes") {
			t.Errorf("call %q is not a batch ssh to the devbox", c)
		}
	}
}

// A host that cannot be reached keeps the comment and says so: it is not a
// missing window.
func TestTellUnreachableHost(t *testing.T) {
	tm := &tmux{}
	d, g, _ := rig(t, "mac", "devbox", tm)
	d.Run = (&ssh{tm: tm, err: &session.ExitError{Command: "ssh", Code: 255, Stderr: "no route to host\n"}}).run
	told, err := Tell("r-7", "hi", d)
	if err == nil || !strings.Contains(err.Error(), "listing the windows on devbox failed") || !strings.Contains(err.Error(), "no route to host") {
		t.Errorf("error %v", err)
	}
	if strings.Contains(err.Error(), "no live tmux window") {
		t.Errorf("an unreachable host was read as a missing window: %v", err)
	}
	if g.posted != "hi" || told.URL != commentURL || len(tm.keys) != 0 {
		t.Errorf("posted %q, told %+v, keys %v", g.posted, told, tm.keys)
	}
}

func TestTellNeedsToken(t *testing.T) {
	d := Deps{Getenv: func(string) string { return "" }}
	if _, err := Tell("r-7", "hi", d); err == nil || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Errorf("got %v", err)
	}
}

func TestPointerNamesTheURL(t *testing.T) {
	if p := Pointer(commentURL); !strings.HasSuffix(p, commentURL) || strings.Contains(p, "\n") {
		t.Errorf("pointer %q", p)
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("pipe closed") }

func TestText(t *testing.T) {
	tests := []struct {
		name    string
		words   []string
		in      string
		want    string
		wantErr string
	}{
		{"words are joined", []string{"go", "ahead"}, "", "go ahead", ""},
		{"one word", []string{"yes"}, "", "yes", ""},
		{"a dash with other words is text", []string{"-", "yes"}, "ignored", "- yes", ""},
		{"stdin text", []string{"-"}, "use the second option", "use the second option", ""},
		{"stdin keeps its lines", []string{"-"}, "one\n\n  two\n```\ncode\n```\n", "one\n\n  two\n```\ncode\n```", ""},
		{"stdin loses trailing whitespace", []string{"-"}, "text \r\n\n", "text", ""},
		{"empty stdin", []string{"-"}, "", "", "the text is empty"},
		{"blank stdin", []string{"-"}, " \t\n\n", "", "the text is empty"},
		{"empty argument", []string{""}, "", "", "the text is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Text(tt.words, strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Text = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	if _, err := Text([]string{"-"}, failReader{}); err == nil || !strings.Contains(err.Error(), "read the text from stdin: pipe closed") {
		t.Errorf("a failing stdin gave %v", err)
	}
}
