package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRunCommandLine(t *testing.T) {
	token := func(k string) string { return map[string]string{"GH_TOKEN": "tok"}[k] }
	none := func(string) string { return "" }
	cases := []struct {
		name   string
		args   []string
		getenv func(string) string
		code   int
		stderr string
	}{
		{"usage", []string{"run"}, token, 2, "usage: engine run"},
		{"bad ref", []string{"run", "nope"}, token, 2, "want owner/repo#n"},
		{"no token", []string{"run", feature}, none, 1, "engine run: no GH_TOKEN"},
		{"no host config", []string{"run", feature}, token, 1, "engine run:"},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		env := Env{Out: &out, Err: &errOut, Getenv: c.getenv, Home: t.TempDir()}
		if code := runRun(c.args[1:], env, func(time.Duration) { t.Error("slept") }); code != c.code || !strings.Contains(errOut.String(), c.stderr) {
			t.Errorf("%s: exit %d, stderr %q", c.name, code, errOut.String())
		}
	}
}
