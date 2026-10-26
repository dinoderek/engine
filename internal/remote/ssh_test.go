package remote

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hostops"
)

// sshFake answers ssh calls in order, and records each one.
type sshFake struct {
	replies []reply
	calls   []call
}

type reply struct {
	stdout, stderr string
	code           int
	err            error
}

type call struct {
	args  []string
	stdin string
}

func (f *sshFake) exec(in io.Reader, out, errOut io.Writer, name string, args ...string) (int, error) {
	c := call{args: append([]string{name}, args...)}
	if in != nil {
		data, _ := io.ReadAll(in)
		c.stdin = string(data)
	}
	f.calls = append(f.calls, c)
	r := f.replies[0]
	if len(f.replies) > 1 {
		f.replies = f.replies[1:]
	}
	_, _ = io.WriteString(out, r.stdout)
	_, _ = io.WriteString(errOut, r.stderr)
	return r.code, r.err
}

func (f *sshFake) ssh(address string) SSH {
	return SSH{Name: "devbox", Address: address, Exec: f.exec}
}

const envelope = `"protocol":3,"engine":"v1","host":"devbox"`

// line is the remote command of the n'th call.
func (f *sshFake) line(n int) string { return f.calls[n].args[len(f.calls[n].args)-1] }

func TestSSHCallsTheOpsWithTheirFlags(t *testing.T) {
	f := &sshFake{replies: []reply{{stdout: `{"clone":"/c","default_branch":"main",` + envelope + "}\n"}}}
	got, err := f.ssh("me@box").SyncRepo("o/r")
	if err != nil || got.Clone != "/c" || got.DefaultBranch != "main" {
		t.Fatalf("got %+v, %v", got, err)
	}
	want := []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "me@box", `'engine' 'host' 'sync-repo' '--json' '--repo' 'o/r'`}
	if strings.Join(f.calls[0].args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv %q, want %q", f.calls[0].args, want)
	}
}

func TestSSHSpawnPassesTheKickoffOnStdin(t *testing.T) {
	f := &sshFake{replies: []reply{{stdout: `{"name":"r-7","window":"@4","run_dir":"/runs/x","worktree":"/wt","branch":"b","attach":"tmux attach",` + envelope + "}"}}}
	req := hostops.SpawnRequest{Repo: "o/r", Task: 7, Feature: "o/r" + "#" + "3", Mode: "hands-off", Harness: "codex", Tier: "deep",
		Model: "opus", Effort: "high", RunID: "r-7-1", Kickoff: "it's $HOME\nsecond line", Heavy: true}
	got, err := f.ssh("me@box").Spawn(req)
	if err != nil || got.Window != "@4" || got.RunDir != "/runs/x" || got.Branch != "b" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if want := `'engine' 'host' 'spawn' '--json' '--repo' 'o/r' '--task' '7' '--mode' 'hands-off' '--model' 'opus' '--effort' 'high' '--run-id' 'r-7-1' '--harness' 'codex' '--tier' 'deep' '--feature' 'o/r` + "#" + `3' '--heavy'`; f.line(0) != want {
		t.Errorf("remote command\n got %s\nwant %s", f.line(0), want)
	}
	if f.calls[0].stdin != req.Kickoff {
		t.Errorf("stdin %q", f.calls[0].stdin)
	}
	f = &sshFake{replies: []reply{{stdout: `{"name":"r-7",` + envelope + "}"}}}
	if _, err := f.ssh("me@box").Spawn(hostops.SpawnRequest{Repo: "o/r", Task: 7, Kickoff: "k"}); err != nil || strings.Contains(f.line(0), "--harness") || strings.Contains(f.line(0), "--tier") || strings.Contains(f.line(0), "--feature") || strings.Contains(f.line(0), "--heavy") {
		t.Errorf("optional flags: %v, %s", err, f.line(0))
	}
}

func TestSSHStatusCollectRelease(t *testing.T) {
	f := &sshFake{replies: []reply{
		{stdout: `{"slots":2,"free_slots":1,"windows":[{"name":"r-7","id":"@4","alive":true,"harness":"codex","state":"waiting"}],"agents":[],"runs":[],` + envelope + "}"},
		{stdout: `{"run_dir":"/runs/x","run":null,"files":["a"],` + envelope + "}"},
		{stdout: `{"name":"r-7","window_closed":true,"worktree_kept":false,` + envelope + "}"},
		{stdout: `{"name":"r-7","window_closed":false,"worktree_kept":true,` + envelope + "}"},
	}}
	c := f.ssh("me@box")
	st, err := c.Status()
	if err != nil || st.FreeSlots != 1 || len(st.Windows) != 1 || st.Windows[0] != (hostops.Window{Name: "r-7", ID: "@4", Alive: true, Harness: "codex", State: "waiting"}) {
		t.Errorf("status %+v, %v", st, err)
	}
	col, err := c.Collect("r-7-1")
	if err != nil || col.RunDir != "/runs/x" || len(col.Files) != 1 {
		t.Errorf("collect %+v, %v", col, err)
	}
	rel, err := c.Release("r-7", false)
	if err != nil || !rel.WindowClosed {
		t.Errorf("release %+v, %v", rel, err)
	}
	if rel, err = c.Release("r-7", true); err != nil || !rel.WorktreeKept {
		t.Errorf("failed release %+v, %v", rel, err)
	}
	want := []string{`'engine' 'host' 'status' '--json'`, `'engine' 'host' 'collect' '--json' '--run-id' 'r-7-1'`,
		`'engine' 'host' 'release' '--json' '--name' 'r-7'`, `'engine' 'host' 'release' '--json' '--name' 'r-7' '--failed'`}
	for i, w := range want {
		if f.line(i) != w {
			t.Errorf("call %d: %s, want %s", i, f.line(i), w)
		}
	}
}

// A host still on protocol 2 has no heavy slots: its status is refused,
// and the operator is told how to bring it up to date.
func TestSSHRefusesAProtocol2Status(t *testing.T) {
	f := &sshFake{replies: []reply{{stdout: `{"protocol":2,"engine":"v0","host":"devbox","slots":2,"free_slots":2,"windows":[],"agents":[],"runs":[]}`}}}
	st, err := f.ssh("me@box").Status()
	if err == nil || !strings.Contains(err.Error(), "speaks protocol 2 (engine v0), this engine speaks 3; run: engine setup --host devbox") || st.Slots != 0 {
		t.Errorf("got %+v, %v", st, err)
	}
}

func TestSSHMapsRemoteFailures(t *testing.T) {
	cases := []struct {
		name    string
		reply   reply
		want    string
		refused bool
	}{
		{"refusal", reply{stdout: `{"error":"the window r-7 exists; nothing was changed","refused":true,` + envelope + "}", code: 1},
			"the window r-7 exists", true},
		{"failure", reply{stdout: `{"error":"tmux is down","refused":false,` + envelope + "}", code: 1}, "host devbox: tmux is down", false},
		{"exit 1 without an error", reply{stdout: `{"slots":1,` + envelope + "}", code: 1}, "exited 1 with no error", false},
		{"protocol mismatch", reply{stdout: `{"protocol":2,"engine":"v9","host":"devbox"}`}, "speaks protocol 2 (engine v9), this engine speaks 3; run: engine setup --host devbox", false},
		{"protocol mismatch on a failure", reply{stdout: `{"error":"x","refused":true,"protocol":2,"engine":"v9","host":"devbox"}`, code: 1}, "speaks protocol 2", false},
		{"no protocol", reply{stdout: `{"clone":"/c"}`}, "speaks protocol 0", false},
		{"engine missing", reply{stderr: "sh: engine: command not found\n", code: 127}, "did not reply (sh: engine: command not found); is engine current there? run: engine setup --host devbox", false},
		{"old engine", reply{stderr: "engine: unknown command \"host\"", code: 2}, "run: engine setup --host devbox", false},
		{"unreadable reply", reply{stdout: `{"clone":7,` + envelope + "}"}, "unreadable reply from engine v1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &sshFake{replies: []reply{tc.reply}}
			_, err := f.ssh("me@box").SyncRepo("o/r")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			var refused *hostops.RefusedError
			if errors.As(err, &refused) != tc.refused {
				t.Errorf("refusal = %v, want %v (%v)", !tc.refused, tc.refused, err)
			}
			if tc.refused && !strings.HasSuffix(err.Error(), "; nothing was changed") || strings.Count(err.Error(), "nothing was changed") > 1 {
				t.Errorf("suffix: %q", err)
			}
		})
	}
}

func TestSSHUnreachable(t *testing.T) {
	cases := map[string]reply{
		"ssh: connect to host box port 22: No route to host": {stderr: "ssh: connect to host box port 22: No route to host\n", code: 255},
		"exit status 255": {code: 255},
	}
	for want, r := range cases {
		f := &sshFake{replies: []reply{r}}
		_, err := f.ssh("me@box").Status()
		var unreachable *UnreachableError
		if !errors.As(err, &unreachable) || unreachable.Host != "devbox" || unreachable.Address != "me@box" || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want an unreachable host with %q", err, want)
		}
		var refused *hostops.RefusedError
		if errors.As(err, &refused) {
			t.Errorf("an unreachable host was read as a refusal: %v", err)
		}
	}
}

func TestSSHCannotRunSSH(t *testing.T) {
	f := &sshFake{replies: []reply{{err: errors.New("ssh: executable file not found in $PATH"), code: -1}}}
	if _, err := f.ssh("me@box").Status(); err == nil || !strings.Contains(err.Error(), "executable file not found") {
		t.Errorf("got %v", err)
	}
}

// Check probes the protocol first, then streams the remote check, standard
// error too, and keeps its exit code.
func TestSSHCheck(t *testing.T) {
	probe := reply{stdout: `{"error":"unknown op \"protocol\"; nothing was changed","refused":true,` + envelope + "}", code: 1}
	for _, tc := range []struct {
		code int
		want bool
	}{{0, true}, {1, false}} {
		f := &sshFake{replies: []reply{probe, {stdout: "ok   closed\n", stderr: "warn\n", code: tc.code}}}
		var out strings.Builder
		ok, err := f.ssh("me@box").Check("", &out)
		if err != nil || ok != tc.want || out.String() != "ok   closed\nwarn\n" {
			t.Errorf("exit %d: ok=%v err=%v out=%q", tc.code, ok, err, out.String())
		}
		if got, want := f.line(1), `'engine' 'start' '--check'`; got != want || f.calls[1].args[len(f.calls[1].args)-2] != "me@box" {
			t.Errorf("check call %q", f.calls[1].args)
		}
	}
}

// A harness other than the default goes to the remote check as --harness.
func TestSSHCheckNamesTheHarness(t *testing.T) {
	probe := reply{stdout: `{"error":"x","refused":true,` + envelope + "}", code: 1}
	f := &sshFake{replies: []reply{probe, {stdout: "ok\n"}}}
	if ok, err := f.ssh("me@box").Check("codex", io.Discard); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got, want := f.line(1), `'engine' 'start' '--check' '--harness' 'codex'`; got != want {
		t.Errorf("check call %q, want %q", got, want)
	}
}

func TestSSHCheckFailures(t *testing.T) {
	probe := reply{stdout: `{"error":"x","refused":true,` + envelope + "}", code: 1}
	stale := reply{stdout: `{"protocol":2,"engine":"v9","host":"devbox"}`, code: 1}
	cases := []struct {
		name    string
		replies []reply
		want    string
	}{
		{"unreachable", []reply{{code: 255, stderr: "no route"}}, "unreachable over ssh (me@box): no route"},
		{"protocol", []reply{stale}, "run: engine setup --host devbox"},
		{"ssh drops during the check", []reply{probe, {code: 255}}, "unreachable over ssh"},
		{"engine missing", []reply{probe, {code: 127}}, "engine start --check exited 127"},
		{"ssh missing", []reply{probe, {code: -1, err: errors.New("no ssh")}}, "no ssh"},
	}
	for _, tc := range cases {
		f := &sshFake{replies: tc.replies}
		ok, err := f.ssh("me@box").Check("", io.Discard)
		if ok || err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: ok=%v err=%v, want %q", tc.name, ok, err, tc.want)
		}
	}
}
