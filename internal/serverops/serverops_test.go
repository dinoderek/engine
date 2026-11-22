package serverops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const hostsTOML = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 8
launch = "{bin} --model {model}"
tiers.deep     = { model = "opus", effort = "high" }
tiers.standard = { model = "sonnet", effort = "high" }
tiers.light    = { model = "haiku", effort = "medium" }

[host.mac]
address = "me@mac"
harnesses = ["claude"]
ram_gb = 24
bin = { claude = "~/bin/claude" }

[host.devbox]
address = "dev@devbox"
harnesses = ["claude"]
ram_gb = 24
bin = { claude = "~/bin/claude" }

[host.devbox.services]
manager = "systemd"
server = "engine-server.service"
`

const dbPath = ".local/share/engine/server/control.db"

// Each command the server's host is asked to run.
const (
	isActive   = "systemctl --user is-active engine-server.service"
	restartCmd = "systemctl --user restart engine-server.service"
	quickCheck = `timeout 30 sqlite3 -readonly "$HOME/.local/share/engine/server/control.db" 'PRAGMA quick_check;'`
	dfCmd      = `df -Pk "$HOME/.local/share/engine/server"`
	journal    = "journalctl --user -u engine-server.service -n 50 --no-pager"
)

const dfOK = "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/nvme0n1p2 479151816 351234560 103498532 78% /\n"

// answer is one command's reply.
type answer struct {
	out  string
	err  string
	code int
}

// host is a fake server host: canned replies by command, and the calls.
type host struct {
	replies map[string]answer
	ssh     []string // the address and command of each ssh
	sh      []string
	down    bool
}

func healthy() *host {
	return &host{replies: map[string]answer{
		isActive: {out: "active\n"}, restartCmd: {}, quickCheck: {out: "ok\n"}, dfCmd: {out: dfOK},
		journal: {out: "Nov 22 17:02:04 devbox engine[1]: engine server listening on 100.1.1.1:7331\n"},
	}}
}

func (h *host) exec(_ io.Reader, out, errOut io.Writer, name string, args ...string) (int, error) {
	command := args[len(args)-1]
	switch name {
	case "ssh":
		h.ssh = append(h.ssh, args[len(args)-2]+" "+command)
		if h.down {
			fmt.Fprint(errOut, "ssh: connect to host devbox port 22: No route to host")
			return 255, nil
		}
	case "sh":
		h.sh = append(h.sh, command)
	default:
		return -1, fmt.Errorf("unexpected %s", name)
	}
	a, ok := h.replies[command]
	if !ok {
		fmt.Fprintf(errOut, "unexpected command %q", command)
		return 99, nil
	}
	fmt.Fprint(out, a.out)
	fmt.Fprint(errOut, a.err)
	return a.code, nil
}

// homeOn is a home whose host file names this host.
func homeOn(t *testing.T, name string) string {
	t.Helper()
	h := t.TempDir()
	dir := filepath.Join(h, ".config", "engine")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string]string{"hosts.toml": hostsTOML, "host": name + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

type rig struct {
	home    string
	host    *host
	answers []error // each call's answer, in turn; past the end, the last
	calls   int
	slept   []time.Duration
}

func newRig(t *testing.T, on string) *rig {
	return &rig{home: homeOn(t, on), host: healthy(), answers: []error{nil}}
}

func (r *rig) main(args ...string) (int, string, string) {
	var out, errOut strings.Builder
	d := Deps{Home: r.home, Exec: r.host.exec, DBPath: dbPath,
		Sleep: func(d time.Duration) { r.slept = append(r.slept, d) },
		Answers: func() error {
			err := r.answers[min(r.calls, len(r.answers)-1)]
			r.calls++
			return err
		}}
	code := Main(args, d, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestStatusOfAHealthyServerFromAnotherHost(t *testing.T) {
	r := newRig(t, "mac")
	code, out, _ := r.main("status")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"engine server status: devbox (dev@devbox), unit engine-server.service",
		"unit: ok (active)", "api: ok (the server answers)", "database: ok (quick_check ok)",
		"disk: ok (98.7 GiB free of 457.0 GiB, 78% used)",
		"last 1 log lines:\n  Nov 22 17:02:04 devbox engine[1]: engine server listening",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	want := []string{"dev@devbox " + isActive, "dev@devbox " + quickCheck, "dev@devbox " + dfCmd, "dev@devbox " + journal}
	if !slices.Equal(r.host.ssh, want) || len(r.host.sh) != 0 {
		t.Errorf("ssh %q, sh %q; want %q", r.host.ssh, r.host.sh, want)
	}
}

func TestOnTheServersHostItRunsInSh(t *testing.T) {
	r := newRig(t, "devbox")
	if code, out, _ := r.main("restart"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(r.host.ssh) != 0 || !slices.Equal(r.host.sh, []string{restartCmd}) {
		t.Errorf("ssh %q, sh %q", r.host.ssh, r.host.sh)
	}
}

func TestStatusReportsEachFailure(t *testing.T) {
	r := newRig(t, "mac")
	r.host.replies[isActive] = answer{out: "failed\n", code: 3}
	r.answers = []error{errors.New("context deadline exceeded")}
	r.host.replies[quickCheck] = answer{out: "*** in database main ***\nPage 12: btreeInitPage() returns error code 11\n", code: 0}
	r.host.replies[dfCmd] = answer{out: "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 1000000 990000 10000 99% /\n"}
	r.host.replies[journal] = answer{err: "Failed to connect to bus", code: 1}
	code, out, _ := r.main("status")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"unit: FAILED (failed)", "api: FAILED (context deadline exceeded)",
		"database: FAILED (quick_check: *** in database main ***; Page 12: btreeInitPage() returns error code 11)",
		"disk: FAILED (0.0 GiB free of 1.0 GiB, 99% used; under 1.0 GiB free)",
		"could not read the log: Failed to connect to bus",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

func TestStatusChecksThatCannotRun(t *testing.T) {
	r := newRig(t, "mac")
	r.host.replies[quickCheck] = answer{err: "sqlite3: command not found", code: 127}
	r.host.replies[dfCmd] = answer{out: "df: unexpected\n"}
	_, out, _ := r.main("status")
	for _, want := range []string{"database: FAILED (sqlite3: command not found)", "disk: FAILED (df said: df: unexpected)"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	r.host.replies[dfCmd] = answer{err: "df: no such file", code: 1}
	if _, out, _ = r.main("status"); !strings.Contains(out, "disk: FAILED (df: no such file)") {
		t.Errorf("df failure:\n%s", out)
	}
}

func TestStatusOfAnUnreachableHostRunsNothingElse(t *testing.T) {
	r := newRig(t, "mac")
	r.host.down = true
	code, out, _ := r.main("status")
	if code != 1 || !strings.Contains(out, "reach: FAILED (") || len(r.host.ssh) != 1 || r.calls != 0 {
		t.Errorf("exit %d, ssh %q, calls %d:\n%s", code, r.host.ssh, r.calls, out)
	}
}

func TestStatusAsJSON(t *testing.T) {
	r := newRig(t, "mac")
	code, out, _ := r.main("status", "--json")
	var got report
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 {
		t.Fatalf("exit %d, %v:\n%s", code, err, out)
	}
	if got.Op != "status" || got.Host != "devbox" || !got.OK || len(got.Checks) != 4 || len(got.Log) != 1 {
		t.Errorf("report %+v", got)
	}
}

func TestRestartWaitsForTheServerToAnswer(t *testing.T) {
	r := newRig(t, "mac")
	r.answers = []error{errors.New("connection refused"), errors.New("connection refused"), nil}
	code, out, _ := r.main("restart")
	if code != 0 || !strings.Contains(out, "restart: ok (systemctl restarted engine-server.service)") || !strings.Contains(out, "api: ok (the server answers)") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !slices.Equal(r.host.ssh, []string{"dev@devbox " + restartCmd}) || len(r.slept) != 2 || r.slept[0] != time.Second {
		t.Errorf("ssh %q, slept %v", r.host.ssh, r.slept)
	}
	if strings.Contains(out, "log lines") {
		t.Errorf("restart printed the log:\n%s", out)
	}
}

func TestRestartFailures(t *testing.T) {
	r := newRig(t, "mac")
	r.host.replies[restartCmd] = answer{err: "Failed to restart engine-server.service: Unit engine-server.service not found.", code: 5}
	code, out, _ := r.main("restart")
	if code != 1 || !strings.Contains(out, "restart: FAILED (Failed to restart engine-server.service: Unit engine-server.service not found.; read its log with engine server status)") || r.calls != 0 {
		t.Errorf("exit %d, calls %d:\n%s", code, r.calls, out)
	}
	r = newRig(t, "mac")
	r.answers = []error{errors.New("connection refused")}
	code, out, _ = r.main("restart", "--json")
	var got report
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 1 || got.OK || r.calls != answerTries {
		t.Fatalf("exit %d, calls %d, %v:\n%s", code, r.calls, err, out)
	}
	if c := got.Checks[1]; c.Name != "api" || !strings.Contains(c.Detail, "no answer within 30s: connection refused") {
		t.Errorf("api check %+v", c)
	}
	r = newRig(t, "mac")
	r.host.down = true
	if code, out, _ = r.main("restart"); code != 1 || !strings.Contains(out, "restart: FAILED (") {
		t.Errorf("unreachable: exit %d:\n%s", code, out)
	}
}

func TestBadArgumentsAndConfig(t *testing.T) {
	r := newRig(t, "mac")
	for _, args := range [][]string{{}, {"start"}, {"status", "restart"}, {"--json"}} {
		if code, _, errOut := r.main(args...); code != 2 || errOut != Usage {
			t.Errorf("%q: exit %d, %q", args, code, errOut)
		}
	}
	r.home = t.TempDir()
	if code, _, errOut := r.main("status"); code != 1 || !strings.Contains(errOut, "engine server status: ") {
		t.Errorf("no config: exit %d, %q", code, errOut)
	}
	r = newRig(t, "mac")
	toml := strings.Split(hostsTOML, "[host.devbox.services]")[0]
	if err := os.WriteFile(filepath.Join(r.home, ".config", "engine", "hosts.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := r.main("restart"); code != 1 || !strings.Contains(errOut, "records a [host.<name>.services] table") {
		t.Errorf("no server host: exit %d, %q", code, errOut)
	}
}
