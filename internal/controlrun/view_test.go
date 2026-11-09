package controlrun

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/lint"
	"github.com/dinoderek/engine/internal/reconcile"
)

// The plan's View is its tasks by number, with their structured fields and
// every dependency edge, and lint reads the same tasks.
func TestViewCarriesThePlansTasksAndEdges(t *testing.T) {
	c, _, repo, plan, task := setup(t)
	external := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Repo: repo.ID, Title: "External", Body: task.Body}})
	external = execute(t, c.Service, control.Request{Op: "complete-external", ID: external.ID, Version: external.Version, Item: control.Item{PR: "https://github.com/" + repo.Slug + "/pull/" + "42"}})
	task = execute(t, c.Service, control.Request{Op: "edit", ID: task.ID, Version: task.Version, Item: control.Item{Title: task.Title, Body: task.Body, Depends: []string{external.ID}, Requires: []string{"linux"}, Harness: "claude", Tier: "light"}})
	acceptance := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Repo: repo.ID, Plan: plan.ID, Title: "Acceptance", Body: task.Body, Depends: []string{task.ID}}})
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	v := c.view(st, plan.ID, reconcile.Fleet{})
	if v.Root.ID != plan.ID || len(v.Tasks) != 2 || v.Tasks[0].ID != task.ID || v.Tasks[1].ID != acceptance.ID {
		t.Fatal("wrong task selection or ordering", v.Tasks)
	}
	if !reflect.DeepEqual(Tasks(st, plan.ID), []control.Item{v.Tasks[0].Item, v.Tasks[1].Item}) {
		t.Fatal("lint and coordination read different tasks")
	}
	first := v.Tasks[0]
	if !slices.Equal(first.Depends, []string{external.ID}) || len(first.Blocked) != 0 || !slices.Equal(v.Tasks[1].Blocked, []string{task.ID}) {
		t.Fatal("lost a dependency edge", first.Depends, first.Blocked, v.Tasks[1].Blocked)
	}
	if first.Harness != "claude" || first.Tier != "light" || !slices.Equal(first.Requires, []string{"linux"}) || len(v.Lint) != 0 || len(lint.Items(Tasks(st, plan.ID))) != 0 {
		t.Fatal("lost structured fields or acceptance edges", first.Item, v.Lint)
	}
	if first.Name != fmt.Sprintf("demo-%d", control.Number(task.ID)) || first.Session != nil {
		t.Fatal("session name, or a session without an attempt", first.Name, first.Session)
	}
}

// A standalone task's View is the task as its root and its only task.
func TestAStandaloneTaskIsItsOwnRootAndTask(t *testing.T) {
	c, _, repo, _, _ := setup(t)
	alone := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Title: "Alone", Repo: repo.ID, Body: "## Outcome\nWorks"}})
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	v := c.view(st, alone.ID, reconcile.Fleet{})
	if v.Root.ID != alone.ID || len(v.Tasks) != 1 || v.Tasks[0].ID != alone.ID || len(v.Lint) != 0 {
		t.Fatalf("view %+v", v)
	}
}
