package lint

import (
	"slices"

	"github.com/dinoderek/engine/internal/control"
)

// serverSections are the sections a server task's body must have. Its
// requirements, harness and tier are structured fields, not sections.
var serverSections = required[:2]

// Items returns every rule a server plan's tasks break, in task order;
// none means it passes. Dependencies are structured and IDs are unique, so
// the card-key and "blocked by" rules do not apply.
func Items(items []control.Item) []Problem {
	if len(items) == 0 {
		return []Problem{{Text: "the feature has no tasks"}}
	}
	items = slices.Clone(items)
	slices.SortStableFunc(items, func(a, b control.Item) int { return control.Number(a.ID) - control.Number(b.ID) })
	tasks := make([]task, len(items))
	var problems []Problem
	for i, it := range items {
		tasks[i] = task{name: it.ID, body: it.Body, deps: it.Depends}
		problems = append(problems, sectionProblems(tasks[i], serverSections)...)
	}
	return append(problems, planProblems(tasks)...)
}
