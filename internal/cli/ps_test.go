package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/ps"
)

const psHosts = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 8
launch = "{bin} --model {model} -n {name}"
tiers.deep     = { model = "opus", effort = "high" }
tiers.standard = { model = "sonnet", effort = "high" }
tiers.light    = { model = "haiku", effort = "medium" }

[harness.codex]
max_sessions = 2
launch = "{bin} -m {model}"
tiers.deep     = { model = "gpt", effort = "high" }
tiers.standard = { model = "gpt", effort = "medium" }
tiers.light    = { model = "gpt", effort = "medium" }

[host.mac]
address = "me@mac"
harnesses = ["claude"]
ram_gb = 48
reserve_gb = 16
capabilities = ["macos"]
bin = { claude = "~/bin/claude" }

[host.devbox]
address = "me@devbox"
harnesses = ["codex"]
ram_gb = 32
capabilities = ["linux"]
bin = { codex = "~/bin/codex" }
`

const psClaim = `Claim: r-%d
Run: r-%d-20261021T100000Z
Host: %s
Harness: claude-code 2.1.302 (claude-sonnet-5-5)
Mode: hands-off
Started by: engine start
Tmux: engine:r-%d
Remote Control: r-%d
Branch: task/r-%d`

// psEnv is an Env for `engine ps`: this host is `mac`, the tracker has two
// claimed tasks (one on this host, one on another), and tmux and claude say
// what the test's probes answer.
type psEnv struct {
	t       *testing.T
	home    string
	out     bytes.Buffer
	errOut  bytes.Buffer
	ghErr   error
	tmuxErr error
	ran     []string
	claude  string // what `claude agents --json` prints
	// status is what the host at an address prints for `engine host status`;
	// an address with no entry is unreachable (ssh exits 255).
	status map[string]string
	sshed  []string
}

func newPsEnv(t *testing.T, host string) *psEnv {
	t.Helper()
	e := &psEnv{t: t, home: t.TempDir(), claude: `[{"pid":1,"cwd":"/w","name":"r-1","status":"busy"}]`, status: map[string]string{
		"me@devbox": `{"protocol":1,"engine":"x","host":"devbox","slots":3,"free_slots":2,"windows":[{"name":"r-2","id":"@1","alive":true}],"agents":[],"runs":[]}`,
		"me@mac":    `{"protocol":1,"engine":"x","host":"mac","slots":4,"free_slots":3,"windows":[{"name":"r-1","id":"@1","alive":true}],"agents":[{"pid":1,"cwd":"/w","name":"r-1","status":"waiting"}],"runs":[]}`,
	}}
	if err := os.MkdirAll(filepath.Join(e.home, ".config", "engine"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"hosts.toml": psHosts, "host": host + "\n", "github-token": "tok\n"} {
		e.write(name, body)
	}
	return e
}

func (e *psEnv) write(name, body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.home, ".config", "engine", name), []byte(body), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *psEnv) drop(name string) {
	e.t.Helper()
	if err := os.Remove(filepath.Join(e.home, ".config", "engine", name)); err != nil {
		e.t.Fatal(err)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (e *psEnv) gh(_ []string, name string, args ...string) (string, error) {
	e.ran = append(e.ran, name+" "+strings.Join(args, " "))
	if e.ghErr != nil {
		return "HTTP 500", e.ghErr
	}
	switch {
	case args[1] == "search/issues" && strings.Contains(strings.Join(args, " "), "commenter:dinoderek-agents"):
		item := func(n int, label string) string {
			return `{"number":` + string(rune('0'+n)) + `,"title":"task ` + string(rune('0'+n)) + `","repository_url":"https://api.github.com/repos/o/r","labels":[{"name":"` + label + `"}]}`
		}
		return `{"incomplete_results":false,"items":[` + item(1, "engine:running") + `,` + item(2, "engine:in-review") + `]}`, nil
	case args[1] == "search/issues":
		return `{"incomplete_results":false,"items":[]}`, nil
	}
	n, host := 1, "mac"
	if strings.Contains(args[1], "/2/") {
		n, host = 2, "devbox"
	}
	claim := strings.ReplaceAll(strings.ReplaceAll(psClaim, "%d", string(rune('0'+n))), "%s", host)
	return `[{"body":` + jsonString(claim) + `,"html_url":"u","created_at":"2026-10-21T10:00:00Z","user":{"login":"dinoderek-agents"}}]`, nil
}

func (e *psEnv) local(name string, args ...string) (string, error) {
	e.ran = append(e.ran, name+" "+strings.Join(args, " "))
	switch {
	case name == "tmux" && args[0] == "has-session":
		return "", e.tmuxErr
	case name == "tmux":
		return "@1 %1 0 r-1\n", nil
	case name == filepath.Join(e.home, "bin", "claude"):
		return e.claude, nil
	}
	e.t.Errorf("unexpected command %s %v", name, args)
	return "", errors.New("unexpected")
}

// ssh answers `ssh ... <address> engine host status --json` with the canned
// status of the host at the address.
func (e *psEnv) ssh(_ io.Reader, out, errOut io.Writer, name string, args ...string) (int, error) {
	address, command := args[len(args)-2], args[len(args)-1]
	e.sshed = append(e.sshed, name+" "+address+" "+command)
	body, ok := e.status[address]
	if name != "ssh" || command != "'engine' 'host' 'status' '--json'" || !ok {
		fmt.Fprintln(errOut, "ssh: connect to host: no route")
		return 255, nil
	}
	fmt.Fprintln(out, body)
	return 0, nil
}

func (e *psEnv) run(args ...string) int {
	e.t.Helper()
	env := Env{
		Out: &e.out, Err: &e.errOut, Exec: e.gh, Local: e.local, SSH: e.ssh, Home: e.home,
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC) },
	}
	return Run(append([]string{"ps"}, args...), env)
}

func TestPsTable(t *testing.T) {
	e := newPsEnv(t, "mac")
	if code := e.run(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got:\n%s", e.out.String())
	}
	if got := strings.Fields(lines[1]); got[1] != "mac" || got[3] != "working" || got[4] != "2h" {
		t.Errorf("local row %q", lines[1])
	}
	if got := strings.Fields(lines[2]); got[1] != "devbox" || got[3] != "in-review" {
		t.Errorf("remote row %q", lines[2])
	}
	// Only the host that holds a claim and is not this one is asked.
	if len(e.sshed) != 1 || !strings.Contains(e.sshed[0], "me@devbox") {
		t.Errorf("ssh calls %q", e.sshed)
	}
	if e.errOut.Len() != 0 {
		t.Errorf("stderr: %s", e.errOut.String())
	}
	for _, call := range e.ran {
		if strings.Contains(call, "~") {
			t.Errorf("the binary path was not expanded: %q", call)
		}
	}
}

func TestPsJSON(t *testing.T) {
	e := newPsEnv(t, "mac")
	if code := e.run("--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	var rows []ps.Row
	if err := json.Unmarshal(e.out.Bytes(), &rows); err != nil {
		t.Fatalf("%v\n%s", err, e.out.String())
	}
	if len(rows) != 2 || rows[0].State != ps.Working || rows[0].Alive == nil || !*rows[0].Alive ||
		rows[1].State != ps.InReview || rows[1].Alive == nil || !*rows[1].Alive || rows[1].Host != "devbox" {
		t.Errorf("rows: %+v", rows)
	}
}

func TestPsSettlesAClaimOnAnotherHostFromItsStatus(t *testing.T) {
	e := newPsEnv(t, "devbox")
	if code := e.run("--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	var rows []ps.Row
	if err := json.Unmarshal(e.out.Bytes(), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("%v: %s", err, e.out.String())
	}
	if rows[0].State != ps.Waiting || rows[0].Alive == nil || !*rows[0].Alive {
		t.Errorf("row 0 is on mac, whose status says it waits: %+v", rows[0])
	}
	// The remote host runs codex only, so claude is never asked.
	for _, call := range e.ran {
		if strings.Contains(call, "bin/claude") {
			t.Errorf("claude ran: %q", call)
		}
	}
}

// A host ssh cannot reach is unknown, never dead: its rows keep their label
// state, liveness is null, and the listing still succeeds with a warning.
func TestPsUnreachableHostIsUnknown(t *testing.T) {
	e := newPsEnv(t, "mac")
	delete(e.status, "me@devbox")
	if code := e.run("--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	var rows []ps.Row
	if err := json.Unmarshal(e.out.Bytes(), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("%v: %s", err, e.out.String())
	}
	if rows[1].State != ps.InReview || rows[1].Alive != nil {
		t.Errorf("row 1 is on the unreachable devbox: %+v", rows[1])
	}
	if !strings.Contains(e.errOut.String(), "warning: host devbox: host devbox is unreachable over ssh") {
		t.Errorf("stderr: %q", e.errOut.String())
	}
}

// A claim naming a host hosts.toml does not list cannot be asked.
func TestPsHostNotInConfigIsUnknown(t *testing.T) {
	e := newPsEnv(t, "mac")
	e.write("hosts.toml", strings.Replace(psHosts, "[host.devbox]", "[host.other]", 1))
	if code := e.run("--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if !strings.Contains(e.errOut.String(), `"devbox" is not in hosts.toml`) || len(e.sshed) != 0 {
		t.Errorf("stderr %q, ssh %q", e.errOut.String(), e.sshed)
	}
}

func TestPsWarnsAndStillLists(t *testing.T) {
	e := newPsEnv(t, "mac")
	e.claude = "not json"
	e.tmuxErr = errors.New("tmux broke")
	if code := e.run(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	for _, want := range []string{"engine ps: warning: tmux: tmux broke", "engine ps: warning: ", "agents --json: unreadable output"} {
		if !strings.Contains(e.errOut.String(), want) {
			t.Errorf("stderr %q lacks %q", e.errOut.String(), want)
		}
	}
	if !strings.Contains(e.out.String(), "running") {
		t.Errorf("stdout: %s", e.out.String())
	}
}

func TestPsFailures(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*psEnv)
		args   []string
		code   int
		stderr string
	}{
		{"unknown flag", func(*psEnv) {}, []string{"--yaml"}, 2, "usage: engine ps [--json]"},
		{"extra argument", func(*psEnv) {}, []string{"--json", "x"}, 2, "usage: engine ps [--json]"},
		{"gh fails", func(e *psEnv) { e.ghErr = errors.New("exit 1") }, nil, 1, "engine ps: search for claimed tasks"},
		{"no host file", func(e *psEnv) { e.drop("host") }, nil, 1, "read host file"},
		{"bad host config", func(e *psEnv) { e.write("hosts.toml", "nope") }, nil, 1, "hosts.toml"},
		{"unknown host", func(e *psEnv) { e.write("host", "elsewhere\n") }, nil, 1, `host "elsewhere" is not in hosts.toml`},
		{"no token", func(e *psEnv) { e.drop("github-token") }, nil, 1, "no GH_TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newPsEnv(t, "mac")
			tc.setup(e)
			if code := e.run(tc.args...); code != tc.code {
				t.Errorf("exit %d, want %d", code, tc.code)
			}
			if !strings.Contains(e.errOut.String(), tc.stderr) || e.out.Len() != 0 {
				t.Errorf("stderr %q lacks %q, or stdout %q is not empty", e.errOut.String(), tc.stderr, e.out.String())
			}
		})
	}
}

func TestPsHostWithoutAClaudeBinaryWarns(t *testing.T) {
	e := newPsEnv(t, "devbox")
	// A claude-code claim on a host that runs only codex.
	if code := e.run(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errOut.String())
	}
	if !strings.Contains(e.errOut.String(), "host devbox has no claude binary in hosts.toml") {
		t.Errorf("stderr: %q", e.errOut.String())
	}
}

func TestPsUsageListsIt(t *testing.T) {
	var out bytes.Buffer
	Run([]string{"help"}, Env{Out: &out})
	if !strings.Contains(out.String(), "ps [--json]") {
		t.Errorf("usage: %s", out.String())
	}
}

func TestExpandHome(t *testing.T) {
	cases := map[string]string{"~/bin/claude": "/h/bin/claude", "~": "/h", "/usr/bin/claude": "/usr/bin/claude", "~other/x": "~other/x", "": ""}
	for in, want := range cases {
		if got := expandHome(in, "/h"); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", in, got, want)
		}
	}
}
