package start

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// These tests start sessions on a "remote" host through a fake ssh on PATH.
// The fake runs the command it is given in a shell whose HOME is the remote
// host's, where `engine` is this test binary in remote mode: it runs
// hostops.Main, the code `engine host` runs, against a private tmux server,
// real git and a fake claude. So the wire format is the real one.

const (
	remoteModeVar = "ENGINE_START_TEST_REMOTE"
	socketVar     = "ENGINE_START_TEST_SOCKET"
	baseURLVar    = "ENGINE_START_TEST_BASEURL"
	// The fake ssh and the fake `engine start --check` read these.
	sshModeVar    = "ENGINE_START_TEST_SSH_MODE"
	checkExitVar  = "ENGINE_START_TEST_CHECK_EXIT"
	sshLogVar     = "ENGINE_START_TEST_SSH_LOG"
	remoteHomeVar = "ENGINE_START_TEST_REMOTE_HOME"
)

func TestMain(m *testing.M) {
	if os.Getenv(remoteModeVar) == "1" {
		os.Exit(runAsRemoteEngine(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// runAsRemoteEngine is the `engine` of the remote host.
func runAsRemoteEngine(args []string) int {
	if len(args) >= 2 && args[0] == "start" && args[1] == "--check" && (len(args) == 2 || len(args) == 4 && args[2] == "--harness") {
		fmt.Println("the remote check report")
		if os.Getenv(checkExitVar) == "1" {
			return 1
		}
		return 0
	}
	if len(args) == 0 || args[0] != "host" {
		fmt.Fprintln(os.Stderr, "engine: unknown command")
		return 2
	}
	bin, _ := exec.LookPath("tmux")
	tmux := &session.Tmux{Bin: bin, Socket: os.Getenv(socketVar), Run: func(name string, args ...string) (string, error) {
		return session.Exec(name, append([]string{args[0], args[1], "-f", "/dev/null"}, args[2:]...)...)
	}}
	d := hostops.Deps{Run: realGit, Exec: session.Exec, Tmux: tmux, Home: os.Getenv("HOME"), Getenv: os.Getenv,
		Now: time.Now, BaseURL: os.Getenv(baseURLVar)}
	return hostops.Main(args[1:], d, os.Stdin, os.Stdout, os.Stderr)
}

// fakeSSH puts an ssh and an engine on PATH, and returns the remote host's
// home. The ssh logs each call, then either fails as the mode says or runs
// the command as the remote host.
func fakeSSH(t *testing.T, tm *session.Tmux, baseURL string) (remoteHome string, log string) {
	t.Helper()
	remoteHome = newHome(t, "dev").dir
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(bin, "engine"), "#!/bin/sh\nexec '"+self+"' \"$@\"\n", 0o755)
	write(t, filepath.Join(bin, "ssh"), `#!/bin/sh
while [ "$1" = "-o" ]; do shift 2; done
address=$1; shift
printf '%s %s\n' "$address" "$*" >> "$`+sshLogVar+`"
case "$`+sshModeVar+`" in
down) echo "ssh: connect to host dev port 22: No route to host" >&2; exit 255 ;;
oldproto) echo '{"protocol":2,"engine":"old","host":"dev"}'; exit 0 ;;
esac
exec env HOME="$`+remoteHomeVar+`" `+remoteModeVar+`=1 sh -c "$*"
`, 0o755)
	log = filepath.Join(t.TempDir(), "ssh.log")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(sshLogVar, log)
	t.Setenv(remoteHomeVar, remoteHome)
	t.Setenv(socketVar, tm.Socket)
	t.Setenv(baseURLVar, baseURL)
	t.Setenv(sshModeVar, "")
	t.Setenv(checkExitVar, "")
	return remoteHome, log
}

// remoteClaude is a fake claude that lists no agents and otherwise waits.
func remoteClaude(t *testing.T, path string) {
	t.Helper()
	write(t, path, "#!/bin/sh\n[ \"$1\" = agents ] && { echo '[]'; exit 0; }\nexec sleep 60\n", 0o755)
}

// sshRig is a local host that starts on "dev" over the fake ssh.
type sshRig struct {
	local, remote home
	tmux          *session.Tmux
	log           string
	deps          Deps
}

func newSSHRig(t *testing.T) sshRig {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	r := sshRig{tmux: privateTmux(t), local: newHome(t, "mac")}
	remoteHome, log := fakeSSH(t, r.tmux, "file://"+bareRemote(t))
	r.remote, r.log = home{dir: remoteHome, bin: filepath.Join(remoteHome, "bin", "claude")}, log
	remoteClaude(t, r.remote.bin)
	gh := &ghFake{issue: issue{number: 7, state: "open", parent: 3, body: "## Requires\nlinux\n\n" + card("light")}}
	r.deps = Deps{GH: gh.run, Run: gitFake(t), Tmux: &session.Tmux{Run: (&tmuxFake{}).run, Bin: "tmux"}, Home: r.local.dir,
		Getenv: func(string) string { return "" }, Now: fixedNow, SSH: remote.ExecCommand}
	return r
}

func (r sshRig) sshCalls(t *testing.T) []string {
	t.Helper()
	return strings.Split(strings.TrimSpace(readFile(r.log)), "\n")
}

func TestStartOverSSH(t *testing.T) {
	r := newSSHRig(t)
	var out bytes.Buffer
	if err := Run(Options{Ref: ref(7), Host: "dev"}, r.deps, &out); err != nil {
		t.Fatal(err)
	}
	windows, err := r.tmux.Windows()
	if err != nil || len(windows) != 1 || windows[0].Name != "r-7" {
		t.Fatalf("windows on the remote host = %+v, %v", windows, err)
	}
	runDir := filepath.Join(r.remote.dir, ".local/share/engine/runs/r-7-20260102T030405Z")
	var rec session.Record
	if err := json.Unmarshal([]byte(readFile(filepath.Join(runDir, "run.json"))), &rec); err != nil {
		t.Fatalf("run.json on the remote host: %v", err)
	}
	if rec.Host != "dev" || rec.Model != "haiku-x" || rec.Feature != ref(3).String() || rec.Tmux != "engine:"+windows[0].ID {
		t.Errorf("run.json = %+v", rec)
	}
	if files := files(t, r.local.dir); strings.Contains(strings.Join(files, " "), "runs/") {
		t.Errorf("the local host got a run directory: %v", files)
	}
	for _, want := range []string{"Started " + ref(7).String() + " as r-7 on dev.", "or: ssh -t me@dev 'tmux attach -t '\\''=engine:" + windows[0].ID} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	calls := r.sshCalls(t)
	for i, op := range []string{"status", "sync-repo", "spawn"} {
		if i >= len(calls) || !strings.HasPrefix(calls[i], "me@dev 'engine' 'host' '"+op+"' '--json'") {
			t.Errorf("ssh call %d = %q, want %s", i, calls, op)
		}
	}

	// A second start reads the window from the remote status and refuses.
	err = Run(Options{Ref: ref(7), Host: "dev"}, r.deps, &out)
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "host dev: the tmux session engine already has a window r-7") {
		t.Errorf("second start: %v", err)
	}
}

func TestStartOverSSHRefusedByTheRemote(t *testing.T) {
	r := newSSHRig(t)
	if err := os.Chmod(r.remote.bin, 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(Options{Ref: ref(7), Host: "dev"}, r.deps, &bytes.Buffer{})
	var refused *hostops.RefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "is not an executable claude binary") ||
		strings.Count(err.Error(), "nothing was changed") != 1 || !strings.HasSuffix(err.Error(), "; nothing was changed") {
		t.Errorf("err = %v, want the remote's refusal, once", err)
	}
	if windows, _ := r.tmux.Windows(); len(windows) != 0 {
		t.Errorf("a refused start left windows: %+v", windows)
	}
}

func TestStartOverSSHProtocolMismatch(t *testing.T) {
	r := newSSHRig(t)
	t.Setenv(sshModeVar, "oldproto")
	err := Run(Options{Ref: ref(7), Host: "dev"}, r.deps, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "host dev speaks protocol 2 (engine old), this engine speaks 3; run: engine setup --host dev") {
		t.Errorf("err = %v", err)
	}
	if got := r.sshCalls(t); len(got) != 1 {
		t.Errorf("a mismatch went on: %q", got)
	}
}

func TestStartOverSSHUnreachable(t *testing.T) {
	r := newSSHRig(t)
	t.Setenv(sshModeVar, "down")
	err := Run(Options{Ref: ref(7), Host: "dev"}, r.deps, &bytes.Buffer{})
	var unreachable *remote.UnreachableError
	var refused *RefusedError
	if !errors.As(err, &unreachable) || errors.As(err, &refused) || !strings.Contains(err.Error(), "host dev is unreachable over ssh (me@dev): ssh: connect to host dev port 22: No route to host") {
		t.Errorf("err = %v, want an unreachable host", err)
	}
	if windows, _ := r.tmux.Windows(); len(windows) != 0 {
		t.Errorf("an unreachable host started a session: %+v", windows)
	}
}

// A check of another harness goes to the host as `engine start --check
// --harness <name>`.
func TestCheckOverSSHNamesTheHarness(t *testing.T) {
	r := newSSHRig(t)
	t.Setenv(checkExitVar, "0")
	if ok, err := Check(r.deps, "dev", "codex", &bytes.Buffer{}); !ok || err != nil {
		t.Errorf("a codex check: ok=%v err=%v", ok, err)
	}
	if calls := r.sshCalls(t); len(calls) != 2 || calls[1] != "me@dev 'engine' 'start' '--check' '--harness' 'codex'" {
		t.Errorf("ssh calls = %q", calls)
	}
}

func TestCheckOverSSHKeepsTheExitCode(t *testing.T) {
	r := newSSHRig(t)
	for _, tc := range []struct {
		exit string
		want bool
	}{{"0", true}, {"1", false}} {
		t.Setenv(checkExitVar, tc.exit)
		var out bytes.Buffer
		ok, err := Check(r.deps, "dev", "", &out)
		if err != nil || ok != tc.want || out.String() != "the remote check report\n" {
			t.Errorf("exit %s: ok=%v err=%v out=%q", tc.exit, ok, err, out.String())
		}
	}
	calls := r.sshCalls(t)
	if len(calls) != 4 || calls[1] != "me@dev 'engine' 'start' '--check'" || !strings.Contains(calls[0], "'host' 'protocol' '--json'") {
		t.Errorf("ssh calls = %q", calls)
	}
	t.Setenv(sshModeVar, "oldproto")
	if ok, err := Check(r.deps, "dev", "", &bytes.Buffer{}); ok || err == nil || !strings.Contains(err.Error(), "engine setup --host dev") {
		t.Errorf("protocol mismatch: ok=%v err=%v", ok, err)
	}
	t.Setenv(sshModeVar, "down")
	if ok, err := Check(r.deps, "dev", "", &bytes.Buffer{}); ok || err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("unreachable: ok=%v err=%v", ok, err)
	}
}
