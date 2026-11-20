// This file tells the operator's prompts from engine's. Before engine types
// into a session's pane (the kickoff, a wake, a continue) it logs the text's
// hash in the run's typed-text log; the prompt hook records every prompt the
// session submits in the run's prompt log, and a prompt engine did not type
// is the operator's.

package session

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// TypedLog is the run's typed-text log: one JSON line, {"sha256": <hash>},
// per text engine typed or launched the session with. PromptLog is the run's
// prompt log: one Prompt per line, per prompt the session submitted.
const (
	TypedLog  = "typed.jsonl"
	PromptLog = "prompts.jsonl"
)

// TextHash is the hash the typed-text log keeps of text: SHA-256, in hex, of
// the text without its leading and trailing white space, which an input box
// may drop.
func TextHash(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

// LogTyped appends hash to the typed-text log of the run in dir, which must
// exist. Text is typed only once its hash is logged, so a prompt hook never
// takes engine's text for the operator's.
func LogTyped(dir, hash string) error {
	if len(hash) != sha256.Size*2 || strings.Trim(hash, "0123456789abcdef") != "" {
		return fmt.Errorf("typed-text hash %q: want %d lowercase hex digits", hash, sha256.Size*2)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("no run directory %s", dir)
	}
	f, err := os.OpenFile(filepath.Join(dir, TypedLog), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "{\"sha256\":%q}\n", hash)
	return errors.Join(err, f.Close())
}

// Prompt is one prompt the session submitted, as the prompt log records it.
type Prompt struct {
	// Seq numbers the run's prompts from 1.
	Seq     int       `json:"seq"`
	At      time.Time `json:"at"`
	Session string    `json:"session,omitempty"`
	SHA256  string    `json:"sha256"`
	// Engine is true when engine typed it: its hash is in the typed-text log
	// more often than earlier prompts engine typed used it.
	Engine bool `json:"engine,omitempty"`
	// Foreign is true when it came from another session than the run's,
	// the session of the run's first prompt: such as a reviewer the session
	// started, which inherits the run's environment.
	Foreign bool `json:"foreign,omitempty"`
}

// Operator reports whether the operator typed p into the run's own session.
func (p Prompt) Operator() bool { return !p.Engine && !p.Foreign }

// RecordPrompt records a prompt the session with ID sessionID submitted, at
// at, in the prompt log of the run in dir, and returns its record. Each
// logged hash accounts for one prompt, so text the operator types again
// after engine typed it once is still the operator's.
func RecordPrompt(dir, sessionID, text string, at time.Time) (Prompt, error) {
	f, err := os.OpenFile(filepath.Join(dir, PromptLog), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return Prompt{}, err
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return Prompt{}, err
	}
	var prompts []Prompt
	if err := readLines(f, func(line []byte) error {
		var p Prompt
		if err := json.Unmarshal(line, &p); err != nil {
			return err
		}
		prompts = append(prompts, p)
		return nil
	}); err != nil {
		return Prompt{}, fmt.Errorf("read %s: %w", PromptLog, err)
	}
	typed, err := typedCounts(dir)
	if err != nil {
		return Prompt{}, err
	}
	p := Prompt{Seq: len(prompts) + 1, At: at.UTC(), Session: sessionID, SHA256: TextHash(text)}
	p.Foreign = len(prompts) > 0 && prompts[0].Session != "" && sessionID != prompts[0].Session
	used := 0
	for _, q := range prompts {
		if q.Engine && q.SHA256 == p.SHA256 {
			used++
		}
	}
	p.Engine = typed[p.SHA256] > used
	line, err := json.Marshal(p)
	if err != nil {
		return Prompt{}, err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return Prompt{}, err
	}
	return p, nil
}

// typedCounts counts each hash in the typed-text log of the run in dir.
func typedCounts(dir string) (map[string]int, error) {
	counts := map[string]int{}
	f, err := os.Open(filepath.Join(dir, TypedLog))
	if errors.Is(err, os.ErrNotExist) {
		return counts, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	err = readLines(f, func(line []byte) error {
		var e struct {
			SHA256 string `json:"sha256"`
		}
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		counts[e.SHA256]++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", TypedLog, err)
	}
	return counts, nil
}

// readLines calls each with every non-blank line of r.
func readLines(r io.Reader, each func([]byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		if line := sc.Bytes(); len(strings.TrimSpace(string(line))) > 0 {
			if err := each(line); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}
