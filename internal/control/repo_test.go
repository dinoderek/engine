package control

import (
	"reflect"
	"testing"
)

func TestARepositoryEditChangesOnlyItsRequires(t *testing.T) {
	s := service(t)
	repo, _ := repoPlan(t, s)
	edited := call(t, s, op, Request{Op: "edit", ID: repo.ID, Version: repo.Version, Item: Item{Requires: []string{"ios"}}}).Item
	if edited.Version != repo.Version+1 || !reflect.DeepEqual(edited.Requires, []string{"ios"}) ||
		edited.Title != repo.Title || edited.Slug != repo.Slug || edited.Remote != repo.Remote {
		t.Fatalf("edited repository: %+v", edited)
	}
	reject(t, s, op, Request{Op: "edit", ID: repo.ID, Version: repo.Version, Item: Item{Requires: []string{"vm"}}}, 409)
	for _, item := range []Item{{Title: "x"}, {Slug: "a/b"}, {Remote: "r"}, {Body: "b"}, {Tier: "deep"}, {Depends: []string{"t1"}}} {
		reject(t, s, op, Request{Op: "edit", ID: repo.ID, Version: edited.Version, Item: item}, 400)
	}
	worker := Actor{Name: "a9", Role: "worker", Attempt: "a9"}
	reject(t, s, worker, Request{Op: "edit", ID: repo.ID, Version: edited.Version, Item: Item{}}, 403)
	cleared := call(t, s, op, Request{Op: "edit", ID: repo.ID, Version: edited.Version, Item: Item{}}).Item
	if len(cleared.Requires) != 0 {
		t.Fatalf("an edit without requires kept %v", cleared.Requires)
	}
}

func TestRequiresAreTheRepositorysThenTheTasksEachOnce(t *testing.T) {
	cases := []struct{ repo, task, want []string }{
		{nil, nil, nil},
		{[]string{"ios"}, nil, []string{"ios"}},
		{nil, []string{"docker"}, []string{"docker"}},
		{[]string{"ios"}, []string{"docker", "ios"}, []string{"ios", "docker"}},
	}
	for _, c := range cases {
		if got := Requires(Item{Requires: c.repo}, Item{Requires: c.task}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Requires(%v, %v) = %v, want %v", c.repo, c.task, got, c.want)
		}
	}
}
