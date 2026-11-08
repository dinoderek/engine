package controlcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

func environment(t *testing.T) (Env, control.Service) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 absent")
	}
	st, err := control.Open(filepath.Join(t.TempDir(), "db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := control.Service{Store: st}
	token := control.Secret()
	server := httptest.NewServer(control.Handler(svc, token, t.Logf))
	t.Cleanup(server.Close)
	return Env{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Home: t.TempDir(), Getenv: func(string) string { return "" }, Client: &control.Client{URL: server.URL, Token: token}}, svc
}

func command(t *testing.T, e Env, args ...string) control.Response {
	t.Helper()
	e.Out.(*bytes.Buffer).Reset()
	e.Err.(*bytes.Buffer).Reset()
	if code := Main(args, e); code != 0 {
		t.Fatalf("%v: %d: %s", args, code, e.Err)
	}
	var out control.Response
	if err := json.Unmarshal(e.Out.(*bytes.Buffer).Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCLIPlanTasksAndOperatorMessages(t *testing.T) {
	e, _ := environment(t)
	repo := command(t, e, "repo", "add", "--title", "Demo", "--slug", "owner/demo", "--remote", "ssh://example.test/demo").Item
	plan := command(t, e, "plan", "create", "--repo", repo.ID, "--title", "Feature", "--body", "Requirements").Item
	task := command(t, e, "task", "create", "--repo", repo.ID, "--plan", plan.ID, "--title", "Task", "--body", "## Outcome\nWorks\n## Tests\nProve it").Item
	command(t, e, "plan", "approve", plan.ID, "--version", "1")
	first := command(t, e, "run", plan.ID, "--request-key", "start")
	retry := command(t, e, "run", plan.ID, "--request-key", "start")
	if first.Item.Version != retry.Item.Version {
		t.Fatal("CLI intent retry changed version")
	}
	command(t, e, "plan", "stop", plan.ID)
	if command(t, e, "task", "show", task.ID).Item.Title != "Task" {
		t.Fatal("show")
	}
	if len(command(t, e, "task", "list", plan.ID).Items) != 1 {
		t.Fatal("list")
	}
	msg := command(t, e, "tell", task.ID, "Please inspect this", "--operator").Updates[0]
	command(t, e, "reply", itoa(msg.ID), "Investigating")
	command(t, e, "ack", itoa(msg.ID))
	if len(command(t, e, "inbox", "--operator").Updates) != 0 {
		t.Fatal("inbox")
	}
	if len(command(t, e, "updates", task.ID, "--after", itoa(msg.ID)).Updates) != 2 {
		t.Fatal("cursor")
	}
	command(t, e, "attempt", "list", task.ID)
	other := command(t, e, "plan", "create", "--repo", repo.ID, "--title", "Other", "--body", "Other").Item
	question := command(t, e, "ask", plan.ID, "May", "tasks", "change", "the", "API?").Updates[0]
	if code := Main([]string{"resolve", itoa(question.ID), "Approved", "--plan", other.ID}, e); code != 1 || !strings.Contains(e.Err.(*bytes.Buffer).String(), "not in plan") {
		t.Fatalf("resolve into another plan exited %d: %s", code, e.Err)
	}
	ruled := command(t, e, "resolve", itoa(question.ID), "Approved", "for", "the", "plan", "--plan", plan.ID).Updates
	if len(ruled) != 1 || ruled[0].Kind != "resolved" || ruled[0].Body != "Approved for the plan" {
		t.Fatalf("resolve --plan = %+v, want the plan's resolution alone", ruled)
	}
}

func itoa(n int) string { return fmt.Sprint(n) }

func TestCLIWorkerLockAndGuard(t *testing.T) {
	e, svc := environment(t)
	repo := command(t, e, "repo", "add", "--title", "Demo", "--slug", "x/y", "--remote", "remote").Item
	task := command(t, e, "task", "create", "--repo", repo.ID, "--title", "Standalone").Item
	task = command(t, e, "task", "approve", task.ID, "--version", "1").Item
	task = command(t, e, "run", task.ID).Item
	at, err := svc.Dispatch(*task, "devbox", "claude", "dispatch")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	e.Getenv = func(k string) string {
		switch k {
		case "ENGINE_RUN_DIR":
			return dir
		case "ENGINE_ATTEMPT_ID":
			return at.Attempt.ID
		}
		return ""
	}
	e.Client.Token = at.Credential
	lock := command(t, e, "lock", "acquire").Lock
	if err = CheckMerge(e); err != nil {
		t.Fatal(err)
	}
	command(t, e, "lock", "release")
	if err = CheckMerge(e); err == nil {
		t.Fatal("guard accepted released lock")
	}
	if err = os.WriteFile(filepath.Join(dir, "merge-lock.json"), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = CheckMerge(e); err == nil {
		t.Fatal("guard accepted corrupt lock")
	}
	path := filepath.Join(t.TempDir(), "lock-token")
	if err = os.WriteFile(path, []byte(lock.Token), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = Parse([]string{"lock", "check", "--lock-token-file", path}, e); err != nil {
		t.Fatal(err)
	}
}

func TestCLIParsingFilesAndFailures(t *testing.T) {
	e, _ := environment(t)
	e.In = strings.NewReader("card from stdin")
	r, err := Parse([]string{"task", "create", "--repo", "r1", "--title", "Task", "--body-file", "-", "--depends", "t2,t3", "--requires", "linux,docker"}, e)
	if err != nil || r.Item.Body != "card from stdin" || len(r.Item.Depends) != 2 || len(r.Item.Requires) != 2 {
		t.Fatal(r, err)
	}
	e.In = strings.NewReader("message")
	r, err = Parse([]string{"tell", "t3", "-"}, e)
	if err != nil || r.Body != "message" {
		t.Fatal(r, err)
	}
	for _, args := range [][]string{
		{"unknown"}, {"task"}, {"attempt", "bad"}, {"task", "bad"}, {"task", "show"},
		{"reply", "bad", "text"}, {"reply"}, {"task", "show", "t1", "unexpected"},
		{"task", "create", "--bad"}, {"task", "create", "--body-file", "/missing"},
		{"lock", "check", "--lock-token-file", "/missing"},
	} {
		if _, err = Parse(args, e); err == nil {
			t.Fatal(args)
		}
	}
	if code := Main(nil, e); code != 2 {
		t.Fatal(code)
	}
	if code := Main([]string{"nonsense"}, e); code != 2 {
		t.Fatal(code)
	}
	if code := Main([]string{"task", "show", "missing"}, e); code != 1 {
		t.Fatal(code)
	}
	e.Client = nil
	if code := Main([]string{"inbox"}, e); code != 1 {
		t.Fatal(code)
	}
}

func TestServeCancellationAndCredentialValidation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }), func(ctx context.Context) { close(started); <-ctx.Done() })
	}()
	<-started
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	e, _ := environment(t)
	if code := Main([]string{"serve", "--foreground", "--token-file", "/missing", "--db", filepath.Join(t.TempDir(), "db")}, e); code != 1 {
		t.Fatal(code)
	}
	if code := Main([]string{"serve", "--bad"}, e); code != 2 {
		t.Fatal(code)
	}
}
