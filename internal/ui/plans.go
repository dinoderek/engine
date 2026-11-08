package ui

import (
	"slices"
	"sort"
	"strings"

	"github.com/dinoderek/engine/internal/control"
)

// Summary groups. Every task belongs to exactly one.
const (
	groupDone       = "done"
	groupInProgress = "in-progress"
	groupReady      = "ready"
	groupBlocked    = "blocked"
)

// Repo is a repository as the plan filters show it.
type Repo struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// PlanRow is one plan's metadata in the plan list.
type PlanRow struct {
	ID    string `json:"id"`
	Repo  string `json:"repo"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	State string `json:"state"`
}

// Filter narrows the plan list. Every set field must match.
type Filter struct {
	Repo, State, Query string
}

// PlanList is the plan list view with the facets its filters offer.
type PlanList struct {
	Repos  []Repo    `json:"repos"`
	States []string  `json:"states"`
	Plans  []PlanRow `json:"plans"`
	Total  int       `json:"total"`
}

// Counts are the four mutually exclusive task summary counts.
type Counts struct {
	Done       int `json:"done"`
	InProgress int `json:"in_progress"`
	Ready      int `json:"ready"`
	Blocked    int `json:"blocked"`
}

// Prerequisite is an unfinished task another task waits for, possibly in another plan.
type Prerequisite struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
	Plan  string `json:"plan,omitempty"`
}

// TaskRow is one direct child of the selected plan.
type TaskRow struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	State   string         `json:"state"`
	Group   string         `json:"group"`
	Host    string         `json:"host,omitempty"`
	Waiting []Prerequisite `json:"waiting"`
}

// PlanDetail is the selected plan's task pane.
type PlanDetail struct {
	Plan   PlanRow   `json:"plan"`
	Counts Counts    `json:"counts"`
	Tasks  []TaskRow `json:"tasks"`
}

func byNumber[T any](items []T, id func(T) string) {
	sort.SliceStable(items, func(i, j int) bool { return control.Number(id(items[i])) < control.Number(id(items[j])) })
}

// planList projects repository and plan records and applies the filter.
func planList(repos, plans []control.Item, f Filter) PlanList {
	out := PlanList{Repos: []Repo{}, States: []string{}, Plans: []PlanRow{}, Total: len(plans)}
	slugs := map[string]string{}
	for _, r := range repos {
		slugs[r.ID] = r.Slug
		out.Repos = append(out.Repos, Repo{ID: r.ID, Slug: r.Slug, Title: r.Title})
	}
	byNumber(out.Repos, func(r Repo) string { return r.ID })
	query := strings.ToLower(strings.TrimSpace(f.Query))
	for _, p := range plans {
		if !slices.Contains(out.States, p.State) {
			out.States = append(out.States, p.State)
		}
		row := PlanRow{ID: p.ID, Repo: p.Repo, Slug: slugs[p.Repo], Title: p.Title, State: p.State}
		if matches(row, f, query) {
			out.Plans = append(out.Plans, row)
		}
	}
	sort.Strings(out.States)
	byNumber(out.Plans, func(p PlanRow) string { return p.ID })
	return out
}

func matches(p PlanRow, f Filter, query string) bool {
	if f.Repo != "" && p.Repo != f.Repo || f.State != "" && p.State != f.State {
		return false
	}
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(strings.Join([]string{p.ID, p.Repo, p.Slug, p.Title}, "\n")), query)
}

// planDetail projects a plan view and the attempt list into the task pane.
// The view holds the plan, its repository, its tasks and their transitive prerequisites.
func planDetail(id string, view []control.Item, live []control.Attempt) PlanDetail {
	items := map[string]control.Item{}
	for _, it := range view {
		items[it.ID] = it
	}
	plan := items[id]
	out := PlanDetail{Plan: PlanRow{ID: plan.ID, Repo: plan.Repo, Slug: items[plan.Repo].Slug, Title: plan.Title, State: plan.State}, Tasks: []TaskRow{}}
	hosts := activeHosts(live)
	dispatchable := plan.ApprovedVersion > 0 && plan.State != "prepare"
	for _, t := range view {
		if t.Kind != "task" || t.Plan != id {
			continue
		}
		row := TaskRow{ID: t.ID, Title: t.Title, State: t.State, Host: hosts[t.Attempt], Waiting: []Prerequisite{}}
		for _, dep := range t.Depends {
			d, ok := items[dep]
			if !ok {
				d = control.Item{ID: dep, State: "unknown"}
			}
			if d.State != "done" {
				row.Waiting = append(row.Waiting, Prerequisite{ID: d.ID, Title: d.Title, State: d.State, Plan: d.Plan})
			}
		}
		row.Group = group(t.State, row.Host != "", len(row.Waiting) > 0, dispatchable)
		out.Counts.add(row.Group)
		out.Tasks = append(out.Tasks, row)
	}
	byNumber(out.Tasks, func(t TaskRow) string { return t.ID })
	return out
}

// activeHosts maps each unended attempt to the host it runs on.
func activeHosts(attempts []control.Attempt) map[string]string {
	out := map[string]string{}
	for _, at := range attempts {
		if at.State != "ended" {
			out[at.ID] = at.Host
		}
	}
	return out
}

// group maps a task's workflow state, live attempt, prerequisites and its
// plan's approval to its summary. Explicit blocking (operator intervention)
// wins over activity; activity wins over prerequisites, because work on the
// task is already under way. Only an approved plan's tasks can be dispatched.
func group(state string, active, waiting, approved bool) string {
	switch {
	case state == "done":
		return groupDone
	case state == "failed" || state == "needs-human":
		return groupBlocked
	case active || state == "running" || state == "in-review":
		return groupInProgress
	case waiting:
		return groupBlocked
	case approved && (state == "todo" || state == "approved"):
		return groupReady
	default:
		return groupBlocked
	}
}

func (c *Counts) add(g string) {
	switch g {
	case groupDone:
		c.Done++
	case groupInProgress:
		c.InProgress++
	case groupReady:
		c.Ready++
	default:
		c.Blocked++
	}
}
