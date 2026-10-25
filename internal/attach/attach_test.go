package attach

import (
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
launch = "{bin} --permission-mode auto --model {model} --effort {effort} -n {name} --remote-control {name}"
tiers.deep     = { model = "claude-opus-5-5",   effort = "high" }
tiers.standard = { model = "claude-sonnet-5-5", effort = "high" }
tiers.light    = { model = "claude-haiku-5-5",  effort = "medium" }

[host.mac]
address = "dino@mac.example.ts.net"
harnesses = ["claude"]
ram_gb = 48
reserve_gb = 16
capabilities = ["macos"]
bin = { claude = "~/.local/bin/claude" }

[host.devbox]
address = "dino@devbox.example.ts.net"
harnesses = ["claude"]
ram_gb = 32
capabilities = ["linux"]
bin = { claude = "~/.local/bin/claude" }
`

const (
	devboxAddr = "dino@devbox.example.ts.net"
	windowsFD  = "#{window_id} #{pane_id} #{pane_dead} #{window_name}"
)

// home returns a home directory whose engine config names self as this host.
func home(t *testing.T, self string) string {
	t.Helper()
	h := t.TempDir()
	dir := hosts.ConfigDir(h)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hosts.toml"), []byte(hostsTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host"), []byte(self+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

// claimBody is the claim comment a session posts for the task name on host.
func claimBody(name, host string) string {
	return fmt.Sprintf("Claim: %s\nRun: %s-20261021T000000Z\nHost: %s\nHarness: claude-code 2.1.302 (claude-sonnet-5-5)\nMode: hands-off\nStarted by: engine start\nTmux: engine:%s\nRemote Control: none\nBranch: task/%s", name, name, host, name, name)
}

// task is a claimed task issue and one claim comment on it.
type task struct {
	repo   string
	number int
	posted time.Time
	body   string
}

// fakeGh answers the claim searches and comments of the given tasks. It
// refuses any call that is not a gh api call as the machine user.
func fakeGh(t *testing.T, tasks ...task) tracker.Runner {
	t.Helper()
	var items []map[string]any
	comments := map[string][]map[string]any{}
	for _, k := range tasks {
		items = append(items, map[string]any{
			"number": k.number, "title": "task", "state": "open",
			"repository_url": "https://api.github.com/repos/" + k.repo,
			"labels":         []map[string]string{{"name": "engine:running"}},
		})
		path := fmt.Sprintf("repos/%s/issues/%d/comments", k.repo, k.number)
		comments[path] = append(comments[path], map[string]any{
			"body": k.body, "html_url": "https://github.com/" + k.repo, "created_at": k.posted.Format(time.RFC3339),
			"user": map[string]string{"login": "dinoderek-agents"},
		})
	}
	return func(env []string, name string, args ...string) (string, error) {
		if name != "gh" || !slices.Contains(env, "GH_TOKEN=tok") || len(args) < 2 || args[0] != "api" {
			t.Errorf("unexpected command %s %v (env %v)", name, args, env)
			return "", errors.New("unexpected command")
		}
		switch path := args[1]; {
		case path == "search/issues":
			return jsonOf(t, map[string]any{"incomplete_results": false, "items": items}), nil
		case comments[path] != nil:
			return jsonOf(t, comments[path]), nil
		}
		t.Errorf("unexpected gh api %s", args[1])
		return "", errors.New("unexpected gh api")
	}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// fakeTmux answers the tmux calls of one host: locally (name "tmux") or over
// ssh (name "ssh", the remote command as one quoted string).
type fakeTmux struct {
	windows string             // what list-windows prints
	session bool               // whether the session engine exists
	sshErr  *session.ExitError // what every ssh call fails with; nil for none
	calls   []string
}

func (f *fakeTmux) run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	sub := ""
	if name == "ssh" {
		if f.sshErr != nil {
			return "", f.sshErr
		}
		sub = strings.Trim(strings.Fields(args[len(args)-1])[1], "'")
	} else {
		sub = args[0]
	}
	switch sub {
	case "has-session":
		if f.session {
			return "", nil
		}
		return "", &session.ExitError{Command: "tmux has-session", Code: 1, Stderr: "can't find session: engine"}
	case "list-windows":
		return f.windows, nil
	}
	return "", fmt.Errorf("unexpected tmux %s", sub)
}

// replaced records the process that would have replaced this one.
type replaced struct {
	path  string
	argv  []string
	calls int
	err   error
}

func (r *replaced) replace(path string, argv []string) error {
	r.calls++
	r.path, r.argv = path, argv
	return r.err
}

// rig wires Deps to the fakes. Its environment holds the token, and TMUX when
// inside tmux.
type rig struct {
	gh      tracker.Runner
	tmux    *fakeTmux
	exec    *replaced
	env     map[string]string
	missing string // a program LookPath does not find
	home    string
}

func (r rig) deps() Deps {
	return Deps{
		Gh:  r.gh,
		Run: r.tmux.run,
		Replace: func(path string, argv []string) error {
			return r.exec.replace(path, argv)
		},
		LookPath: func(file string) (string, error) {
			if file == r.missing {
				return "", fmt.Errorf("%s: executable file not found in $PATH", file)
			}
			return "/bin/" + file, nil
		},
		Getenv: func(k string) string { return r.env[k] },
		Home:   r.home,
	}
}

func newRig(t *testing.T, self string, tmux *fakeTmux, tasks ...task) rig {
	t.Helper()
	return rig{
		gh: fakeGh(t, tasks...), tmux: tmux, exec: &replaced{},
		env: map[string]string{"GH_TOKEN": "tok"}, home: home(t, self),
	}
}

var (
	t0 = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Hour)
)

// webTask is the task web.app-412, claimed on host, on its own issue.
func webTask(host string, posted time.Time) task {
	return task{repo: "o/web.app", number: 4, posted: posted, body: claimBody("web.app-412", host)}
}

// localWindows has the task's window, whose name holds a dot, and another.
const localWindows = "@3 %5 0 boga3-412\n@4 %6 0 web.app-412\n"

func TestAttachLocalOutsideTmux(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{windows: localWindows, session: true}, webTask("mac", t0))
	if err := Attach("web.app-412", r.deps()); err != nil {
		t.Fatal(err)
	}
	want := []string{"tmux", "attach", "-t", "=engine:@4"}
	if r.exec.calls != 1 || r.exec.path != "/bin/tmux" || !slices.Equal(r.exec.argv, want) {
		t.Errorf("replaced %d times with %s %v, want once with /bin/tmux %v", r.exec.calls, r.exec.path, r.exec.argv, want)
	}
}

func TestAttachLocalInsideTmuxSwitchesTheClient(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{windows: localWindows, session: true}, webTask("mac", t0))
	r.env["TMUX"] = "/private/tmp/tmux-501/default,123,0"
	if err := Attach("web.app-412", r.deps()); err != nil {
		t.Fatal(err)
	}
	want := []string{"tmux", "switch-client", "-t", "=engine:@4"}
	if !slices.Equal(r.exec.argv, want) {
		t.Errorf("argv %v, want %v", r.exec.argv, want)
	}
}

func TestAttachRemoteFindsTheWindowOverSSH(t *testing.T) {
	tm := &fakeTmux{windows: "@7 %9 0 web.app-412\n", session: true}
	r := newRig(t, "mac", tm, webTask("devbox", t0))
	if err := Attach("web.app-412", r.deps()); err != nil {
		t.Fatal(err)
	}
	wantCall := "ssh -o BatchMode=yes -o ConnectTimeout=10 " + devboxAddr + " 'tmux' 'list-windows' '-t' '=engine:' '-F' '" + windowsFD + "'"
	if !slices.Contains(tm.calls, wantCall) {
		t.Errorf("calls %q lack %q", tm.calls, wantCall)
	}
	// ssh gets the remote command as one quoted string, so the target is not
	// expanded by the remote login shell.
	want := []string{"ssh", "-t", devboxAddr, "'tmux' 'attach' '-t' '=engine:@7'"}
	if r.exec.calls != 1 || r.exec.path != "/bin/ssh" || !slices.Equal(r.exec.argv, want) {
		t.Errorf("replaced with %s %v, want /bin/ssh %v", r.exec.path, r.exec.argv, want)
	}
}

func TestAttachRemoteIgnoresTmuxInside(t *testing.T) {
	// The remote tmux is another server: the ssh attach is never a switch.
	r := newRig(t, "mac", &fakeTmux{windows: "@7 %9 0 web.app-412\n", session: true}, webTask("devbox", t0))
	r.env["TMUX"] = "/tmp/tmux-501/default,1,0"
	if err := Attach("web.app-412", r.deps()); err != nil {
		t.Fatal(err)
	}
	if got := r.exec.argv[3]; !strings.HasPrefix(got, "'tmux' 'attach' ") {
		t.Errorf("remote command %q, want a remote attach", got)
	}
}

func TestAttachRemoteUnreachable(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{sshErr: &session.ExitError{Code: 255, Stderr: "Connection refused"}}, webTask("devbox", t0))
	err := Attach("web.app-412", r.deps())
	if err == nil || err.Error() != "ssh "+devboxAddr+": Connection refused" {
		t.Errorf("error %v, want the ssh failure", err)
	}
	if r.exec.calls != 0 {
		t.Error("replaced the process after ssh failed")
	}
}

func TestAttachRemoteWithoutTmux(t *testing.T) {
	// A missing tmux on the remote exits 127: that is not a missing session.
	r := newRig(t, "mac", &fakeTmux{sshErr: &session.ExitError{Code: 127, Stderr: "zsh: command not found: tmux"}}, webTask("devbox", t0))
	err := Attach("web.app-412", r.deps())
	if err == nil || err.Error() != "ssh "+devboxAddr+": zsh: command not found: tmux" {
		t.Errorf("error %v, want the remote shell's failure", err)
	}
	if r.exec.calls != 0 {
		t.Error("replaced the process without tmux")
	}
}

func TestAttachRemoteWithoutSession(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{session: false}, webTask("devbox", t0))
	err := Attach("web.app-412", r.deps())
	if err == nil || !strings.Contains(err.Error(), "no tmux window named web.app-412 in the session engine on devbox") {
		t.Errorf("error %v", err)
	}
}

func TestAttachUnknownName(t *testing.T) {
	tm := &fakeTmux{windows: localWindows, session: true}
	r := newRig(t, "mac", tm, webTask("mac", t0))
	err := Attach("nope-1", r.deps())
	if err == nil || err.Error() != "no claimed task named nope-1" {
		t.Errorf("error %v", err)
	}
	if r.exec.calls != 0 || len(tm.calls) != 0 {
		t.Errorf("replaced %d times, tmux calls %q; want none", r.exec.calls, tm.calls)
	}
}

func TestAttachWindowMissingOnLocalHost(t *testing.T) {
	// A session started by hand has a claim but no window in engine's session.
	r := newRig(t, "mac", &fakeTmux{windows: "@3 %5 0 boga3-412\n", session: true}, webTask("mac", t0))
	err := Attach("web.app-412", r.deps())
	if err == nil || !strings.Contains(err.Error(), "no tmux window named web.app-412 in the session engine on mac") {
		t.Errorf("error %v", err)
	}
}

func TestAttachLatestClaimWins(t *testing.T) {
	// The task ran twice: the rerun on devbox posted last, so that is the one.
	r := newRig(t, "mac", &fakeTmux{windows: "@7 %9 0 web.app-412\n", session: true},
		webTask("mac", t0), webTask("devbox", t1))
	if err := Attach("web.app-412", r.deps()); err != nil {
		t.Fatal(err)
	}
	if r.exec.path != "/bin/ssh" {
		t.Errorf("attached with %s, want the devbox claim over ssh", r.exec.path)
	}
}

func TestAttachClaimNamesNoHost(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{}, task{repo: "o/web.app", number: 4, posted: t0, body: strings.Replace(claimBody("web.app-412", "x"), "Host: x", "Host: none", 1)})
	err := Attach("web.app-412", r.deps())
	if err == nil || err.Error() != "the claim of web.app-412 names no host" {
		t.Errorf("error %v", err)
	}
}

func TestAttachHostNotInConfig(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{}, webTask("ghost", t0))
	err := Attach("web.app-412", r.deps())
	if err == nil || !strings.Contains(err.Error(), `host "ghost" of web.app-412 is not in hosts.toml`) {
		t.Errorf("error %v", err)
	}
}

func TestAttachNeedsAToken(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{}, webTask("mac", t0))
	delete(r.env, "GH_TOKEN")
	r.home = t.TempDir()
	err := Attach("web.app-412", r.deps())
	if err == nil || !strings.Contains(err.Error(), "no GH_TOKEN and no token file") {
		t.Errorf("error %v", err)
	}
}

func TestAttachNeedsTheHostFile(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{}, webTask("mac", t0))
	if err := os.Remove(filepath.Join(hosts.ConfigDir(r.home), "host")); err != nil {
		t.Fatal(err)
	}
	err := Attach("web.app-412", r.deps())
	if err == nil || !strings.Contains(err.Error(), "read host file") {
		t.Errorf("error %v", err)
	}
}

func TestAttachNeedsTmuxOnPath(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{windows: localWindows, session: true}, webTask("mac", t0))
	r.missing = "tmux"
	err := Attach("web.app-412", r.deps())
	if err == nil || !strings.Contains(err.Error(), "tmux: executable file not found") {
		t.Errorf("error %v", err)
	}
	if r.exec.calls != 0 {
		t.Error("replaced the process without tmux")
	}
}

func TestAttachReportsGhFailure(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{}, webTask("mac", t0))
	r.gh = func([]string, string, ...string) (string, error) { return "HTTP 403", errors.New("exit 1") }
	if err := Attach("web.app-412", r.deps()); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("error %v", err)
	}
}

func TestAttachReportsAReplaceFailure(t *testing.T) {
	r := newRig(t, "mac", &fakeTmux{windows: localWindows, session: true}, webTask("mac", t0))
	r.exec.err = errors.New("exec refused")
	if err := Attach("web.app-412", r.deps()); err == nil || err.Error() != "exec refused" {
		t.Errorf("error %v", err)
	}
}

func TestSystemFillsEveryDep(t *testing.T) {
	d := System(func([]string, string, ...string) (string, error) { return "", nil }, os.Getenv, "/home/x")
	if d.Gh == nil || d.Run == nil || d.Replace == nil || d.LookPath == nil || d.Getenv == nil || d.Home != "/home/x" {
		t.Errorf("System left a dep unset: %+v", d)
	}
}
