package controlcmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dinoderek/engine/internal/control"
)

func TestTaskMoveParsesItsDestination(t *testing.T) {
	e := Env{In: strings.NewReader(""), Getenv: func(string) string { return "" }}
	r, err := Parse(strings.Fields("task move t4 --host mac --harness codex --yes --json --request-key k"), e)
	if err != nil || r.Op != "task-move" || r.ID != "t4" || r.Key != "k" || r.Item.Harness != "" ||
		r.Move == nil || *r.Move != (control.MoveRequest{Host: "mac", Harness: "codex", Confirm: true}) {
		t.Fatalf("parsed %+v (move %+v), %v", r, r.Move, err)
	}
	if r, err = Parse(strings.Fields("task move t4"), e); err != nil || r.Move == nil || *r.Move != (control.MoveRequest{}) {
		t.Fatalf("a bare move: %+v, %v", r, err)
	}
	for _, args := range []string{"task move", "plan move p2", "task show t4 --host mac", "task stop t4 --yes", "task move t4 extra"} {
		if _, err := Parse(strings.Fields(args), e); err == nil {
			t.Errorf("%s parsed", args)
		}
	}
	if !strings.Contains(Usage, "engine task move <task-id> [--host <host>] [--harness <harness>] [--yes]\n") {
		t.Error("the usage does not show task move")
	}
}

// moving is a CLI environment whose server last read devbox, or no host
// when unread, with task t3 running there.
func moving(t *testing.T, unread bool) (Env, string) {
	t.Helper()
	fleet := control.HostStatus{At: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC), Hosts: []control.HostRow{{Name: "devbox"}, {Name: "mac"}}}
	if unread {
		fleet.Hosts[0].Unreachable = "ssh: exit 255"
	}
	e, svc := environmentWith(t, func() control.HostStatus { return fleet })
	repo := command(t, e, "repo", "add", "--title", "Demo", "--slug", "owner/demo", "--remote", "ssh://example.test/demo").Item
	plan := command(t, e, "plan", "create", "--repo", repo.ID, "--title", "Feature", "--body", "Requirements").Item
	task := command(t, e, "task", "create", "--repo", repo.ID, "--plan", plan.ID, "--title", "Task", "--body", "## Outcome\nWorks\n").Item
	command(t, e, "plan", "approve", plan.ID, "--version", "1")
	out, err := svc.Execute(control.Actor{Name: "operator", Role: "operator"}, control.Request{Op: "claim", ID: task.ID, Body: "devbox", State: "claude", Key: "dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.SetAttempt(out.Attempt.ID, "running", "@1"); err != nil {
		t.Fatal(err)
	}
	return e, task.ID
}

func TestTaskMovePrintsTheMovedTaskAndIsSafeToRepeat(t *testing.T) {
	e, task := moving(t, false)
	out := command(t, e, "task", "move", task, "--host", "mac", "--json", "--request-key", "move-1")
	if out.Item.ID != task || out.Attempt.Reason != control.EndMoved || out.Attempt.MoveHost != "mac" || out.Attempt.MovedBlind {
		t.Fatalf("move printed %+v, attempt %+v", out.Item, out.Attempt)
	}
	again := command(t, e, "task", "move", task, "--host", "mac", "--request-key", "move-1")
	if again.Item.Version != out.Item.Version || !again.Attempt.Ended.Equal(out.Attempt.Ended) {
		t.Fatalf("a repeat moved again: %+v", again.Item)
	}
}

// Off a host the server could not read, the move warns that the old session
// may still push, and acts only with --yes or a yes at a terminal.
func TestTaskMoveOffAnUnreachableHostWarnsAndNeedsConfirming(t *testing.T) {
	e, task := moving(t, true)
	errOut := e.Err.(*bytes.Buffer)
	if code := Main([]string{"task", "move", task}, e); code != 1 || !strings.Contains(errOut.String(), "warning: host devbox was not reachable") ||
		!strings.Contains(errOut.String(), "may still be running and pushing") || !strings.Contains(errOut.String(), "rerun with --yes") {
		t.Fatalf("an unconfirmed move exited %d: %s", code, errOut)
	}
	if got := command(t, e, "task", "show", task).Item; got.State != "running" {
		t.Fatalf("an unconfirmed move changed the task: %+v", got)
	}
	terminal := e
	terminal.Terminal = func() bool { return true }
	terminal.In = strings.NewReader("n\n")
	errOut.Reset()
	if code := Main([]string{"task", "move", task}, terminal); code != 1 || !strings.Contains(errOut.String(), "Move it anyway? [y/N]") || !strings.Contains(errOut.String(), "not moved") {
		t.Fatalf("a declined move exited %d: %s", code, errOut)
	}
	terminal.In = strings.NewReader("yes\n")
	terminal.Out.(*bytes.Buffer).Reset()
	if code := Main([]string{"task", "move", task, "--request-key", "asked"}, terminal); code != 0 || !strings.Contains(terminal.Out.(*bytes.Buffer).String(), `"moved_blind": true`) {
		t.Fatalf("a confirmed move exited %d: %s %s", code, errOut, terminal.Out)
	}
	if out := command(t, e, "task", "move", task, "--yes"); !out.Attempt.MovedBlind {
		t.Fatalf("--yes on a moved task: %+v", out.Attempt)
	}
}
