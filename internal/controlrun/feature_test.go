package controlrun

import (
	"reflect"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/lint"
	"github.com/dinoderek/engine/internal/run"
)

func TestFeatureProjectionMatchesCoordinatorAndPreservesEdges(t *testing.T) {
	c, _, repo, plan, task := setup(t)
	external := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Repo: repo.ID, Title: "External", Body: task.Body}})
	external = execute(t, c.Service, control.Request{Op: "complete-external", ID: external.ID, Version: external.Version, Item: control.Item{PR: "https://github.com/" + repo.Slug + "/pull/" + "42"}})
	task = execute(t, c.Service, control.Request{Op: "edit", ID: task.ID, Version: task.Version, Item: control.Item{Title: task.Title, Body: task.Body, Depends: []string{external.ID}, Requires: []string{"linux"}, Harness: "claude", Tier: "light"}})
	acceptance := execute(t, c.Service, control.Request{Op: "create", Item: control.Item{Kind: "task", Repo: repo.ID, Plan: plan.ID, Title: "Acceptance", Body: task.Body, Depends: []string{task.ID}}})
	st, err := c.Service.Store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	f := Feature(st, plan.ID)
	v, err := c.view(st, plan.ID, run.Fleet{})
	if err != nil || !reflect.DeepEqual(f, v.Feature) {
		t.Fatal("different projection", err)
	}
	if len(f.Tasks) != 2 || f.Tasks[0].Ref.Number != control.Number(task.ID) || f.Tasks[1].Ref.Number != control.Number(acceptance.ID) {
		t.Fatal("wrong task selection or ordering", f.Tasks)
	}
	first := f.Tasks[0]
	if len(first.BlockedBy) != 1 || first.BlockedBy[0].Open || first.BlockedBy[0].Ref.Number != control.Number(external.ID) {
		t.Fatal("lost external completion edge", first.BlockedBy)
	}
	if !strings.Contains(first.Body, "claude light") || !reflect.DeepEqual(first.Requires, []string{"linux"}) || len(lint.Lint(f)) != 0 {
		t.Fatal("lost structured fields or acceptance edges", first)
	}
}
