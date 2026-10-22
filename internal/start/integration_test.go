package start

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// This test runs engine start for real against a private tmux server, real
// git with a bare repository as the remote, and a fake claude that records
// its arguments and environment. Only the tracker is faked.

var isolatedGit = []string{
	"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
	"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
}

// realGit runs git isolated from the host's config, and answers gh's
// question about the machine user.
func realGit(dir string, env []string, name string, args ...string) (string, error) {
	if name == "gh" {
		return "testbot 42\n", nil
	}
	return session.RunCommand(dir, append(append([]string{}, env...), isolatedGit...), name, args...)
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := realGit(dir, nil, "git", args...); err != nil {
		t.Fatal(err)
	}
}

// bareRemote is <base>/o/r.git with one commit on main.
func bareRemote(t *testing.T) string {
	t.Helper()
	base, work := t.TempDir(), t.TempDir()
	bare := filepath.Join(base, "o", "r.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, bare, "init", "--bare", "--quiet", "--initial-branch=main")
	mustGit(t, work, "init", "--quiet", "--initial-branch=main")
	write(t, filepath.Join(work, "AGENTS.md"), "# rules\n", 0o644)
	mustGit(t, work, "add", "AGENTS.md")
	mustGit(t, work, "commit", "--quiet", "-m", "init")
	mustGit(t, work, "push", "--quiet", bare, "HEAD:refs/heads/main")
	return base
}

// privateTmux is a Tmux on a socket of its own, with no tmux.conf and no
// keep-awake, killed when the test ends. It skips without tmux.
func privateTmux(t *testing.T) *session.Tmux {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	if v, ok := os.LookupEnv("TMUX"); ok {
		t.Setenv("TMUX", v)
		if err := os.Unsetenv("TMUX"); err != nil {
			t.Fatal(err)
		}
	}
	socket := fmt.Sprintf("engine-start-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	isolated := func(name string, args ...string) (string, error) {
		return session.Exec(name, append([]string{args[0], args[1], "-f", "/dev/null"}, args[2:]...)...)
	}
	t.Cleanup(func() {
		path, _ := session.Exec(bin, "-L", socket, "display-message", "-p", "#{socket_path}")
		_, _ = session.Exec(bin, "-L", socket, "kill-server")
		if path = strings.TrimSpace(path); strings.Contains(path, socket) {
			_ = os.Remove(path)
		}
	})
	return &session.Tmux{Run: isolated, Bin: bin, Socket: socket}
}

// fakeClaude records its arguments, one per line, and GH_TOKEN, then waits.
func fakeClaude(t *testing.T, path, record string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\n{ printf 'arg:%%s\\n' \"$@\"; printf 'token:%%s\\n' \"$GH_TOKEN\"; printf 'pwd:%%s\\n' \"$PWD\"; } > %s\necho fake claude up\nexec sleep 60\n", record)
	write(t, path, script, 0o755)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRealStart(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	tm := privateTmux(t)
	h := newHome(t, "mac")
	record := filepath.Join(t.TempDir(), "claude.args")
	fakeClaude(t, h.bin, record)
	gh := &ghFake{issue: issue{number: 7, state: "open", parent: 3, body: card("light")}}
	d := Deps{GH: gh.run, Run: realGit, Tmux: tm, Home: h.dir, Getenv: func(string) string { return "" },
		Now: fixedNow, BaseURL: "file://" + bareRemote(t)}
	var out bytes.Buffer
	if err := Run(Options{Ref: ref(7)}, d, &out); err != nil {
		t.Fatal(err)
	}

	windows, err := tm.Windows()
	if err != nil || len(windows) != 1 || windows[0].Name != "r-7" {
		t.Fatalf("windows = %+v, %v", windows, err)
	}
	eventually(t, "the fake claude's record", func() bool { return strings.Contains(readFile(record), "pwd:") })
	kickoff := "arg:Execute " + ref(7).String() + " with the engine executor, hands-off mode. Reviewer model: opus-x. " +
		"run=r-7-20260102T030405Z tmux=engine:r-7 rc=r-7\n"
	got := readFile(record)
	for _, want := range []string{
		"arg:--model\narg:haiku-x\narg:--effort\narg:medium\narg:-n\narg:r-7\narg:--remote-control\narg:r-7\n" + kickoff,
		"token:tok\n", "/task-r-7\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the fake claude got\n%s\nwant it to contain\n%s", got, want)
		}
	}

	runDir := filepath.Join(h.dir, ".local/share/engine/runs/r-7-20260102T030405Z")
	var rec session.Record
	if err := json.Unmarshal([]byte(readFile(filepath.Join(runDir, "run.json"))), &rec); err != nil {
		t.Fatalf("run.json: %v", err)
	}
	if rec.RunID != "r-7-20260102T030405Z" || rec.Task != ref(7).String() || rec.Feature != ref(3).String() ||
		rec.Host != "mac" || rec.Tier != "light" || rec.Model != "haiku-x" || rec.Tmux != "engine:"+windows[0].ID ||
		rec.Branch != "task/r-7" || !rec.Spawned.Equal(fixedNow()) || rec.PaneLog != filepath.Join(runDir, "pane.log") {
		t.Errorf("run.json = %+v", rec)
	}
	eventually(t, "the pane log", func() bool { return strings.Contains(readFile(rec.PaneLog), "fake claude up") })
	if trust := readFile(filepath.Join(h.dir, ".claude.json")); !strings.Contains(trust, `"hasTrustDialogAccepted": true`) ||
		!strings.Contains(trust, rec.Worktree) {
		t.Errorf("the worktree is not trusted:\n%s", trust)
	}
	for _, want := range []string{"Started " + ref(7).String() + " as r-7", "Attach: engine attach r-7 (once", "or: tmux attach -t '=engine:" + windows[0].ID} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	// A second start finds the window and refuses.
	if err := Run(Options{Ref: ref(7)}, d, &out); err == nil || !strings.Contains(err.Error(), "already has a window r-7") {
		t.Errorf("second start: %v", err)
	}
}

func readFile(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}
