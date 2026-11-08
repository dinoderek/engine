package hosts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const serverHostsTOML = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 1
launch = "{bin} --model {model} --effort {effort} -n {name}"
tiers.deep     = { model = "m", effort = "high" }
tiers.standard = { model = "m", effort = "high" }
tiers.light    = { model = "m", effort = "medium" }

[host.mac]
address = "me@mac.example.ts.net"
harnesses = ["claude"]
ram_gb = 16
bin = { claude = "~/bin/claude" }
`

func TestServerOrigin(t *testing.T) {
	for _, tc := range []struct {
		name, config, want, failure string
		noHosts                     bool
	}{
		{name: "no config", want: ""},
		{name: "url", config: "[server]\nurl = 'https://engine.example'\n", want: "https://engine.example"},
		{name: "host", config: "[server]\nhost = 'mac'\n", want: "http://mac.example.ts.net:7331"},
		{name: "both", config: "[server]\nhost = 'mac'\nurl = 'http://x:1'\n", failure: "exactly one of host and url"},
		{name: "neither", config: "[server]\n", failure: "exactly one of host and url"},
		{name: "no server table", config: "x = 'y'\n", failure: "missing server"},
		{name: "unknown host", config: "[server]\nhost = 'devbox'\n", failure: `server host "devbox" is not in hosts.toml`},
		{name: "missing hosts.toml", config: "[server]\nhost = 'mac'\n", noHosts: true, failure: "hosts.toml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.config != "" {
				writeFile(t, filepath.Join(dir, "config.toml"), tc.config)
			}
			if !tc.noHosts {
				writeFile(t, filepath.Join(dir, "hosts.toml"), serverHostsTOML)
			}
			got, err := ServerOrigin(dir)
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("error = %v, want %q", err, tc.failure)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("origin = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestServerAddressUsesTheHostsName(t *testing.T) {
	for address, want := range map[string]string{"me@mac.example.ts.net": "mac.example.ts.net:7331", "devbox": "devbox:7331"} {
		listen, origin := ServerAddress(Host{Address: address})
		if listen != want || origin != "http://"+want {
			t.Fatalf("%s: listen %q, origin %q", address, listen, origin)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
