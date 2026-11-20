package hostops

import (
	"os"

	"github.com/dinoderek/engine/internal/session"
)

// Typed is the typed op's reply: the hash it logged.
type Typed struct {
	SHA256 string `json:"sha256"`
}

// LogTyped is the typed op: it logs hash, the session.TextHash of a text
// about to be typed into the session of the run, in the run's typed-text
// log, so the run's prompt hook knows engine typed it. A run with no
// directory on this host is refused.
func LogTyped(d Deps, runID, hash string) (Typed, error) {
	dir, err := session.RunDir(d.Home, runID)
	if err != nil {
		return Typed{}, refuse("%v", err)
	}
	if err := session.LogTyped(dir, hash); err != nil {
		return Typed{}, refuse("%v", err)
	}
	return Typed{SHA256: hash}, nil
}

// logTyped logs text in the typed-text log of the run in dir, creating the
// directory, before the text is passed as the kickoff.
func logTyped(dir, text string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return session.LogTyped(dir, session.TextHash(text))
}
