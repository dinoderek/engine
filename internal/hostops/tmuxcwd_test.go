package hostops

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

func TestParseLsofCwd(t *testing.T) {
	for _, tc := range []struct {
		name, out, want string
		fails           bool
	}{
		{"field output", "p4242\nfcwd\nn/Users/me/work tree\n", "/Users/me/work tree", false},
		{"no trailing newline", "p1\nfcwd\nn/tmp", "/tmp", false},
		{"no name field", "p4242\nfcwd\n", "", true},
		{"empty name", "p4242\nn\n", "", true},
		{"nothing", "", "", true},
	} {
		got, err := parseLsofCwd(tc.out)
		if got != tc.want || (err != nil) != tc.fails {
			t.Errorf("%s: %q, %v", tc.name, got, err)
		}
	}
}

// cwdRunner answers readlink (Linux) and lsof (macOS) with dir.
func cwdRunner(dir string, err error) session.Runner {
	return func(name string, args ...string) (string, error) {
		switch name {
		case "readlink":
			return dir + "\n", err
		case "lsof":
			return "p4242\nfcwd\nn" + dir + "\n", err
		}
		return "", errors.New("unexpected " + name)
	}
}

func TestServerCwdAsksTheHostsTool(t *testing.T) {
	var got []string
	d := Deps{Exec: func(name string, args ...string) (string, error) {
		got = append(got, name+" "+strings.Join(args, " "))
		return "p9\nn/w\n", nil
	}}
	if dir, err := d.serverCwd("darwin", 9); err != nil || dir != "/w" {
		t.Errorf("darwin: %q, %v", dir, err)
	}
	d.Exec = func(name string, args ...string) (string, error) {
		got = append(got, name+" "+strings.Join(args, " "))
		return "/w (deleted)\n", nil
	}
	if dir, err := d.serverCwd("linux", 9); err != nil || dir != "/w (deleted)" {
		t.Errorf("linux: %q, %v", dir, err)
	}
	want := []string{"lsof -a -p 9 -d cwd -Fn", "readlink /proc/9/cwd"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("ran %v, want %v", got, want)
	}
	d.Exec = cwdRunner("", errors.New("boom"))
	if _, err := d.serverCwd("linux", 9); err == nil {
		t.Error("a failed read was reported as a directory")
	}
	d.Exec = cwdRunner("", nil)
	if _, err := d.serverCwd("linux", 9); err == nil {
		t.Error("an empty readlink was reported as a directory")
	}
}

func TestStatusReportsAServerWhoseDirectoryIsGone(t *testing.T) {
	h := newHome(t, "mac")
	gone := filepath.Join(t.TempDir(), "removed-worktree")
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1"}, serverPid: "4242"}
	d := Deps{Tmux: tm.tmux(), Home: h.dir, Exec: cwdRunner(gone, nil)}
	got, err := ReadStatus(d)
	if err != nil {
		t.Fatal(err)
	}
	c := got.TmuxCwdGone
	if c == nil || c.Pid != 4242 || c.Dir != gone || !strings.Contains(c.Reason, "`tmux kill-server`") || !strings.Contains(c.Reason, gone) {
		t.Fatalf("status %+v", got)
	}
	d.Tmux.Socket = "side"
	if got, _ = ReadStatus(d); got.TmuxCwdGone == nil || !strings.Contains(got.TmuxCwdGone.Reason, "`tmux -L side kill-server`") {
		t.Errorf("a private server's restart command names its socket: %+v", got.TmuxCwdGone)
	}
	d.Exec = cwdRunner(gone+" (deleted)", nil)
	if got, _ = ReadStatus(d); got.TmuxCwdGone == nil || got.TmuxCwdGone.Dir != gone {
		t.Errorf("a (deleted) cwd is gone even when the path is back: %+v", got.TmuxCwdGone)
	}
}

func TestStatusReportsAServerWhoseDirectoryExistsAsReady(t *testing.T) {
	h := newHome(t, "mac")
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1"}, serverPid: "4242"}
	got, err := ReadStatus(Deps{Tmux: tm.tmux(), Home: h.dir, Exec: cwdRunner(t.TempDir(), nil)})
	if err != nil || got.TmuxCwdGone != nil || got.TmuxCwdError != "" {
		t.Errorf("status %+v, %v", got, err)
	}
	tm.readOnly(t)
}

// A directory that cannot be read is unknown, never passed: the status says
// so and the host is not marked unready.
func TestStatusSaysWhenTheServerDirectoryIsUnknown(t *testing.T) {
	h := newHome(t, "mac")
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1"}, serverPid: "4242"}
	got, err := ReadStatus(Deps{Tmux: tm.tmux(), Home: h.dir, Exec: cwdRunner("", errors.New("lsof: not found"))})
	if err != nil || got.TmuxCwdGone != nil || !strings.Contains(got.TmuxCwdError, "lsof: not found") {
		t.Errorf("status %+v, %v", got, err)
	}
}

// startDoomed starts a private tmux server from a directory and removes the
// directory, as a worktree is removed after its session ends.
func startDoomed(t *testing.T, tm *session.Tmux) {
	t.Helper()
	dir, err := os.MkdirTemp("", "doomed")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tm.Bin, "-L", tm.Socket, "-f", "/dev/null", "new-session", "-d", "-s", "doomed")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, out)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
}

// The check against a real server: one started from a directory that is then
// removed is reported, and one engine started is not.
func TestRealStatusReportsADeadServerDirectory(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("reads the server's directory through /proc or lsof")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	tm := privateTmux(t)
	startDoomed(t, tm)
	d := Deps{Tmux: tm, Exec: session.Exec}
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("lsof"); err != nil {
			t.Skip("lsof is not installed")
		}
	}
	got, err := d.tmuxCwdGone()
	if err != nil || got == nil || got.Pid == 0 || !strings.Contains(got.Reason, "kill-server") {
		t.Fatalf("a server in a removed directory: %+v, %v", got, err)
	}
}
