package lint

import (
	"fmt"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

// item is a well-formed server task t<n>, with open as its Open section,
// depending on the given tasks. It has no card key, Requires or harness
// section: those are structured.
func item(n int, open string, deps ...int) control.Item {
	it := control.Item{ID: fmt.Sprintf("t%d", n), Kind: "task", Body: "## Outcome\nIt works.\n\n## Open\n" + open + "\n\n## Tests\nA unit test.\n"}
	for _, d := range deps {
		it.Depends = append(it.Depends, fmt.Sprintf("t%d", d))
	}
	return it
}

func TestItemsAppliesThePlanRulesToServerTasks(t *testing.T) {
	noTests := item(3, "")
	noTests.Body = "## Outcome\nIt works."
	emptyOutcome := item(4, "", 2)
	emptyOutcome.Body = "## Outcome\n\n## Tests\nA unit test."
	for _, tc := range []struct {
		name  string
		items []control.Item
		want  string
	}{
		{"a good plan passes, in any order", []control.Item{item(4, "", 2, 3), item(2, "- Which name?"), item(3, "None")}, ""},
		{"no tasks", nil, "the feature has no tasks"},
		{"sections, by task number", []control.Item{emptyOutcome, item(2, ""), noTests},
			"t3: no ## Tests section\nt4: an empty ## Outcome section\nno acceptance task: no task is blocked by every other task"},
		{"a cycle has no acceptance task", []control.Item{item(2, "", 3), item(3, "", 2)},
			"t2: is in a dependency cycle\nt3: is in a dependency cycle\nno acceptance task: no task is blocked by every other task"},
		{"a dependency outside the plan is no edge", []control.Item{item(2, "", 9), item(3, "", 2)}, ""},
		{"a shared open question", []control.Item{item(2, "- Which name?"), item(3, "- which  NAME? (`low`)", 2)},
			`t3: open question "which name?" is also on t2: give it one owner card`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := texts(Items(tc.items)); got != tc.want {
				t.Errorf("problems:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}
