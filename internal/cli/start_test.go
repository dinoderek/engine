package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/start"
	"github.com/dinoderek/engine/internal/tracker"
)

func TestParseStart(t *testing.T) {
	task := tracker.Ref{Repo: "o/r", Number: 7}
	cases := []struct {
		name string
		args []string
		want startArgs
		err  string
	}{
		{"task only", []string{task.String()}, startArgs{Options: start.Options{Ref: task}}, ""},
		{"flags after", []string{task.String(), "--tier", "deep", "--mode", "gated"}, startArgs{Options: start.Options{Ref: task, Tier: "deep", Mode: "gated"}}, ""},
		{"flags before, with =", []string{"--mode=gated", "--tier=light", task.String()}, startArgs{Options: start.Options{Ref: task, Tier: "light", Mode: "gated"}}, ""},
		{"no task", []string{"--tier", "deep"}, startArgs{}, "no task"},
		{"two tasks", []string{task.String(), task.String()}, startArgs{}, "one task at a time"},
		{"flag without a value", []string{task.String(), "--tier"}, startArgs{}, "--tier needs a value"},
		{"empty value with =", []string{task.String(), "--mode="}, startArgs{}, "--mode= needs a value"},
		{"empty value", []string{task.String(), "--tier", ""}, startArgs{}, "--tier needs a value"},
		{"unknown flag", []string{task.String(), "--model", "x"}, startArgs{}, "unknown flag --model"},
		{"host", []string{task.String(), "--host", "devbox"}, startArgs{Options: start.Options{Ref: task, Host: "devbox"}}, ""},
		{"host with =", []string{"--host=devbox", task.String(), "--tier", "deep"}, startArgs{Options: start.Options{Ref: task, Host: "devbox", Tier: "deep"}}, ""},
		{"host without a value", []string{task.String(), "--host"}, startArgs{}, "--host needs a value"},
		{"harness", []string{task.String(), "--harness", "codex"}, startArgs{Options: start.Options{Ref: task, Harness: "codex"}}, ""},
		{"harness with =", []string{"--harness=claude", task.String(), "--tier", "deep"}, startArgs{Options: start.Options{Ref: task, Harness: "claude", Tier: "deep"}}, ""},
		{"harness without a value", []string{task.String(), "--harness"}, startArgs{}, "--harness needs a value"},
		{"check", []string{"--check"}, startArgs{Check: true}, ""},
		{"check of a harness", []string{"--check", "--harness", "codex"}, startArgs{Options: start.Options{Harness: "codex"}, Check: true}, ""},
		{"check of a harness on a host", []string{"--harness=codex", "--check", "--host", "devbox"}, startArgs{Options: start.Options{Harness: "codex", Host: "devbox"}, Check: true}, ""},
		{"check on a host", []string{"--check", "--host", "devbox"}, startArgs{Options: start.Options{Host: "devbox"}, Check: true}, ""},
		{"host before check", []string{"--host=devbox", "--check"}, startArgs{Options: start.Options{Host: "devbox"}, Check: true}, ""},
		{"check with a task", []string{"--check", task.String()}, startArgs{}, "--check takes no task"},
		{"check with a tier", []string{"--check", "--tier", "deep"}, startArgs{}, "--check takes no task, --tier or --mode"},
		{"check with a mode", []string{"--mode=gated", "--check"}, startArgs{}, "--check takes no task"},
		{"bad task", []string{"o/r"}, startArgs{}, "want owner/repo#n"},
	}
	for _, c := range cases {
		got, err := parseStart(c.args)
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
	task := tracker.Ref{Repo: "o/r", Number: 1}.String()
	cases := []struct {
		name   string
		args   []string
		code   int
		stderr string
	}{
		{"usage", []string{"start"}, 2, "usage: engine start <owner/repo#n>"},
		{"refusal", []string{"start", task, "--mode", "yolo"}, 1, `engine start: unknown mode "yolo": want hands-off or gated; nothing was changed`},
		{"no token", []string{"start", task}, 1, "engine start: the session reads the machine user's token from"},
		{"usage names --check", []string{"start"}, 2, "engine start --check [--harness <name>] [--host <name>]"},
		{"usage names --harness", []string{"start", task, "--bogus"}, 2, "[--harness <name>]"},
		{"--harness parses, then the token is checked", []string{"start", task, "--harness", "codex"}, 1, "engine start: the session reads the machine user's token from"},
		{"--harness needs a value", []string{"start", task, "--harness"}, 2, "--harness needs a value"},
		{"check --harness parses, then the token is checked", []string{"start", "--check", "--harness", "codex", "--host", "devbox"}, 1, "engine start --check: read host config"},
		{"check --harness codex, no token", []string{"start", "--check", "--harness=codex"}, 1, "engine start --check: the session reads the machine user's token from"},
		{"usage names --host", []string{"start", task, "--bogus"}, 2, "[--host <name>]"},
		{"--host parses, then the token is checked", []string{"start", task, "--host", "nowhere"}, 1, "engine start: the session reads the machine user's token from"},
		{"check with a task", []string{"start", "--check", task}, 2, "--check takes no task"},
		{"check on a host, no host config", []string{"start", "--check", "--host", "devbox"}, 1, "engine start --check: read host config"},
		{"check, no token", []string{"start", "--check"}, 1, "engine start --check: the session reads the machine user's token from"},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		env := Env{Out: &out, Err: &errOut, Getenv: none, Home: t.TempDir()}
		if code := Run(c.args, env); code != c.code || !strings.Contains(errOut.String(), c.stderr) {
			t.Errorf("%s: exit %d, stderr %q; want %d and %q", c.name, code, errOut.String(), c.code, c.stderr)
		}
	}
}
