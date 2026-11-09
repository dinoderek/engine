package start

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/session"
)

// These tests check a "remote" host through a fake ssh on PATH. The fake runs
// the command it is given in a shell whose HOME is the remote host's, where
// `engine` is this test binary in remote mode: it answers `engine start
// --check` and runs hostops.Main, the code `engine host` runs. So the wire
// format is the real one.

const (
	remoteModeVar = "ENGINE_START_TEST_REMOTE"
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
	d := hostops.Deps{Run: session.RunCommand, Exec: session.Exec, Home: os.Getenv("HOME"), Getenv: os.Getenv, Now: time.Now}
	return hostops.Main(args[1:], d, os.Stdin, os.Stdout, os.Stderr)
}

// fakeSSH puts an ssh and an engine on PATH, and returns the remote host's
// home. The ssh logs each call, then either fails as the mode says or runs
// the command as the remote host.
func fakeSSH(t *testing.T) (remoteHome string, log string) {
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
	t.Setenv(sshModeVar, "")
	t.Setenv(checkExitVar, "")
	return remoteHome, log
}

// sshRig is a local host that checks "dev" over the fake ssh.
type sshRig struct {
	local, remote home
	log           string
	deps          Deps
}

func newSSHRig(t *testing.T) sshRig {
	t.Helper()
	r := sshRig{local: newHome(t, "mac")}
	remoteHome, log := fakeSSH(t)
	r.remote, r.log = home{dir: remoteHome, bin: filepath.Join(remoteHome, "bin", "claude")}, log
	r.deps = Deps{Home: r.local.dir, Getenv: func(string) string { return "" }, Now: fixedNow, SSH: remote.ExecCommand}
	return r
}

func (r sshRig) sshCalls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(r.log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
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
