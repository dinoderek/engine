package session

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var promptAt = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

func TestAPromptEngineTypedIsIgnoredAndAnyOtherIsTheOperators(t *testing.T) {
	dir := t.TempDir()
	const wake = "Read the new task updates: engine updates t7 --after 512"
	for _, text := range []string{"the kickoff", wake, wake, "continue"} {
		if err := LogTyped(dir, TextHash(text)); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		session, text string
		operator      bool
	}{
		{"s1", "the kickoff", false},
		{"s1", "Yes, drop the column", true},
		{"s1", wake + "\n", false}, // an input box may add or drop the newline
		{"s1", wake, false},
		{"s1", wake, true}, // both of engine's wakes are spent
		{"s1", "continue", false},
		{"s1", "continue", true},
		{"reviewer", "Review this diff", false}, // another session, not the operator
	}
	for i, s := range steps {
		p, err := RecordPrompt(dir, s.session, s.text, promptAt)
		if err != nil {
			t.Fatal(err)
		}
		if p.Seq != i+1 || p.Operator() != s.operator || p.SHA256 != TextHash(s.text) {
			t.Errorf("prompt %d %q: %+v, want operator %v", i+1, s.text, p, s.operator)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, PromptLog))
	if err != nil || strings.Count(string(data), "\n") != len(steps) {
		t.Errorf("prompt log %q, %v: want one line per prompt", data, err)
	}
	if strings.Contains(string(data), "drop the column") {
		t.Error("the prompt log keeps the text, not only its hash")
	}
}

func TestAPromptWithNoTypedTextLogIsTheOperators(t *testing.T) {
	p, err := RecordPrompt(t.TempDir(), "s1", "Approved", promptAt)
	if err != nil || !p.Operator() {
		t.Errorf("%+v, %v; want the operator's", p, err)
	}
}

func TestLogTypedRefusesABadHashOrAMissingRun(t *testing.T) {
	dir := t.TempDir()
	if err := LogTyped(dir, "not-a-hash"); err == nil {
		t.Error("logged a malformed hash")
	}
	if err := LogTyped(filepath.Join(dir, "gone"), TextHash("x")); err == nil {
		t.Error("logged into a run directory that does not exist")
	}
	if err := os.WriteFile(filepath.Join(dir, TypedLog), []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordPrompt(dir, "s1", "x", promptAt); err == nil {
		t.Error("read a corrupt typed-text log as empty")
	}
}

func TestThePaneKeySignsForItsPublicKey(t *testing.T) {
	dir := t.TempDir()
	pub, err := NewPaneKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, PaneKeyFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("pane key file: %v, %v; want it owner-only", info, err)
	}
	if again, err := PanePublicKey(dir); err != nil || again != pub {
		t.Errorf("public key %q, %v; want %q", again, err, pub)
	}
	sig, err := SignPane(dir, []byte("message"))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := hex.DecodeString(pub)
	raw, _ := hex.DecodeString(sig)
	if !ed25519.Verify(key, []byte("message"), raw) || ed25519.Verify(key, []byte("other"), raw) {
		t.Error("the signature does not verify for its message alone")
	}
	if _, err := SignPane(t.TempDir(), []byte("message")); err == nil {
		t.Error("signed with no pane key")
	}
	if err := os.WriteFile(filepath.Join(dir, PaneKeyFile), []byte("zz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PanePublicKey(dir); err == nil {
		t.Error("read a corrupt pane key")
	}
}
