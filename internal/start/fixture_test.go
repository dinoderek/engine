package start

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

// hostsTOML is a host config with two slots on mac, and a host that runs
// Codex only. Tests replace @BIN@ with the fake harness's path.
const hostsTOML = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 8
launch = "{bin} --permission-mode auto --model {model} --effort {effort} -n {name} --remote-control {name}"
tiers.deep     = { model = "opus-x",   effort = "high" }
tiers.standard = { model = "sonnet-x", effort = "high" }
tiers.light    = { model = "haiku-x",  effort = "medium" }

[harness.codex]
max_sessions = 2
launch = "{bin} -m {model} -c model_reasoning_effort={effort}"
tiers.deep     = { model = "sol", effort = "high" }
tiers.standard = { model = "sol", effort = "medium" }
tiers.light    = { model = "luna", effort = "medium" }

[host.mac]
address = "me@mac"
harnesses = ["claude", "codex"]
ram_gb = 24
capabilities = ["macos"]
bin = { claude = "@BIN@", codex = "~/bin/codex" }

[host.box]
address = "me@box"
harnesses = ["codex"]
ram_gb = 24
capabilities = ["linux"]
bin = { codex = "~/bin/codex" }
`

// home is a temp home directory with the host config, the host file, the
// token file and an executable fake claude at ~/bin/claude.
type home struct {
	dir string
	bin string
}

func newHome(t *testing.T, host string) home {
	t.Helper()
	h := home{dir: t.TempDir()}
	h.bin = filepath.Join(h.dir, "bin", "claude")
	write(t, h.bin, "#!/bin/sh\nexit 0\n", 0o755)
	cfg := strings.ReplaceAll(hostsTOML, "@BIN@", "~/bin/claude")
	write(t, filepath.Join(h.dir, ".config", "engine", "hosts.toml"), cfg, 0o600)
	write(t, filepath.Join(h.dir, ".config", "engine", "host"), host+"\n", 0o600)
	write(t, filepath.Join(h.dir, ".config", "engine", "github-token"), "tok\n", 0o600)
	return h
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// files lists every file under dir, relative to it.
func files(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, rel)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// issue is a task issue as gh api returns it.
type issue struct {
	number   int
	state    string
	labels   []string
	parent   int // 0 for a standalone task
	body     string
	blockers []int // the open ones
}

func (i issue) json() string {
	labels := make([]string, len(i.labels))
	for k, l := range i.labels {
		labels[k] = fmt.Sprintf(`{"name":%q}`, l)
	}
	parent := "null"
	if i.parent > 0 {
		parent = fmt.Sprintf(`"https://api.github.com/repos/o/r/issues/%d"`, i.parent)
	}
	return fmt.Sprintf(`{"number":%d,"title":"A task","state":%q,"body":%q,`+
		`"repository_url":"https://api.github.com/repos/o/r","parent_issue_url":%s,"labels":[%s]}`,
		i.number, i.state, i.body, parent, strings.Join(labels, ","))
}

func (i issue) blockersJSON() string {
	items := make([]string, len(i.blockers))
	for k, n := range i.blockers {
		items[k] = fmt.Sprintf(`{"number":%d,"state":"open","repository_url":"https://api.github.com/repos/o/r"}`, n)
	}
	return "[" + strings.Join(items, ",") + "]"
}

// ghFake answers the tracker's reads of one issue and records every call.
type ghFake struct {
	issue issue
	calls []string
}

func (f *ghFake) run(_ []string, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	base := fmt.Sprintf("repos/o/r/issues/%d", f.issue.number)
	switch {
	case len(args) < 2 || args[0] != "api":
		return "", errors.New("unexpected gh call: " + line)
	case args[1] == base:
		return f.issue.json(), nil
	case args[1] == base+"/dependencies/blocked_by":
		return f.issue.blockersJSON(), nil
	}
	return "", errors.New("no reply for " + line)
}

// readOnly reports a call that is not a read.
func (f *ghFake) readOnly(t *testing.T) {
	t.Helper()
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "gh api repos/") || strings.Contains(c, "--method") && !strings.Contains(c, "--method GET") {
			t.Errorf("a refusal wrote to the tracker: %s", c)
		}
	}
}

// tmuxFake answers has-session and list-windows from windows, and records
// every call.
type tmuxFake struct {
	windows []string // "#{window_id} #{pane_id} #{pane_dead} #{window_name}"
	calls   []string
}

func (f *tmuxFake) run(_ string, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "has-session":
		if f.windows == nil {
			return "", &session.ExitError{Command: "tmux has-session", Code: 1}
		}
		return "", nil
	case "list-windows":
		return strings.Join(f.windows, "\n") + "\n", nil
	}
	return "", errors.New("tmux " + args[0] + " is not a read")
}

// gitFake fails the test on any git or gh call: a refusal never syncs.
func gitFake(t *testing.T) session.RunFunc {
	return func(_ string, _ []string, name string, args ...string) (string, error) {
		t.Errorf("a refusal ran %s %s", name, strings.Join(args, " "))
		return "", errors.New("not allowed")
	}
}

func ref(n int) tracker.Ref { return tracker.Ref{Repo: "o/r", Number: n} }

func fixedNow() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
