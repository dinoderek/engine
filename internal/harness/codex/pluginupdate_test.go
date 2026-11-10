package codex

import (
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hosts"
)

// The caller's plugin update upgrades engine's marketplace, then adds the
// plugin, exactly, with the binary hosts.toml pins; a host with no pinned
// binary is an error naming the missing bin.
func TestPluginUpdateCommands(t *testing.T) {
	h := hosts.Host{Name: "mac", Bin: map[string]string{Name: "~/.local/bin/codex"}}
	got, err := Adapter{}.Setup().PluginUpdate(h, boxHome)
	want := []string{boxCodex + " plugin marketplace upgrade engine", boxCodex + " plugin add engine@engine"}
	if err != nil || strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("PluginUpdate = %q, %v; want %q", got, err, want)
	}
	if got, err := (Adapter{}).Setup().PluginUpdate(hosts.Host{Name: "mac"}, boxHome); err == nil || !strings.Contains(err.Error(), "has no bin") {
		t.Fatalf("PluginUpdate without a bin = %q, %v; want the missing bin", got, err)
	}
}
