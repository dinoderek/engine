package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// serverDir reads the working directory of the process pid.
func serverDir(t *testing.T, pid int) string {
	t.Helper()
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
		if err != nil {
			t.Skipf("lsof: %v", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if dir, ok := strings.CutPrefix(line, "n"); ok {
				return dir
			}
		}
		t.Fatalf("lsof printed %q", out)
	}
	dir, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err != nil {
		t.Skipf("no /proc: %v", err)
	}
	return dir
}

// An engine process may run from a worktree. The tmux server it starts must
// live in the home directory instead, so that removing the worktree leaves
// the server, and every later window, unharmed.
func TestRealServerStartedByEngineNeverLivesInAWorktree(t *testing.T) {
	tm := privateTmux(t)
	home, worktree, work := t.TempDir(), t.TempDir(), t.TempDir()
	run := ExecIn(home)
	tm.Run = func(name string, args ...string) (string, error) {
		return run(name, append([]string{args[0], args[1], "-f", "/dev/null"}, args[2:]...)...)
	}
	tm.Home = home
	t.Chdir(worktree)

	if _, err := tm.Open(Window{Name: "first", Dir: work, Command: "sleep 300"}); err != nil {
		t.Fatal(err)
	}
	pid, running, err := tm.ServerPid()
	if err != nil || !running {
		t.Fatalf("server pid %d, %v, %v", pid, running, err)
	}
	if got, want := serverDir(t, pid), home; resolve(t, got) != resolve(t, want) {
		t.Errorf("the server runs in %s, want the home directory %s", got, want)
	}
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Open(Window{Name: "second", Dir: filepath.Join(work), Command: "sleep 300"}); err != nil {
		t.Errorf("a window opened after the worktree was removed: %v", err)
	}
	if ok, err := tm.HasWindow("second"); err != nil || !ok {
		t.Errorf("second window: %v, %v", ok, err)
	}
	if _, err := os.Stat(tm.lockPath()); err != nil {
		t.Errorf("the host lock file: %v", err)
	}
}
