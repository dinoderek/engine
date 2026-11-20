package cli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/session"
)

// promptRun is a server run of Claude Code whose server is a fake that
// records each request.
func promptRun(t *testing.T) (dir, transcript, pub string, requests *[]control.Request) {
	t.Helper()
	requests = new([]control.Request)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req control.Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		*requests = append(*requests, req)
		_ = json.NewEncoder(w).Encode(control.Response{Updates: []control.Update{{ID: 7}}})
	}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	dir = filepath.Join(home, filepath.FromSlash(session.RunsDir), "demo-7-a9")
	if err := session.WriteRecord(dir, session.Record{Schema: session.RecordSchema, RunID: "demo-7-a9", Harness: "claude", Mode: "hands-off"}); err != nil {
		t.Fatal(err)
	}
	conn, _ := json.Marshal(struct{ URL, Attempt string }{srv.URL, "a9"})
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("server-session.json", conn)
	write("server-token", []byte(strings.Repeat("c", 64)+"\n"))
	var err error
	if pub, err = session.NewPaneKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := session.LogTyped(dir, session.TextHash("the kickoff")); err != nil {
		t.Fatal(err)
	}
	transcript = filepath.Join(home, "transcript.jsonl")
	return dir, transcript, pub, requests
}

func TestThePromptHookPostsTheOperatorsPaneAnswer(t *testing.T) {
	dir, transcript, pub, requests := promptRun(t)
	const answer = "Yes, drop the column."
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": answer}})
	if err := os.WriteFile(transcript, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	var detached [][]string
	var env Env
	env = Env{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Home: t.TempDir(), Now: func() time.Time { return stateAt },
		Getenv: func(k string) string { return map[string]string{session.RunDirVar: dir}[k] },
		Detach: func(args ...string) error {
			detached = append(detached, args)
			bg := env
			bg.In = strings.NewReader("")
			if code := Run(args, bg); code != 0 {
				t.Errorf("background post exited %d: %s", code, env.Err)
			}
			return nil
		}}
	for _, prompt := range []string{"the kickoff", answer} {
		event, _ := json.Marshal(map[string]string{"session_id": "s1", "hook_event_name": "UserPromptSubmit", "cwd": "/w", "prompt": prompt, "transcript_path": transcript})
		env.In = bytes.NewReader(event)
		if code := Run([]string{"hook", "prompt", "--harness", "claude"}, env); code != 0 {
			t.Fatalf("%q: exit %d: %s", prompt, code, env.Err)
		}
	}
	if want := [][]string{{"hook", "prompt", "--harness", "claude", dir, "2"}}; !reflect.DeepEqual(detached, want) {
		t.Errorf("detached %q, want %q", detached, want)
	}
	if len(*requests) != 1 {
		t.Fatalf("posted %+v, want the operator's answer alone", *requests)
	}
	got := (*requests)[0]
	key, _ := hex.DecodeString(pub)
	sig, _ := hex.DecodeString(got.Pane.Signature)
	if got.Op != "pane-input" || got.Body != answer || !got.Pane.Transcript || !ed25519.Verify(key, control.PaneMessage("a9", got.Key, *got.Pane, answer), sig) {
		t.Errorf("request %+v, want the answer verbatim, signed, found in the transcript", got)
	}
}

func TestThePromptHookFailsOpen(t *testing.T) {
	dir, _, _, requests := promptRun(t)
	var errs bytes.Buffer
	env := Env{In: strings.NewReader("not json"), Out: &bytes.Buffer{}, Err: &errs, Home: t.TempDir(), Now: func() time.Time { return stateAt },
		Getenv: func(k string) string { return map[string]string{session.RunDirVar: dir}[k] }}
	if code := Run([]string{"hook", "prompt", "--harness", "claude"}, env); code != 1 {
		t.Errorf("unreadable event: exit %d, want 1, which blocks no prompt", code)
	}
	if code := Run([]string{"hook", "prompt", "--harness", "claude", dir, "x"}, env); code != 1 || !strings.Contains(errs.String(), "want <run-dir> <seq>") {
		t.Errorf("bad background call: exit %d, %q", code, errs.String())
	}
	if code := Run([]string{"hook", "prompt", "--harness", "claude", dir, "3"}, env); code != 1 {
		t.Errorf("a post of no queued prompt: exit %d, want 1", code)
	}
	env.In = strings.NewReader(`{"hook_event_name":"UserPromptSubmit","session_id":"s1","prompt":"Approved"}`)
	env.Getenv = func(string) string { return "" }
	if code := Run([]string{"hook", "prompt", "--harness", "claude"}, env); code != 0 || len(*requests) != 0 {
		t.Errorf("outside a run: exit %d, posted %+v", code, *requests)
	}
}
