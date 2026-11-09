package ui

import (
	"reflect"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

func fixtureRepos() []control.Item {
	return []control.Item{
		{ID: "r9", Kind: "repo", Slug: "acme/web", Title: "Web"},
		{ID: "r1", Kind: "repo", Slug: "dinoderek/engine", Title: "Engine"},
	}
}

func fixturePlans() []control.Item {
	return []control.Item{
		{ID: "p24", Kind: "plan", Repo: "r1", Title: "Control server rollout", State: "planned", Status: "executing"},
		{ID: "p3", Kind: "plan", Repo: "r9", Title: "Checkout redesign", State: "prepare"},
		{ID: "p10", Kind: "plan", Repo: "r1", Title: "Dashboard", State: "done"},
		{ID: "p11", Kind: "plan", Repo: "r9", Title: "Engine-powered search", State: "planned", Status: "executing"},
	}
}

func ids(rows []PlanRow) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func TestPlanListFilters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter Filter
		want   []string
	}{
		{name: "everything, by number", want: []string{"p3", "p10", "p11", "p24"}},
		{name: "repo", filter: Filter{Repo: "r9"}, want: []string{"p3", "p11"}},
		{name: "state", filter: Filter{State: "executing"}, want: []string{"p11", "p24"}},
		{name: "state filters on the status, not the stored state", filter: Filter{State: "planned"}, want: []string{}},
		{name: "repo and state combine", filter: Filter{Repo: "r1", State: "executing"}, want: []string{"p24"}},
		{name: "title, case-insensitive", filter: Filter{Query: "ROLLOUT"}, want: []string{"p24"}},
		{name: "repo slug", filter: Filter{Query: "acme/"}, want: []string{"p3", "p11"}},
		{name: "plan ID", filter: Filter{Query: " P10 "}, want: []string{"p10"}},
		{name: "query matches title and slug", filter: Filter{Query: "engine"}, want: []string{"p10", "p11", "p24"}},
		{name: "query with filters", filter: Filter{Repo: "r9", State: "executing", Query: "engine"}, want: []string{"p11"}},
		{name: "zero results", filter: Filter{Repo: "r1", Query: "checkout"}, want: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := planList(fixtureRepos(), fixturePlans(), tc.filter)
			if !reflect.DeepEqual(ids(got.Plans), tc.want) {
				t.Fatalf("plans = %v, want %v", ids(got.Plans), tc.want)
			}
			if got.Total != 4 {
				t.Fatalf("total = %d, want every loaded plan", got.Total)
			}
		})
	}
}

func TestPlanListFacets(t *testing.T) {
	got := planList(fixtureRepos(), fixturePlans(), Filter{})
	if want := []string{"done", "executing", "prepare"}; !reflect.DeepEqual(got.States, want) {
		t.Fatalf("states = %v, want %v", got.States, want)
	}
	if want := []Repo{{ID: "r1", Slug: "dinoderek/engine", Title: "Engine"}, {ID: "r9", Slug: "acme/web", Title: "Web"}}; !reflect.DeepEqual(got.Repos, want) {
		t.Fatalf("repos = %v, want %v", got.Repos, want)
	}
	if got.Plans[0].Slug != "acme/web" {
		t.Fatalf("plan row = %+v, want its repository slug", got.Plans[0])
	}
	empty := planList(nil, nil, Filter{})
	if empty.Plans == nil || empty.Repos == nil || empty.States == nil || empty.Total != 0 {
		t.Fatalf("empty list = %+v, want empty arrays", empty)
	}
}

func TestGroup(t *testing.T) {
	for _, tc := range []struct {
		state                            string
		active, waiting, held, unplanned bool
		want                             string
	}{
		{state: "done", want: groupDone},
		{state: "done", waiting: true, want: groupDone},
		{state: "failed", want: groupBlocked},
		{state: "needs-human", active: true, want: groupBlocked},
		{state: "running", want: groupInProgress},
		{state: "in-review", want: groupInProgress},
		{state: "todo", active: true, want: groupInProgress},
		{state: "running", waiting: true, want: groupInProgress},
		{state: "todo", waiting: true, want: groupBlocked},
		{state: "todo", want: groupReady},
		{state: "approved", want: groupReady},
		{state: "prepare", want: groupBlocked},
		{state: "todo", unplanned: true, want: groupBlocked},
		{state: "running", unplanned: true, want: groupInProgress},
		{state: "todo", held: true, want: groupBlocked},
		{state: "approved", held: true, want: groupBlocked},
		{state: "running", held: true, want: groupInProgress},
		{state: "done", held: true, want: groupDone},
	} {
		if got := group(tc.state, tc.active, tc.waiting, tc.held, !tc.unplanned); got != tc.want {
			t.Errorf("group(%q, active %t, waiting %t, held %t, approved %t) = %q, want %q", tc.state, tc.active, tc.waiting, tc.held, !tc.unplanned, got, tc.want)
		}
	}
}

// fixtureView is a plan view: the plan, its repository, its tasks and their
// prerequisites, one in another plan and one standalone.
func fixtureView() []control.Item {
	return []control.Item{
		{ID: "p24", Kind: "plan", Repo: "r1", Title: "Control server rollout", State: "planned", Status: "executing", ApprovedVersion: 2},
		{ID: "r1", Kind: "repo", Slug: "dinoderek/engine"},
		{ID: "t30", Kind: "task", Plan: "p24", Title: "Acceptance", State: "todo", Depends: []string{"t26", "t28", "t29"}},
		{ID: "t26", Kind: "task", Plan: "p24", Title: "Store", State: "done"},
		{ID: "t28", Kind: "task", Plan: "p24", Title: "API", State: "running", Attempt: "a40", Depends: []string{"t26", "t5"}},
		{ID: "t29", Kind: "task", Plan: "p24", Title: "Review", State: "in-review", Attempt: "a41"},
		{ID: "t31", Kind: "task", Plan: "p24", Title: "Docs", State: "todo", Depends: []string{"t26"}},
		{ID: "t32", Kind: "task", Plan: "p24", Title: "Migrate", State: "todo", Depends: []string{"t7"}},
		{ID: "t33", Kind: "task", Plan: "p24", Title: "Retry me", State: "failed", Attempt: "a39"},
		{ID: "t7", Kind: "task", Plan: "p2", Title: "Schema in another plan", State: "in-review"},
		{ID: "t5", Kind: "task", Title: "Standalone fix", State: "approved"},
	}
}

func fixtureAttempts() []control.Attempt {
	return []control.Attempt{
		{ID: "a39", Item: "t33", Host: "mac", State: "ended"},
		{ID: "a40", Item: "t28", Host: "devbox", State: "running"},
		{ID: "a41", Item: "t29", Host: "mac", State: "waiting"},
		{ID: "a42", Item: "t7", Host: "devbox", State: "running"},
	}
}

func TestPlanDetail(t *testing.T) {
	got := planDetail("p24", fixtureView(), fixtureAttempts(), nil)
	if want := (PlanRow{ID: "p24", Repo: "r1", Slug: "dinoderek/engine", Title: "Control server rollout", State: "executing"}); got.Plan != want {
		t.Fatalf("plan = %+v, want %+v", got.Plan, want)
	}
	if want := (Counts{Done: 1, InProgress: 2, Ready: 1, Blocked: 3}); got.Counts != want {
		t.Fatalf("counts = %+v, want %+v", got.Counts, want)
	}
	want := []TaskRow{
		{ID: "t26", Title: "Store", State: "done", Group: groupDone, Waiting: []Prerequisite{}},
		{ID: "t28", Title: "API", State: "running", Group: groupInProgress, Host: "devbox",
			Waiting: []Prerequisite{{ID: "t5", Title: "Standalone fix", State: "approved"}}},
		{ID: "t29", Title: "Review", State: "in-review", Group: groupInProgress, Host: "mac", Waiting: []Prerequisite{}},
		{ID: "t30", Title: "Acceptance", State: "todo", Group: groupBlocked, Waiting: []Prerequisite{
			{ID: "t28", Title: "API", State: "running", Plan: "p24"}, {ID: "t29", Title: "Review", State: "in-review", Plan: "p24"}}},
		{ID: "t31", Title: "Docs", State: "todo", Group: groupReady, Waiting: []Prerequisite{}},
		{ID: "t32", Title: "Migrate", State: "todo", Group: groupBlocked,
			Waiting: []Prerequisite{{ID: "t7", Title: "Schema in another plan", State: "in-review", Plan: "p2"}}},
		{ID: "t33", Title: "Retry me", State: "failed", Group: groupBlocked, Waiting: []Prerequisite{}},
	}
	if !reflect.DeepEqual(got.Tasks, want) {
		t.Fatalf("tasks =\n%+v\nwant\n%+v", got.Tasks, want)
	}
}

func TestPlanDetailEdges(t *testing.T) {
	empty := planDetail("p24", fixtureView()[:2], nil, nil)
	if empty.Tasks == nil || len(empty.Tasks) != 0 || empty.Counts != (Counts{}) {
		t.Fatalf("plan without tasks = %+v, want an empty table", empty)
	}
	missing := planDetail("p1", []control.Item{
		{ID: "p1", Kind: "plan", Repo: "r1", State: "planned"},
		{ID: "t2", Kind: "task", Plan: "p1", State: "todo", Depends: []string{"t99"}},
	}, nil, nil)
	unapproved := fixtureView()
	unapproved[0].State, unapproved[0].ApprovedVersion = "prepare", 0
	if got := planDetail("p24", unapproved, fixtureAttempts(), nil).Counts; got != (Counts{Done: 1, InProgress: 2, Blocked: 4}) {
		t.Fatalf("unapproved plan counts = %+v, want its queued task blocked on approval", got)
	}
	if w := missing.Tasks[0].Waiting; len(w) != 1 || w[0].State != "unknown" || missing.Tasks[0].Group != groupBlocked {
		t.Fatalf("unreadable prerequisite = %+v, want a blocker in unknown state", missing.Tasks[0])
	}
}
