// Package lint checks a plan before its tasks run: the rules in the engine
// skill's plan reference (references/plan.md, "Breakdown"). It reads a
// GitHub tracker.Feature, or a server plan's tasks, and never writes.
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
var required = []section{{"Outcome", true}, {"Tests", true}, {"Requires", false}}

// section is a heading a task's body must have, and whether it must say
// something.
type section struct {
	Heading  string
	Nonempty bool
}

// Problem is one broken rule.
type Problem struct {
	// Task names the task the problem is on; empty for the plan as a whole.
	Task string
	Text string
}

func (p Problem) String() string {
	if p.Task == "" {
		return p.Text
	}
	return p.Task + ": " + p.Text
}

// task is what the plan rules read of one task: the name problems give it,
// its body, and the names of the tasks it depends on.
type task struct {
	name string
	body string
	deps []string
}

// Lint returns every rule f's plan breaks, in task order; none means it
// passes.
func Lint(f tracker.Feature) []Problem {
	if len(f.Tasks) == 0 {
		return []Problem{{Text: "the feature has no tasks"}}
	}
	cards := slices.Clone(f.Tasks)
	slices.SortStableFunc(cards, func(a, b tracker.Card) int {
		if c := strings.Compare(a.Ref.Repo, b.Ref.Repo); c != 0 {
			return c
		}
		return a.Ref.Number - b.Ref.Number
	})
	tasks := make([]task, len(cards))
	var problems []Problem
	for i, c := range cards {
		tasks[i] = task{name: c.Ref.String(), body: c.Body}
		for _, b := range c.BlockedBy {
			tasks[i].deps = append(tasks[i].deps, b.Ref.String())
		}
		problems = append(problems, sectionProblems(tasks[i], required)...)
		if _, _, err := tracker.ParseHarnessTier(c.Body); err != nil {
			problems = append(problems, Problem{tasks[i].name, err.Error()})
		}
	}
	problems = append(problems, keyProblems(cards)...)
	return append(problems, planProblems(tasks)...)
}

// planProblems are the rules across tasks: no cycle, one acceptance task,
// and one owner per open question.
func planProblems(tasks []task) []Problem {
	problems := cycleProblems(tasks)
	problems = append(problems, acceptanceProblems(tasks)...)
	return append(problems, sharedQuestions(tasks)...)
}

// sectionProblems are the required sections t lacks or leaves empty.
func sectionProblems(t task, sections []section) []Problem {
	var problems []Problem
	for _, r := range sections {
		text, ok := tracker.Section(t.body, r.Heading)
		switch {
		case !ok:
			problems = append(problems, Problem{t.name, "no ## " + r.Heading + " section"})
		case r.Nonempty && text == "":
			problems = append(problems, Problem{t.name, "an empty ## " + r.Heading + " section"})
		}
	}
	return problems
}

// keyProblems finds two cards with one card key: a rerun planner would skip
// the second.
func keyProblems(cards []tracker.Card) []Problem {
	first := map[string]tracker.Ref{}
	var problems []Problem
	for _, c := range cards {
		key := tracker.CardKey(c.Body)
		if key == "" {
			problems = append(problems, Problem{c.Ref.String(), "no <!-- engine:card <key> --> marker"})
			continue
		}
		if prev, ok := first[key]; ok {
			problems = append(problems, Problem{c.Ref.String(), fmt.Sprintf("card key %q is also on %s", key, prev)})
			continue
		}
		first[key] = c.Ref
	}
	return problems
}

// blockers maps each task to the tasks of the plan it depends on.
func blockers(tasks []task) map[string][]string {
	in := map[string]bool{}
	for _, t := range tasks {
		in[t.name] = true
	}
	edges := map[string][]string{}
	for _, t := range tasks {
		for _, d := range t.deps {
			if in[d] {
				edges[t.name] = append(edges[t.name], d)
			}
		}
	}
	return edges
}

// cycleProblems reports each task that depends on itself through others.
func cycleProblems(tasks []task) []Problem {
	edges := blockers(tasks)
	var problems []Problem
	for _, t := range tasks {
		if slices.Contains(Upstream(edges, t.name), t.name) {
			problems = append(problems, Problem{t.name, "is in a dependency cycle"})
		}
	}
	return problems
}

// Upstream returns every task that from is blocked by, directly or through
// others, given each task's direct blockers.
func Upstream[K comparable](edges map[K][]K, from K) []K {
	seen := map[K]bool{}
	var order []K
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
// A plan of one task is its own acceptance task.
func acceptanceProblems(tasks []task) []Problem {
	edges := blockers(tasks)
	for _, t := range tasks {
		up := Upstream(edges, t.name)
		if len(up) >= len(tasks)-1 && !slices.Contains(up, t.name) {
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
func sharedQuestions(tasks []task) []Problem {
	owner := map[string]string{}
	var problems []Problem
	for _, t := range tasks {
		text, _ := tracker.Section(t.body, "Open")
		for _, q := range questions(text) {
			prev, ok := owner[q]
			switch {
			case !ok:
				owner[q] = t.name
			case prev != t.name:
				problems = append(problems, Problem{t.name, fmt.Sprintf("open question %q is also on %s: give it one owner card", q, prev)})
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
