// Package lint checks a feature's plan before its tasks run: the rules in
// the engine skill's plan reference (references/plan.md, "Breakdown"). It
// reads a tracker.Feature and never writes.
package lint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/tracker"
)

// required are the card sections every task must have. Requires may be
// empty or say "none"; the others must say something.
var required = []struct {
	Heading  string
	Nonempty bool
}{{"Outcome", true}, {"Tests", true}, {"Requires", false}}

// Problem is one broken rule.
type Problem struct {
	// Task is the card the problem is on; the zero Ref for the plan as a whole.
	Task tracker.Ref
	Text string
}

func (p Problem) String() string {
	if p.Task == (tracker.Ref{}) {
		return p.Text
	}
	return p.Task.String() + ": " + p.Text
}

// Lint returns every rule f's plan breaks, in task order; none means it
// passes.
func Lint(f tracker.Feature) []Problem {
	if len(f.Tasks) == 0 {
		return []Problem{{Text: "the feature has no tasks"}}
	}
	tasks := slices.Clone(f.Tasks)
	slices.SortStableFunc(tasks, func(a, b tracker.Card) int {
		if c := strings.Compare(a.Ref.Repo, b.Ref.Repo); c != 0 {
			return c
		}
		return a.Ref.Number - b.Ref.Number
	})
	var problems []Problem
	for _, c := range tasks {
		problems = append(problems, cardProblems(c)...)
	}
	problems = append(problems, keyProblems(tasks)...)
	problems = append(problems, cycleProblems(tasks)...)
	problems = append(problems, acceptanceProblems(tasks)...)
	return append(problems, sharedQuestions(tasks)...)
}

func cardProblems(c tracker.Card) []Problem {
	var problems []Problem
	for _, r := range required {
		text, ok := tracker.Section(c.Body, r.Heading)
		switch {
		case !ok:
			problems = append(problems, Problem{c.Ref, "no ## " + r.Heading + " section"})
		case r.Nonempty && text == "":
			problems = append(problems, Problem{c.Ref, "an empty ## " + r.Heading + " section"})
		}
	}
	if _, _, err := tracker.ParseHarnessTier(c.Body); err != nil {
		problems = append(problems, Problem{c.Ref, err.Error()})
	}
	return problems
}

// keyProblems finds two cards with one card key: a rerun planner would skip
// the second.
func keyProblems(tasks []tracker.Card) []Problem {
	first := map[string]tracker.Ref{}
	var problems []Problem
	for _, c := range tasks {
		key := tracker.CardKey(c.Body)
		if key == "" {
			problems = append(problems, Problem{c.Ref, "no <!-- engine:card <key> --> marker"})
			continue
		}
		if prev, ok := first[key]; ok {
			problems = append(problems, Problem{c.Ref, fmt.Sprintf("card key %q is also on %s", key, prev)})
			continue
		}
		first[key] = c.Ref
	}
	return problems
}

// blockers maps each task to the tasks of the feature it is blocked by.
func blockers(tasks []tracker.Card) map[tracker.Ref][]tracker.Ref {
	in := map[tracker.Ref]bool{}
	for _, c := range tasks {
		in[c.Ref] = true
	}
	edges := map[tracker.Ref][]tracker.Ref{}
	for _, c := range tasks {
		for _, b := range c.BlockedBy {
			if in[b.Ref] {
				edges[c.Ref] = append(edges[c.Ref], b.Ref)
			}
		}
	}
	return edges
}

// cycleProblems reports each task that is blocked by itself through others.
func cycleProblems(tasks []tracker.Card) []Problem {
	edges := blockers(tasks)
	var problems []Problem
	for _, c := range tasks {
		if slices.Contains(Upstream(edges, c.Ref), c.Ref) {
			problems = append(problems, Problem{c.Ref, "is in a dependency cycle"})
		}
	}
	return problems
}

// Upstream returns every task that from is blocked by, directly or through
// others, given each task's direct blockers.
func Upstream(edges map[tracker.Ref][]tracker.Ref, from tracker.Ref) []tracker.Ref {
	seen := map[tracker.Ref]bool{}
	var order []tracker.Ref
	stack := slices.Clone(edges[from])
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[r] {
			continue
		}
		seen[r] = true
		order = append(order, r)
		stack = append(stack, edges[r]...)
	}
	return order
}

// acceptanceProblems needs one task blocked, directly or not, by every other.
// A feature of one task is its own acceptance task.
func acceptanceProblems(tasks []tracker.Card) []Problem {
	edges := blockers(tasks)
	for _, c := range tasks {
		up := Upstream(edges, c.Ref)
		if len(up) >= len(tasks)-1 && !slices.Contains(up, c.Ref) {
			return nil
		}
	}
	return []Problem{{Text: "no acceptance task: no task is blocked by every other task"}}
}

var (
	listMark = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	// grade is a trailing risk grade, such as "(`low`)" or "(risk: med)".
	grade    = regexp.MustCompile("\\s*\\((?:risk:\\s*)?`?(?:low|med|high)`?\\)\\s*$")
	space    = regexp.MustCompile(`\s+`)
	noneItem = regexp.MustCompile(`^none\b`)
)

// sharedQuestions finds an open question on more than one card, by its text
// with case, spacing and a trailing grade ignored. Each question has one
// owner card.
func sharedQuestions(tasks []tracker.Card) []Problem {
	owner := map[string]tracker.Ref{}
	var problems []Problem
	for _, c := range tasks {
		text, _ := tracker.Section(c.Body, "Open")
		for _, q := range questions(text) {
			prev, ok := owner[q]
			switch {
			case !ok:
				owner[q] = c.Ref
			case prev != c.Ref:
				problems = append(problems, Problem{c.Ref, fmt.Sprintf("open question %q is also on %s: give it one owner card", q, prev)})
			}
		}
	}
	return problems
}

// questions returns the section's list items, normalised. A line with no
// list marker continues the item before it. An item starting with "none"
// is no question.
func questions(section string) []string {
	var items []string
	for _, line := range strings.Split(section, "\n") {
		item := strings.TrimSpace(listMark.ReplaceAllString(line, ""))
		switch {
		case item == "":
		case !listMark.MatchString(line) && len(items) > 0:
			items[len(items)-1] += " " + item
		default:
			items = append(items, item)
		}
	}
	var qs []string
	for _, item := range items {
		q := strings.ToLower(grade.ReplaceAllString(space.ReplaceAllString(item, " "), ""))
		if !noneItem.MatchString(q) {
			qs = append(qs, q)
		}
	}
	return qs
}
