package registry

import (
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
)

// The registry knows both harnesses, and a request that names none launches
// Claude.
func TestRegistry(t *testing.T) {
	set := New(harness.Env{Home: t.TempDir()})
	if got := strings.Join(set.Names(), ","); got != "claude,codex" {
		t.Errorf("names %s", got)
	}
	if a, ok := set.Get(""); !ok || a.Name() != "claude" {
		t.Errorf("default %v, %v", a, ok)
	}
}
