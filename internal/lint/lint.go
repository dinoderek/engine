// Package lint checks a feature's plan before its tasks run: the rules in
// the engine skill's plan reference (references/plan.md, "Breakdown"). It
// reads a tracker.Feature and never writes.
package lint

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/dinoderek/engine/internal/tracker"
)

// Required are the card sections every task must have. Requires may say
// "none"; the others must say something.
var Required = []struct {
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
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Ref.Number < tasks[j].Ref.Number })
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
	for _, r := range Required {
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
func acceptanceProblems(tasks []tracker.Card) []Problem {
	edges := blockers(tasks)
	for _, c := range tasks {
		if len(Upstream(edges, c.Ref)) >= len(tasks)-1 && !slices.Contains(Upstream(edges, c.Ref), c.Ref) {
			return nil
		}
	}
	return []Problem{{Text: "no acceptance task: no task is blocked by every other task"}}
}

var (
	listMark = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)
	code     = regexp.MustCompile("`([^`]+)`")
	space    = regexp.MustCompile(`\s+`)
)

// sharedQuestions finds an open question on more than one card: the same
// item, or the same `name` in backticks (such as a fact's key) in the Open
// sections of two cards. Each question has one owner card.
func sharedQuestions(tasks []tracker.Card) []Problem {
	owner := map[string]tracker.Ref{}
	var problems []Problem
	for _, c := range tasks {
		text, _ := tracker.Section(c.Body, "Open")
		for _, q := range questionKeys(text) {
			prev, ok := owner[q]
			switch {
			case !ok:
				owner[q] = c.Ref
			case prev != c.Ref:
				problems = append(problems, Problem{c.Ref, fmt.Sprintf("open question %s is also on %s: give it one owner card", q, prev)})
			}
		}
	}
	return problems
}

// questionKeys returns what identifies each open question: its normalised
// text, quoted, and each name in backticks.
func questionKeys(section string) []string {
	var keys []string
	for _, line := range strings.Split(section, "\n") {
		item := strings.TrimSpace(listMark.ReplaceAllString(line, ""))
		if item == "" || strings.EqualFold(item, "none") || strings.EqualFold(item, "none.") {
			continue
		}
		keys = append(keys, fmt.Sprintf("%q", strings.ToLower(space.ReplaceAllString(item, " "))))
		for _, m := range code.FindAllStringSubmatch(item, -1) {
			keys = append(keys, "`"+m[1]+"`")
		}
	}
	return keys
}
