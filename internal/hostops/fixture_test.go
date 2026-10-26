package hostops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// hostsTOML is a host config with two slots on mac, a host that runs Codex
// only and one that runs Claude only. Tests replace @BIN@ with the fake
// harness's path. Codex's launch line quotes {clone}, as the real one does.
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
launch = "{bin} -c 'sandbox_workspace_write.writable_roots=[\"{clone}\"]' -m {model} -c model_reasoning_effort={effort}"
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

[host.cl]
address = "me@cl"
harnesses = ["claude"]
ram_gb = 24
capabilities = ["linux"]
bin = { claude = "~/bin/claude" }
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

func fixedNow() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

// tmuxFake plays one tmux server. windows are list-windows lines,
// "#{window_id} #{pane_id} #{pane_dead} #{window_name}"; codes are the exit
// codes of dead windows by id; screens are capture-pane's output by window
// id. new-window opens @9, whose command it records. Every call is recorded.
type tmuxFake struct {
	windows []string
	codes   map[string]string
	screens map[string]string
	fail    error // every list-windows fails
	openErr error // new-session and new-window fail
	// awakeErr fails the keep-awake job, which the fake's Tmux then starts.
	awakeErr error
	command  string
	calls    []string
}

func (f *tmuxFake) run(_ string, args ...string) (string, error) {
	line := strings.Join(args, " ")
	f.calls = append(f.calls, line)
	switch args[0] {
	case "has-session":
		if f.windows == nil {
			return "", &session.ExitError{Command: "tmux has-session", Code: 1}
		}
	case "list-windows":
		return f.list(line)
	case "new-session", "new-window":
		if f.openErr != nil {
			return "", f.openErr
		}
		return "@9 %9\n", nil
	case "split-window":
		return "%10\n", nil
	case "run-shell":
		return "", f.awakeErr
	case "respawn-pane":
		f.command = args[len(args)-1]
	case "capture-pane":
		return f.screens[strings.TrimPrefix(args[3], "="+session.Name+":")], nil
	}
	return "", nil
}

func (f *tmuxFake) list(line string) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	var out []string
	for _, w := range f.windows {
		if !strings.Contains(line, "pane_dead_status") {
			out = append(out, w)
			continue
		}
		fields := strings.Fields(w)
		out = append(out, fields[0]+" "+fields[2]+" "+f.codes[fields[0]])
	}
	return strings.Join(out, "\n") + "\n", nil
}

func (f *tmuxFake) tmux() *session.Tmux {
	t := &session.Tmux{Run: f.run, Bin: "tmux"}
	if f.awakeErr != nil {
		t.Awake = "caffeinate"
	}
	return t
}

// readOnly reports a tmux call that changes something.
func (f *tmuxFake) readOnly(t *testing.T) {
	t.Helper()
	for _, call := range f.calls {
		if !strings.HasPrefix(call, "has-session") && !strings.HasPrefix(call, "list-windows") && !strings.HasPrefix(call, "capture-pane") {
			t.Errorf("a read ran tmux %s", call)
		}
	}
}

// gitFake plays git and gh for engine's clone of o/r, which GitHub serves:
// worktree add creates
// the worktree's directory, and worktree list shows it afterwards. Every call
// is recorded, and fail fails the calls whose line contains its key.
type gitFake struct {
	calls []string
	fail  map[string]error
	added string
	// branch is true when the task's branch already exists in the clone,
	// with commits of its own.
	branch bool
}

func (f *gitFake) run(_ string, _ []string, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	for key, err := range f.fail {
		if strings.Contains(line, key) {
			return "", err
		}
	}
	switch {
	case name == "gh":
		return "testbot 42\n", nil
	case strings.Contains(line, "get-url") || strings.Contains(line, "config --get remote.origin.url"):
		return "https://github.com/o/r.git\n", nil
	case strings.Contains(line, "symbolic-ref"):
		return "origin/main\n", nil
	case strings.Contains(line, "rev-parse --verify") && (!f.branch || strings.Contains(line, "refs/remotes/")),
		strings.Contains(line, "merge-base --is-ancestor"):
		return "", errors.New("exit status 1")
	case strings.Contains(line, "worktree add"):
		f.added = args[len(args)-2]
		return "", os.MkdirAll(f.added, 0o755)
	case strings.Contains(line, "worktree list") && f.added != "":
		return "worktree " + f.added + "\nbranch refs/heads/task/r-7\n", nil
	}
	return "", nil
}

// noGit fails the test on any git or gh call.
func noGit(t *testing.T) session.RunFunc {
	return func(_ string, _ []string, name string, args ...string) (string, error) {
		t.Errorf("ran %s %s", name, strings.Join(args, " "))
		return "", errors.New("not allowed")
	}
}

// clone makes h's clone of o/r look synced, with the lock file a sync
// leaves beside it.
func (h home) clone(t *testing.T) string {
	t.Helper()
	path := filepath.Join(h.dir, ".local/share/engine/repos/o/r")
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, path+".lock", "", 0o600)
	return path
}
