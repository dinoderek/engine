package start

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hostsTOML is a host config with two slots on mac, and a host that runs
// Codex only. Tests replace @BIN@ with the fake harness's path.
const hostsTOML = `default_harness = "claude"
gb_per_session = 8
reserve_gb = 8

[harness.claude]
max_sessions = 8
launch = "{bin} --permission-mode auto --model {model} --effort {effort} -n {name} --remote-control {name}"
tiers.deep     = { model = "opus-x",   effort = "high" }
tiers.standard = { model = "sonnet-x", effort = "high" }
tiers.light    = { model = "haiku-x",  effort = "medium" }

[harness.codex]
max_sessions = 2
launch = "{bin} -m {model} -c model_reasoning_effort={effort}"
tiers.deep     = { model = "sol", effort = "high" }
tiers.standard = { model = "sol", effort = "medium" }
tiers.light    = { model = "luna", effort = "medium" }

[host.mac]
address = "me@mac"
harnesses = ["claude", "codex"]
ram_gb = 24
capabilities = ["macos"]
bin = { claude = "@BIN@", codex = "~/bin/codex" }

[host.dev]
address = "me@dev"
harnesses = ["claude"]
ram_gb = 24
capabilities = ["linux", "docker"]
bin = { claude = "~/bin/claude" }

[host.box]
address = "me@box"
harnesses = ["codex"]
ram_gb = 24
capabilities = ["linux"]
bin = { codex = "~/bin/codex" }
`

// home is a temp home directory with the host config, the host file, the
// token file and an executable fake claude at ~/bin/claude.
type home struct {
	dir string
	bin string
}

func newHome(t *testing.T, host string) home {
	t.Helper()
	h := home{dir: t.TempDir()}
	h.bin = filepath.Join(h.dir, "bin", "claude")
	write(t, h.bin, "#!/bin/sh\nexit 0\n", 0o755)
	cfg := strings.ReplaceAll(hostsTOML, "@BIN@", "~/bin/claude")
	write(t, filepath.Join(h.dir, ".config", "engine", "hosts.toml"), cfg, 0o600)
	write(t, filepath.Join(h.dir, ".config", "engine", "host"), host+"\n", 0o600)

	return h
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func fixedNow() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
