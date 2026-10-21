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
		want start.Options
		err  string
	}{
		{"task only", []string{task.String()}, start.Options{Ref: task}, ""},
		{"flags after", []string{task.String(), "--tier", "deep", "--mode", "gated"}, start.Options{Ref: task, Tier: "deep", Mode: "gated"}, ""},
		{"flags before, with =", []string{"--mode=gated", "--tier=light", task.String()}, start.Options{Ref: task, Tier: "light", Mode: "gated"}, ""},
		{"no task", []string{"--tier", "deep"}, start.Options{}, "no task"},
		{"two tasks", []string{task.String(), task.String()}, start.Options{}, "one task at a time"},
		{"flag without a value", []string{task.String(), "--tier"}, start.Options{}, "--tier needs a value"},
		{"unknown flag", []string{task.String(), "--model", "x"}, start.Options{}, "unknown flag --model"},
		{"bad task", []string{"o/r"}, start.Options{}, "want owner/repo#n"},
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
		{"no token", []string{"start", task}, 1, "engine start: no GH_TOKEN and no token file"},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		env := Env{Out: &out, Err: &errOut, Getenv: none, Home: t.TempDir()}
		if code := Run(c.args, env); code != c.code || !strings.Contains(errOut.String(), c.stderr) {
			t.Errorf("%s: exit %d, stderr %q; want %d and %q", c.name, code, errOut.String(), c.code, c.stderr)
		}
	}
}
