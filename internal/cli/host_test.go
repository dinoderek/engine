package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestHostCommandLine(t *testing.T) {
	none := func(string) string { return "" }
	cases := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"usage", []string{"host"}, 2, "", "usage: engine host <op> --json"},
		{"no --json", []string{"host", "status"}, 2, "", "usage: engine host <op> --json"},
		{"a refusal is a JSON reply", []string{"host", "status", "--json"}, 1, `"refused":true`, ""},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		env := Env{In: strings.NewReader(""), Out: &out, Err: &errOut, Getenv: none, Home: t.TempDir()}
		code := Run(c.args, env)
		if code != c.code || !strings.Contains(out.String(), c.stdout) || !strings.Contains(errOut.String(), c.stderr) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", c.name, code, out.String(), errOut.String())
		}
	}
	var out bytes.Buffer
	if Run([]string{"help"}, Env{Out: &out}) != 0 || !strings.Contains(out.String(), "host <op> --json") {
		t.Errorf("the usage lacks engine host:\n%s", out.String())
	}
}
