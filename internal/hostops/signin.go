package hostops

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/session"
)

// readSignIns lists the harnesses this host runs whose own CLI says they are
// signed out: a session started with one stops at its first prompt. A check
// that cannot run, or whose answer is unreadable, says nothing, so the
// harness is not listed.
func (s *Status) readSignIns(d Deps, host hosts.Host) {
	if d.Exec == nil {
		return
	}
	if host.Runs(Claude) {
		if bin, err := host.BinPath(Claude, d.Home); err == nil && claudeSignedOut(d.Exec(bin, "auth", "status")) {
			s.SignedOut = append(s.SignedOut, Claude)
		}
	}
	if host.Runs(Codex) {
		// The Codex home engine's sessions use; setup links the operator's
		// login into it.
		if bin, err := host.BinPath(Codex, d.Home); err == nil && codexSignedOut(d.Exec("env", "CODEX_HOME="+(codex{d: d}).engineHome(), bin, "login", "status")) {
			s.SignedOut = append(s.SignedOut, Codex)
		}
	}
}

// claudeSignedOut reads `claude auth status`, which prints its JSON and exits
// non-zero when signed out.
func claudeSignedOut(out string, err error) bool {
	var exit *session.ExitError
	if err != nil && !errors.As(err, &exit) {
		return false
	}
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if json.Unmarshal([]byte(out), &status) != nil || status.LoggedIn == nil {
		return false
	}
	return !*status.LoggedIn
}

// codexSignedOut reads `codex login status`, which exits non-zero and says
// "Not logged in" when signed out.
func codexSignedOut(out string, err error) bool {
	var exit *session.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	return strings.Contains(strings.ToLower(out+exit.Stderr), "not logged in")
}
