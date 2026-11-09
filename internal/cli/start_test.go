package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseCheck(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want checkArgs
		err  string
	}{
		{"check", []string{"--check"}, checkArgs{}, ""},
		{"of a harness", []string{"--check", "--harness", "codex"}, checkArgs{Harness: "codex"}, ""},
		{"of a harness on a host", []string{"--harness=codex", "--check", "--host", "devbox"}, checkArgs{Harness: "codex", Host: "devbox"}, ""},
		{"on a host", []string{"--check", "--host", "devbox"}, checkArgs{Host: "devbox"}, ""},
		{"host before check", []string{"--host=devbox", "--check"}, checkArgs{Host: "devbox"}, ""},
		{"no --check", []string{"--host", "devbox"}, checkArgs{}, "only --check is left"},
		{"a task", []string{"--check", "t7"}, checkArgs{}, "engine start takes no task, got t7"},
		{"a tier", []string{"--check", "--tier", "deep"}, checkArgs{}, "unknown flag --tier"},
		{"a mode", []string{"--mode=gated", "--check"}, checkArgs{}, "unknown flag --mode=gated"},
		{"flag without a value", []string{"--check", "--harness"}, checkArgs{}, "--harness needs a value"},
		{"empty value with =", []string{"--check", "--host="}, checkArgs{}, "--host= needs a value"},
		{"empty value", []string{"--check", "--host", ""}, checkArgs{}, "--host needs a value"},
	}
	for _, c := range cases {
		got, err := parseCheck(c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: err %v, want %q", c.name, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
}

func TestCheckCode(t *testing.T) {
	if c := checkCode(true); c != 0 {
		t.Errorf("a clean check exits %d", c)
	}
	if c := checkCode(false); c != 1 {
		t.Errorf("a check where a session would stop exits %d", c)
	}
}

func TestStartCommandLine(t *testing.T) {
	none := func(string) string { return "" }
	cases := []struct {
		name   string
		args   []string
		code   int
		stderr string
	}{
		{"usage names --check", []string{"start"}, 2, "engine start --check [--harness <name>] [--host <name>]"},
		{"usage names the server form", []string{"start", "t7"}, 2, "engine run <task-id>"},
		{"a bad flag", []string{"start", "--check", "--bogus"}, 2, "unknown flag --bogus"},
		{"--harness needs a value", []string{"start", "--check", "--harness"}, 2, "--harness needs a value"},
		{"--harness parses, then the host config is checked", []string{"start", "--check", "--harness", "codex", "--host", "devbox"}, 1, "engine start --check: read host config"},
		{"--harness=codex, no host config", []string{"start", "--check", "--harness=codex"}, 1, "engine start --check: read host config"},
		{"on a host, no host config", []string{"start", "--check", "--host", "devbox"}, 1, "engine start --check: read host config"},
		{"no host config", []string{"start", "--check"}, 1, "engine start --check: read host config"},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		env := Env{Out: &out, Err: &errOut, Getenv: none, Home: t.TempDir()}
		if code := Run(c.args, env); code != c.code || !strings.Contains(errOut.String(), c.stderr) {
			t.Errorf("%s: exit %d, stderr %q; want %d and %q", c.name, code, errOut.String(), c.code, c.stderr)
		}
	}
}
