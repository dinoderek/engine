package hostops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
)

// This test runs the ops for real against a private tmux server, real git
// with a bare repository as the remote, and a fake claude: sync-repo, then
// spawn, status and release.

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
	socket := fmt.Sprintf("engine-hostops-test-%d-%d", os.Getpid(), time.Now().UnixNano())
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

// fakeClaude lists no agents; the session r-8 exits 3 at once, and any
// other prints a line and waits.
const fakeClaude = `#!/bin/sh
if [ "$1" = agents ]; then echo '[]'; exit 0; fi
for a; do if [ "$a" = r-8 ]; then echo dying; exit 3; fi; done
echo fake claude up
exec sleep 60
`

func TestRealSpawnStatusRelease(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	tm := privateTmux(t)
	h := newHome(t, "mac")
	write(t, h.bin, fakeClaude, 0o755)
	d := Deps{Run: realGit, Exec: session.Exec, Tmux: tm, Home: h.dir, Getenv: func(string) string { return "" },
		Now: fixedNow, BaseURL: "file://" + bareRemote(t)}
	synced, err := SyncRepo(d, "o/r")
	if err != nil || synced.DefaultBranch != "main" {
		t.Fatalf("synced %+v, %v", synced, err)
	}
	spawned := map[int]Spawned{}
	for _, task := range []int{7, 8} {
		r := request()
		r.Task, r.RunID = task, fmt.Sprintf("r-%d-20260102T030405Z", task)
		if spawned[task], err = Spawn(d, r); err != nil {
			t.Fatal(err)
		}
	}
	var s Status
	eventually(t, "r-8 to exit and r-7 to print", func() bool {
		s, err = ReadStatus(d)
		return err == nil && len(s.Windows) == 2 && !s.Windows[1].Alive && strings.Contains(s.Windows[0].Screen, "fake claude up")
	})
	if w := s.Windows[1]; w.Name != "r-8" || w.ExitCode == nil || *w.ExitCode != 3 || !strings.Contains(w.Screen, "dying") {
		t.Errorf("the dead window %+v", w)
	}
	if s.Windows[0].Name != "r-7" || !s.Windows[0].Alive || s.FreeSlots != 1 || s.AgentsError != "" || len(s.Runs) != 2 || s.Runs[0].Record.Tmux != "engine:"+spawned[7].Window {
		t.Errorf("status %+v", s)
	}

	strayWindowsTakeNoSlot(t, d, tm, h.dir, 1)

	done, err := Release(d, "r-7", false)
	if err != nil || !done.WindowClosed || done.WorktreeKept {
		t.Errorf("released r-7: %+v, %v", done, err)
	}
	if _, err := os.Stat(spawned[7].Worktree); !os.IsNotExist(err) {
		t.Errorf("r-7's worktree is still there: %v", err)
	}
	mustGit(t, synced.Clone, "rev-parse", "--verify", "--quiet", "refs/heads/task/r-7") // the branch stays
	failed, err := Release(d, "r-8", true)
	if err != nil || !failed.WindowClosed || !failed.WorktreeKept {
		t.Errorf("released r-8: %+v, %v", failed, err)
	}
	if _, err := os.Stat(spawned[8].Worktree); err != nil {
		t.Errorf("a failed run's worktree is kept: %v", err)
	}
	if s, err = ReadStatus(d); err != nil || len(s.Windows) != 0 || s.FreeSlots != 2 {
		t.Errorf("after release: %+v, %v", s, err)
	}
}

// strayWindowsTakeNoSlot opens a stray shell and engine run's own window,
// both live and neither a session, and expects the free slots to stay free.
func strayWindowsTakeNoSlot(t *testing.T, d Deps, tm *session.Tmux, dir string, free int) {
	t.Helper()
	before, err := ReadStatus(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"stray-shell", "run-o-56"} {
		o, err := tm.Open(session.Window{Name: name, Dir: dir, Command: "exec sleep 60"})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tm.Kill(o.ID) }()
	}
	got, err := ReadStatus(d)
	if err != nil || len(got.Windows) != len(before.Windows)+2 || got.FreeSlots != free {
		t.Errorf("with two strays: %d windows, %d free slots, %v; want %d and %d",
			len(got.Windows), got.FreeSlots, err, len(before.Windows)+2, free)
	}
}
