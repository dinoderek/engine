package hostops

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/session"
)

func TestServiceUnit(t *testing.T) {
	for _, tc := range []struct{ name, cgroup, want string }{
		{"inside the server's unit", "0::/user.slice/user-1000.slice/user@1000.service/app.slice/engine-server.service\n", "engine-server.service"},
		{"a scope of its own", "0::/user.slice/user-1000.slice/user@1000.service/app.slice/run-p42-i43.scope\n", ""},
		{"a login session", "0::/user.slice/user-1000.slice/session-579.scope\n", ""},
		{"a service's delegated subgroup", "0::/system.slice/ci.service/payload\n", "ci.service"},
		{"a legacy host", "12:pids:/user.slice\n1:name=systemd:/system.slice/engine.service\n", "engine.service"},
		{"the unified line wins", "1:name=systemd:/system.slice/old.service\n0::/user.slice/session-2.scope\n", ""},
		{"outside systemd", "0::/\n", ""},
		{"no usable line", "garbage\n", ""},
		{"nothing", "", ""},
	} {
		if got := serviceUnit(tc.cgroup); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// cgroupRunner answers cat with cgroup, and readlink with dir.
func cgroupRunner(cgroup, dir string, err error) func(string, ...string) (string, error) {
	return func(name string, args ...string) (string, error) {
		switch name {
		case "cat":
			if len(args) != 1 || args[0] != "/proc/4242/cgroup" {
				return "", errors.New("unexpected cat " + strings.Join(args, " "))
			}
			return cgroup, err
		case "readlink":
			return dir + "\n", nil
		}
		return "", errors.New("unexpected " + name)
	}
}

func TestTmuxUnitReadsTheServersCgroup(t *testing.T) {
	tm := &tmuxFake{serverPid: "4242"}
	d := Deps{Tmux: tm.tmux(), Exec: cgroupRunner("0::/user.slice/user-1000.slice/user@1000.service/app.slice/engine-server.service\n", "", nil)}
	got, err := d.tmuxUnit("linux", 4242)
	if err != nil || got == nil || got.Pid != 4242 || got.Unit != "engine-server.service" ||
		!strings.Contains(got.Reason, "inside engine-server.service") || !strings.Contains(got.Reason, "`tmux kill-server`") {
		t.Fatalf("got %+v, %v", got, err)
	}
	d.Tmux.Socket = "side"
	if got, _ := d.tmuxUnit("linux", 4242); got == nil || !strings.Contains(got.Reason, "`tmux -L side kill-server`") {
		t.Errorf("a private server's restart command names its socket: %+v", got)
	}
	d.Exec = cgroupRunner("0::/user.slice/user-1000.slice/user@1000.service/app.slice/run-p1-i2.scope\n", "", nil)
	if got, err := d.tmuxUnit("linux", 4242); got != nil || err != nil {
		t.Errorf("a server in a scope: %+v, %v", got, err)
	}
	d.Exec = cgroupRunner("", "", errors.New("permission denied"))
	if got, err := d.tmuxUnit("linux", 4242); got != nil || err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("an unreadable cgroup is unknown: %+v, %v", got, err)
	}
	d.Exec = func(string, ...string) (string, error) { return "", errors.New("must not run") }
	if got, err := d.tmuxUnit("darwin", 4242); got != nil || err != nil {
		t.Errorf("macOS has no cgroups: %+v, %v", got, err)
	}
}

func TestStatusReportsAServerInsideAUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads the server's cgroup on Linux only")
	}
	h := newHome(t, "mac")
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1"}, serverPid: "4242"}
	d := Deps{Tmux: tm.tmux(), Home: h.dir, Exec: cgroupRunner("0::/system.slice/engine-server.service\n", t.TempDir(), nil)}
	got, err := ReadStatus(d)
	if err != nil || got.TmuxUnit == nil || got.TmuxUnit.Unit != "engine-server.service" || got.TmuxUnitError != "" || got.TmuxCwdGone != nil {
		t.Fatalf("status %+v, %v", got, err)
	}
	d.Exec = cgroupRunner("", t.TempDir(), errors.New("cat: boom"))
	if got, _ = ReadStatus(d); got.TmuxUnit != nil || !strings.Contains(got.TmuxUnitError, "cat: boom") {
		t.Errorf("an unreadable cgroup: %+v", got)
	}
	tm.readOnly(t)
}

func TestStatusSaysNothingOfTheServerWhenNoneRuns(t *testing.T) {
	h := newHome(t, "mac")
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1"}}
	d := Deps{Tmux: tm.tmux(), Home: h.dir, Exec: func(string, ...string) (string, error) { return "", errors.New("must not run") }}
	got, err := ReadStatus(d)
	if err != nil || got.TmuxUnit != nil || got.TmuxUnitError != "" || got.TmuxCwdGone != nil || got.TmuxCwdError != "" {
		t.Errorf("status %+v, %v", got, err)
	}
}

func TestStatusSaysWhenTheServerCannotBeAsked(t *testing.T) {
	h := newHome(t, "mac")
	tm := &tmuxFake{windows: []string{"@1 %1 0 r-1"}, serverPid: "not-a-pid"}
	got, err := ReadStatus(Deps{Tmux: tm.tmux(), Home: h.dir})
	if err != nil || got.TmuxUnitError == "" || got.TmuxUnitError != got.TmuxCwdError {
		t.Errorf("status %+v, %v", got, err)
	}
}

// The real check on this process: cat reads its cgroup, and the unit found
// is the one the file names.
func TestRealTmuxUnitReadsAProcessCgroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	if _, err := os.Stat("/proc/self/cgroup"); err != nil {
		t.Skip("no /proc/self/cgroup")
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	d := Deps{Tmux: (&tmuxFake{}).tmux(), Exec: session.Exec}
	got, err := d.tmuxUnit("linux", os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if want := serviceUnit(string(data)); (got == nil) != (want == "") || (got != nil && got.Unit != want) {
		t.Errorf("read %+v, want the unit %q", got, want)
	}
}
