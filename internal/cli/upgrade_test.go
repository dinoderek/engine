package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

const upgradeHosts = setupHosts + `
[host.mac.services]
manager = "systemd"
server = "engine-server.service"
`

// upgradeEnv is a Mac that runs the server under its unit, a server whose
// tasks are tasks, and a gh that knows main's head. ran lists every command
// line and program the run started.
func upgradeEnv(t *testing.T, tasks []control.Item) (Env, *bytes.Buffer, *bytes.Buffer, *[]string) {
	t.Helper()
	env, out, errOut := setupEnv(t)
	dir := filepath.Join(env.Home, ".config", "engine")
	for name, content := range map[string]string{"hosts.toml": upgradeHosts, "config.toml": "[server]\nhost = \"mac\"\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req control.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Op != "list" || req.State != "task" {
			t.Errorf("unexpected request %+v, %v", req, err)
		}
		_ = json.NewEncoder(w).Encode(control.Response{Items: tasks})
	}))
	t.Cleanup(server.Close)
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte(control.Secret()), 0o600); err != nil {
		t.Fatal(err)
	}
	env.Getenv = func(k string) string {
		return map[string]string{"ENGINE_SERVER_URL": server.URL, "ENGINE_SERVER_TOKEN_FILE": token}[k]
	}
	env.Now = func() time.Time { return time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC) }
	sha := strings.Repeat("ab", 20)
	env.Run = func(_ string, _ []string, _ string, args ...string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "contents/go.mod") {
			return "module m\n\ntoolchain go1.27.3\n", nil
		}
		return sha + "\n", nil
	}
	var ran []string
	env.SSH = func(_ io.Reader, o, _ io.Writer, name string, args ...string) (int, error) {
		command := args[len(args)-1]
		ran = append(ran, name+" "+command)
		switch command {
		case "systemctl --user is-active engine-server.service":
			fmt.Fprintln(o, "active")
		case "engine version":
			fmt.Fprintln(o, "engine v0.0.0-20261110131219-d8c1744aa2b3 (d8c1744aa2b3, go1.27.2)")
		default:
			return 1, nil
		}
		return 0, nil
	}
	return env, out, errOut, &ran
}

// `engine upgrade --dry-run` prints the steps and runs nothing that changes
// a host: only the preflight's reads.
func TestUpgradeDryRunCommandLine(t *testing.T) {
	env, out, errOut, ran := upgradeEnv(t, []control.Item{{ID: "t1", Kind: "task", State: "done"}})
	if code := Run([]string{"upgrade", "--dry-run"}, env); code != 0 {
		t.Fatalf("exit %d; out %s; err %s", code, out, errOut)
	}
	for _, line := range *ran {
		if line != "sh systemctl --user is-active engine-server.service" && line != "sh engine version" {
			t.Errorf("dry run ran %q", line)
		}
	}
	for _, want := range []string{"engine upgrade --dry-run: nothing is changed", "target: ababab", "1. here: systemctl --user stop engine-server.service",
		"here: claude plugin marketplace update engine", "here: ~/.local/bin/engine setup\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// A busy task refuses with exit 1; bad flags exit 2 with the usage.
func TestUpgradeCommandLineRefusesAndRejectsFlags(t *testing.T) {
	env, _, errOut, ran := upgradeEnv(t, []control.Item{{ID: "t7", Kind: "task", State: "running"}})
	if code := Run([]string{"upgrade"}, env); code != 1 || !strings.Contains(errOut.String(), "engine upgrade: 1 task(s) are running") {
		t.Fatalf("exit %d; err %s", code, errOut)
	}
	if len(*ran) != 1 {
		t.Errorf("a refused upgrade ran %q", *ran)
	}
	for _, args := range [][]string{{"--dry"}, {"--dry-run", "x"}} {
		errOut.Reset()
		if code := Run(append([]string{"upgrade"}, args...), env); code != 2 || !strings.Contains(errOut.String(), upgradeUsage) {
			t.Errorf("%q: exit %d; err %s", args, code, errOut)
		}
	}
}
