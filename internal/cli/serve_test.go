package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeStartsTheSystemdUnitTheHostRecords(t *testing.T) {
	env, out, errOut := setupEnv(t)
	dir := filepath.Join(env.Home, ".config", "engine")
	hostsToml := strings.Replace(setupHosts, "[host.mac]\n", "[host.mac.services]\nmanager = \"systemd\"\nserver = \"engine-server.service\"\n\n[host.mac]\n", 1)
	for name, content := range map[string]string{"hosts.toml": hostsToml, "config.toml": "[server]\nhost = 'mac'\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var ran []string
	env.SSH = func(_ io.Reader, _, _ io.Writer, name string, args ...string) (int, error) {
		ran = append(ran, strings.Join(append([]string{name}, args...), " "))
		if args[1] == "is-active" && len(ran) == 1 {
			return 3, nil
		}
		return 0, nil
	}
	env.Sleep = func(time.Duration) {}
	if code := Run([]string{"serve"}, env); code != 0 || !strings.Contains(out.String(), "engine server started on mac at http://") || !strings.Contains(out.String(), "(systemd unit engine-server.service)") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	if len(ran) != 3 || ran[1] != "systemctl --user start engine-server.service" {
		t.Fatalf("ran %q", ran)
	}
}
