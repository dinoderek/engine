package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/version"
)

func TestParseSetup(t *testing.T) {
	cases := []struct {
		args []string
		want string
		err  bool
	}{
		{nil, "", false},
		{[]string{"--host", "devbox"}, "devbox", false},
		{[]string{"--host=devbox"}, "devbox", false},
		{[]string{"--host"}, "", true},
		{[]string{"--host", ""}, "", true},
		{[]string{"--host="}, "", true},
		{[]string{"devbox"}, "", true},
		{[]string{"--host", "a", "b"}, "", true},
	}
	for _, c := range cases {
		got, err := parseSetup(c.args)
		if got != c.want || (err != nil) != c.err {
			t.Errorf("%q: %q, %v; want %q, error %v", c.args, got, err, c.want, c.err)
		}
	}
}

const setupHosts = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 8
launch = "{bin} --model {model} --effort {effort} -n {name} --remote-control {name}"
tiers.deep     = { model = "o", effort = "high" }
tiers.standard = { model = "s", effort = "high" }
tiers.light    = { model = "h", effort = "medium" }

[host.mac]
address = "me@mac"
harnesses = ["claude"]
ram_gb = 24
capabilities = ["macos"]
bin = { claude = "~/bin/claude" }
`

// setupEnv is a Mac on which every command setup runs answers as if all were in
// place.
func setupEnv(t *testing.T) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	for name, content := range map[string]string{"hosts.toml": setupHosts, "host": "mac\n", "github-token": "tok\n"} {
		path := filepath.Join(home, ".config", "engine", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out, errOut bytes.Buffer
	env := Env{
		In: strings.NewReader(""), Out: &out, Err: &errOut, Home: home, Getenv: func(string) string { return "" },
		Build: version.Info{Revision: "abc1234"},
		SSH: func(_ io.Reader, o, _ io.Writer, name string, args ...string) (int, error) {
			command := args[len(args)-1]
			switch {
			case strings.HasPrefix(command, "for t in"):
				return 0, nil
			case command == "command -v engine":
				_, _ = io.WriteString(o, "/usr/local/bin/engine\n")
			case command == "claude plugin list --json":
				_, _ = io.WriteString(o, `[{"id":"engine@engine","version":"0.7.1","scope":"user"}]`)
			case command == "claude auth status":
				_, _ = io.WriteString(o, `{"loggedIn":true,"authMethod":"claude.ai"}`)
			case strings.HasPrefix(command, "uname"):
				_, _ = io.WriteString(o, "Darwin\n")
			}
			return 0, nil
		},
	}
	return env, &out, &errOut
}

func TestSetupCommandLine(t *testing.T) {
	env, _, errOut := setupEnv(t)
	if code := Run([]string{"setup", "--host", "devbox"}, env); code != 1 ||
		!strings.Contains(errOut.String(), `engine setup: host "devbox" is not in hosts.toml; its machines are mac`) {
		t.Errorf("unknown host: exit %d, stderr %q", code, errOut.String())
	}
	env, _, errOut = setupEnv(t)
	if code := Run([]string{"setup", "devbox"}, env); code != 2 || !strings.Contains(errOut.String(), "usage: engine setup [--host <name>]") {
		t.Errorf("bad usage: exit %d, stderr %q", code, errOut.String())
	}
	env, out, errOut := setupEnv(t)
	code := Run([]string{"setup"}, env)
	if !strings.Contains(out.String(), "engine setup: mac (this host, checked only)") || !strings.Contains(out.String(), "5. sign-in: ok (claude.ai)") {
		t.Errorf("a check of this host: exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	// The preflight is the real one: this home has no claude binary, so it
	// refuses, and an open item exits 1.
	if code != 1 || !strings.Contains(out.String(), "7. preflight: OPEN (") || !strings.Contains(out.String(), "is not an executable claude binary") {
		t.Errorf("the preflight did not run through engine start --check: exit %d\n%s", code, out.String())
	}
	var help bytes.Buffer
	if Run([]string{"help"}, Env{Out: &help}) != 0 || !strings.Contains(help.String(), "setup [--host <name>]") {
		t.Errorf("the usage lacks engine setup:\n%s", help.String())
	}
}
