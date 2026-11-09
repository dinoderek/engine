package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/harness"
)

func TestHooksReadClaudeEvents(t *testing.T) {
	h := New(harness.Env{}).Hooks()
	read := func(raw string) harness.Event {
		ev, err := h.Event([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	bash := read(events[2].Raw)
	if bash.Tool != "Bash" || bash.Command != "./dev test" || bash.SessionID != claudeSession || bash.Cwd != "/w" || bash.Mode != "auto" ||
		string(bash.Input) != `{"command":"./dev test"}` {
		t.Errorf("a shell call: %+v", bash)
	}
	if req := read(events[4].Raw); req.Tool != "Read" || req.Command != "" || string(req.Input) != `{"file_path":"/opt/x"}` {
		t.Errorf("a file read has a command: %+v", req)
	}
	if denied := read(events[5].Raw); denied.Reason != "[Irreversible Local Destruction]" || denied.Command != "rm -rf /x" {
		t.Errorf("a denial: %+v", denied)
	}
}

// An unattended Claude request is denied, in the shape Claude Code reads,
// and its hooks always have the launch's variables.
func TestHooksDenyAndNeverLookForTheirRun(t *testing.T) {
	h := New(harness.Env{}).Hooks()
	if h.Unattended() != harness.Deny || h.FindsRun() {
		t.Errorf("policy %v, finds run %v", h.Unattended(), h.FindsRun())
	}
	var out bytes.Buffer
	if err := h.Deny(&out, "no"); err != nil {
		t.Fatal(err)
	}
	if want := `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":"no"}}}` + "\n"; out.String() != want {
		t.Errorf("deny wrote %s, want %s", out.String(), want)
	}
	var errs bytes.Buffer
	h.Signalled(harness.HookContext{Run: harness.HookRun{Dir: t.TempDir(), Joined: true, Repo: "o/r"}, Err: &errs}, harness.Event{Kind: harness.TurnStopped})
	if errs.Len() != 0 {
		t.Errorf("signalled: %q", errs.String())
	}
}

// The status line the launch sets is the adapter's own entry, and both
// entries write to the run directory they are given, at the adapter's time.
func TestEntriesRecordQuotaAndLimit(t *testing.T) {
	launch := New(harness.Env{Getenv: func(string) string { return "" }}).Session("r").Args(harness.Launch{Kickoff: "k"})
	if !strings.Contains(launch, `"command":"engine hook statusline --harness claude"`) {
		t.Errorf("launch %q does not set the statusline entry", launch)
	}
	dir := t.TempDir()
	h := New(harness.Env{Now: func() time.Time { return time.Unix(1000, 0) }}).Hooks()
	entries := h.Entries()
	var out bytes.Buffer
	in := `{"session_name":"hand","rate_limits":{"five_hour":{"used_percentage":83,"resets_at":2000}}}`
	if err := entries["statusline"](harness.Call{In: strings.NewReader(in), Out: &out, RunDir: dir}); err != nil || out.String() != "hand  5h 83%\n" {
		t.Errorf("statusline: %q, %v", out.String(), err)
	}
	var q struct {
		Harness    string `json:"harness"`
		ObservedAt int64  `json:"observed_at"`
	}
	data, _ := os.ReadFile(filepath.Join(dir, "quota.json"))
	if json.Unmarshal(data, &q) != nil || q.Harness != Name || q.ObservedAt != 1000 {
		t.Errorf("quota %s", data)
	}
	stop := `{"hook_event_name":"StopFailure","error":"rate_limit","resets_at":2000}`
	if err := entries["limit"](harness.Call{In: strings.NewReader(stop), RunDir: dir}); err != nil {
		t.Fatal(err)
	}
	if l, ok := ReadLimit(dir); !ok || l.Since != 1000 || l.ResetsAt != 2000 {
		t.Errorf("limit %+v, %v", l, ok)
	}
}
