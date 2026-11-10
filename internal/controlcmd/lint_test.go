package controlcmd

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestServerLintBeforeApprovalIsReadOnly(t *testing.T) {
	for _, tt := range []struct {
		name, body, want string
		tasks, code      int
	}{
		{"valid", "## Outcome\nWorks\n## Tests\nProve it", "the plan passes, 1 tasks", 1, 0},
		{"invalid", "## Outcome\nWorks", "no ## Tests section", 1, 1},
		{"empty", "", "the feature has no tasks", 0, 1},
		{"no-acceptance", "## Outcome\nWorks\n## Tests\nProve it", "no acceptance task", 2, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e, svc := environment(t)
			repo := command(t, e, "repo", "add", "--title", "Demo", "--slug", "owner/demo", "--remote", "remote").Item
			plan := command(t, e, "plan", "create", "--repo", repo.ID, "--title", "Feature").Item
			for range tt.tasks {
				command(t, e, "task", "create", "--repo", repo.ID, "--plan", plan.ID, "--title", "Task", "--body", tt.body)
			}
			before, err := svc.Store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			e.Out.(*bytes.Buffer).Reset()
			if code := Main([]string{"lint", plan.ID}, e); code != tt.code || !strings.Contains(e.Out.(*bytes.Buffer).String(), tt.want) {
				t.Fatalf("lint: %d, out %s, err %s", code, e.Out, e.Err)
			}
			after, err := svc.Store.Snapshot()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("lint changed state", err)
			}
		})
	}
}

func TestServerLintErrors(t *testing.T) {
	e, _ := environment(t)
	for _, args := range [][]string{nil, {"t1"}, {"p0"}, {"pX"}, {"p1", "extra"}} {
		if code := Main(append([]string{"lint"}, args...), e); code != 2 {
			t.Fatal(args, code)
		}
	}
	if code := Main([]string{"lint", "p999"}, e); code != 1 {
		t.Fatal(code)
	}
	e.Client = nil
	if code := Main([]string{"lint", "p1"}, e); code != 1 {
		t.Fatal(code)
	}
}

func TestExternalCompletionCLI(t *testing.T) {
	e, _ := environment(t)
	repo := command(t, e, "repo", "add", "--title", "Demo", "--slug", "owner/demo", "--remote", "remote").Item
	plan := command(t, e, "plan", "create", "--repo", repo.ID, "--title", "Feature").Item
	task := command(t, e, "task", "create", "--repo", repo.ID, "--plan", plan.ID, "--title", "Done elsewhere").Item
	pr := "https://github.com/" + repo.Slug + "/pull/" + itoa(42)
	done := command(t, e, "task", "complete-external", task.ID, "--version", "1", "--pr", pr).Item
	if done.State != "done" || done.PR != pr || done.Attempt != "" {
		t.Fatal(done)
	}
	if _, err := Parse([]string{"plan", "complete-external", plan.ID, "--version", "1", "--pr", pr}, e); err == nil {
		t.Fatal("accepted plan operation")
	}
	// Ordinary workflow transitions are not loosened by this command.
	task = command(t, e, "task", "create", "--repo", repo.ID, "--plan", plan.ID, "--title", "Still todo").Item
	if code := Main([]string{"task", "state", task.ID, "--version", "1", "--state", "done"}, e); code != 1 {
		t.Fatal(code)
	}
	if code := Main([]string{"task", "complete-external", task.ID, "--pr", pr}, e); code != 1 {
		t.Fatal("accepted unversioned completion")
	}
}
