package controlcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

func TestTaskStopAndResumeParseAsTaskOps(t *testing.T) {
	e := Env{In: strings.NewReader(""), Getenv: func(string) string { return "" }}
	for args, want := range map[string]string{"task stop t4": "task-stop", "task resume t4": "task-resume", "plan stop p2": "stop"} {
		r, err := Parse(strings.Fields(args), e)
		if err != nil || r.Op != want || r.ID != strings.Fields(args)[2] {
			t.Errorf("%s: %+v, %v", args, r, err)
		}
	}
	for _, args := range []string{"task stop", "task resume t4 extra", "repo stop r1", "plan resume p2"} {
		if _, err := Parse(strings.Fields(args), e); err == nil {
			t.Errorf("%s parsed", args)
		}
	}
	if !strings.Contains(Usage, "engine task stop|resume <task-id>\n") {
		t.Error("the usage does not show task stop and resume")
	}
}

func TestTaskStopAndResumeAreOperatorOnly(t *testing.T) {
	e, svc := environment(t)
	repo := command(t, e, "repo", "add", "--title", "Demo", "--slug", "owner/demo", "--remote", "ssh://example.test/demo").Item
	plan := command(t, e, "plan", "create", "--repo", repo.ID, "--title", "Feature", "--body", "Requirements").Item
	task := command(t, e, "task", "create", "--repo", repo.ID, "--plan", plan.ID, "--title", "Task", "--body", "## Outcome\nWorks\n").Item
	command(t, e, "plan", "approve", plan.ID, "--version", "1")
	// An idle task has no execution to stop.
	if code := Main([]string{"task", "stop", task.ID}, e); code != 1 || !strings.Contains(e.Err.(*bytes.Buffer).String(), "no running execution to stop") {
		t.Fatalf("stopping an idle task exited %d: %s", code, e.Err)
	}
	operator := control.Actor{Name: "operator", Role: "operator"}
	out, err := svc.Execute(operator, control.Request{Op: "claim", ID: task.ID, Body: "devbox", State: "claude", Key: "dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	worker := e
	worker.Client = &control.Client{URL: e.Client.URL, Token: out.Credential}
	worker.Out, worker.Err = &bytes.Buffer{}, &bytes.Buffer{}
	for _, verb := range []string{"stop", "resume"} {
		if code := Main([]string{"task", verb, task.ID}, worker); code != 1 || !strings.Contains(worker.Err.(*bytes.Buffer).String(), "requires the operator") {
			t.Errorf("a worker's task %s exited %d: %s", verb, code, worker.Err)
		}
	}
	stopped := command(t, e, "task", "stop", task.ID)
	if stopped.Item.ID != task.ID || stopped.Item.State != "todo" || !stopped.Item.Held || stopped.Attempt.Reason != control.EndStopped {
		t.Fatalf("stop printed %+v, attempt %+v", stopped.Item, stopped.Attempt)
	}
	if got := command(t, e, "task", "show", task.ID).Item; !got.Held {
		t.Fatalf("show lost the hold: %+v", got)
	}
	resumed := command(t, e, "task", "resume", task.ID)
	if resumed.Item.Held || resumed.Item.State != "todo" {
		t.Fatalf("resume printed %+v", resumed.Item)
	}
	if code := Main([]string{"task", "resume", task.ID}, e); code != 1 || !strings.Contains(e.Err.(*bytes.Buffer).String(), "is not held") {
		t.Fatalf("resuming a task that is not held exited %d: %s", code, e.Err)
	}
}
