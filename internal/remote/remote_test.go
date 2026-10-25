package remote

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// Each Local method runs its op in-process: with no host config, each one
// refuses as the op does.
func TestLocalRunsTheOps(t *testing.T) {
	tmux := &session.Tmux{Run: func(string, ...string) (string, error) { return "", nil }, Bin: "tmux"}
	l := Local{Deps: hostops.Deps{Home: t.TempDir(), Tmux: tmux}}
	calls := map[string]func() error{
		"sync-repo": func() error { _, err := l.SyncRepo("o"); return err },
		"spawn":     func() error { _, err := l.Spawn(hostops.SpawnRequest{Repo: "o/r"}); return err },
		"status":    func() error { _, err := l.Status(); return err },
		"collect":   func() error { _, err := l.Collect("none"); return err },
		"release":   func() error { _, err := l.Release("-x", false); return err },
		"check": func() error {
			ok, err := l.Check(&bytes.Buffer{})
			if ok {
				return errors.New("a check that cannot run passed")
			}
			return err
		},
	}
	for op, call := range calls {
		var refused *hostops.RefusedError
		if err := call(); !errors.As(err, &refused) || !strings.HasSuffix(err.Error(), "nothing was changed") {
			t.Errorf("%s: %v, want the op's refusal", op, err)
		}
	}
}
