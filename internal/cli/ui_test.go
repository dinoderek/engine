package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestUIDispatch(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"ui", "--bogus"}, 2, "usage: engine ui"},
		{[]string{"ui", "--listen", "0.0.0.0:7332"}, 2, "loopback"},
		// No operator credential under this home: the dashboard uses the CLI's.
		{[]string{"ui", "--listen", "127.0.0.1:0"}, 1, "server-token"},
	} {
		var stdout, stderr bytes.Buffer
		env := Env{Home: t.TempDir(), Out: &stdout, Err: &stderr, Getenv: func(string) string { return "" }}
		if code := Run(tc.args, env); code != tc.code || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("Run(%v) = %d, stderr %q; want %d and %q", tc.args, code, stderr.String(), tc.code, tc.want)
		}
	}
	var stdout bytes.Buffer
	if Run([]string{"help"}, Env{Out: &stdout}) != 0 || !strings.Contains(stdout.String(), "ui [--listen <addr>]") {
		t.Fatalf("usage lacks the ui command: %s", stdout.String())
	}
}
