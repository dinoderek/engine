package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

// probeEnv is upgradeEnv with a server that answers the probe: a list of
// repositories.
func probeEnv(t *testing.T) (Env, *strings.Builder, *strings.Builder, *[]string) {
	t.Helper()
	env, _, _, ran := upgradeEnv(t, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req control.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Op != "list" || req.State != "repo" {
			t.Errorf("unexpected request %+v, %v", req, err)
		}
		_ = json.NewEncoder(w).Encode(control.Response{})
	}))
	t.Cleanup(server.Close)
	getenv := env.Getenv
	env.Getenv = func(k string) string {
		if k == "ENGINE_SERVER_URL" {
			return server.URL
		}
		return getenv(k)
	}
	var out, errOut strings.Builder
	env.Out, env.Err = &out, &errOut
	return env, &out, &errOut, ran
}

// `engine server restart` on the server's own host restarts its unit in sh
// and asks the server, through the operator's client, until it answers.
func TestServerRestartCommandLine(t *testing.T) {
	env, out, errOut, ran := probeEnv(t)
	env.SSH = func(_ io.Reader, o, _ io.Writer, name string, args ...string) (int, error) {
		*ran = append(*ran, name+" "+args[len(args)-1])
		return 0, nil
	}
	if code := Run([]string{"server", "restart"}, env); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	if len(*ran) != 1 || (*ran)[0] != "sh systemctl --user restart engine-server.service" {
		t.Errorf("ran %q", *ran)
	}
	if !strings.Contains(out.String(), "api: ok (the server answers)") {
		t.Errorf("out:\n%s", out)
	}
}

func TestServerAndWatchUsage(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"server"}, "usage: engine server status|restart [--json]"},
		{[]string{"server", "stop"}, "usage: engine server status|restart [--json]"},
		{[]string{"watch"}, "usage: engine watch --chat <id>"},
	} {
		env, _, errOut := setupEnv(t)
		if code := Run(tc.args, env); code != 2 || !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%q: exit %d, %q", tc.args, code, errOut)
		}
	}
	env, out, _ := setupEnv(t)
	Run([]string{"help"}, env)
	for _, want := range []string{"server status|restart [--json]", "watch --chat <id>"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}

// answers is one call through the operator's client; without a credential
// it fails.
func TestAnswersCallsTheServer(t *testing.T) {
	env, _, _, _ := probeEnv(t)
	if err := answers(env); err != nil {
		t.Fatalf("a server that answers: %v", err)
	}
	env.Getenv = func(string) string { return "" }
	env.Home = t.TempDir()
	if err := answers(env); err == nil {
		t.Error("no credential answered")
	}
}

// `engine watch` starts from the operator's client and the token copy; with
// no token copy it cannot start.
func TestWatchCommandLineNeedsTheTokenCopy(t *testing.T) {
	env, _, errOut, _ := probeEnv(t)
	code := Run([]string{"watch", "--chat", "42"}, env)
	if code != 1 || !strings.Contains(errOut.String(), "the bot token copy") {
		t.Errorf("exit %d, %q", code, errOut)
	}
}
