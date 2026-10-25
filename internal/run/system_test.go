package run

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/session"
	"github.com/dinoderek/engine/internal/tracker"
)

func TestSystemTaskNamesItsFeature(t *testing.T) {
	gh := func(_ []string, _ string, args ...string) (string, error) {
		switch {
		case strings.HasSuffix(args[1], "/blocked_by"):
			return `[{"number":5,"state":"open","repository_url":"https://api.github.com/repos/o/r"}]`, nil
		case strings.HasSuffix(args[1], "/7"):
			return `{"number":7,"title":"T","state":"open","repository_url":"https://api.github.com/repos/o/r",` +
				`"parent_issue_url":"https://api.github.com/repos/o/r/` + `issues/3","labels":[{"name":"engine:running"}]}`, nil
		}
		return "", errors.New("unexpected " + args[1])
	}
	c, op, err := System{Gh: gh, Token: "tok"}.Task(ref(7))
	if err != nil || op != ref(3) || !c.Open || !c.Has(Running) || len(c.BlockedBy) != 1 || c.BlockedBy[0].Ref != ref(5) {
		t.Errorf("card %+v, operator %v, %v", c, op, err)
	}
}

func TestSystemNotesAndLog(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	s := System{Home: home, Out: &out, Clock: func() time.Time { return t0 }}
	dir, err := s.NoteDir(ref(1))
	if err != nil || dir != filepath.Join(home, ".local/share/engine/runs", "r-1-coordinator") {
		t.Fatalf("note dir %q, %v", dir, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("asking for the note directory made it: %v", err)
	}
	if err := session.WriteNote(dir, LintNoted, t0); err != nil {
		t.Fatal(err)
	}
	if at, ok, err := session.ReadNote(dir, LintNoted); err != nil || !ok || !at.Equal(t0) {
		t.Errorf("the first note: %v %v %v", at, ok, err)
	}
	if _, err := s.NoteDir(tracker.Ref{Repo: "bad", Number: 1}); err == nil {
		t.Error("a bad repo: no error")
	}
	s.Logf("start %s", ref(2))
	if out.String() != "2026-10-24T13:00:00Z start "+ref(2).String()+"\n" {
		t.Errorf("log %q", out.String())
	}
}
