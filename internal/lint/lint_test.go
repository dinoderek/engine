package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

func texts(ps []Problem) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.String())
	}
	return strings.Join(s, "\n")
}

func TestSectionRules(t *testing.T) {
	for name, tc := range map[string]struct {
		items []control.Item
		want  string
	}{
		"different questions sharing names pass": {[]control.Item{item(2, "- Flag name in `hosts.toml`? (`low`)\n- None yet"), item(3, "- Order in `hosts.toml`? (`low`)\n- none so far", 2)}, ""},
		"a question differing only in grade is shared": {[]control.Item{item(2, "- Which name? (`low`)"), item(3, "- Which\n  name? (risk: med)", 2)},
			`t3: open question "which name?" is also on t2: give it one owner card`},
		"a task blocked by another of the plan, through others": {[]control.Item{item(2, "", 9), item(3, "", 2), item(4, "", 3)}, ""},
		"one task is its own acceptance":                        {[]control.Item{item(2, "")}, ""},
		"no acceptance task":                                    {[]control.Item{item(2, ""), item(3, ""), item(4, "", 2)}, "no acceptance task: no task is blocked by every other task"},
		"one task may repeat its own question":                  {[]control.Item{item(2, "- Use x?\n- Use x?"), item(3, "", 2)}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := texts(Items(tc.items)); got != tc.want {
				t.Errorf("problems:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestUpstream(t *testing.T) {
	edges := map[string][]string{"t4": {"t3"}, "t3": {"t2"}, "t2": {}}
	if got := fmt.Sprint(Upstream(edges, "t4")); got != fmt.Sprint([]string{"t3", "t2"}) {
		t.Errorf("upstream of t4: %s", got)
	}
}

func TestSection(t *testing.T) {
	body := "## Outcome\nIt works.\n<!-- note -->\n\n### Detail\nx\n## Open\n- a\n## Outcomes\nno\n"
	// A ### subheading stays inside its section; a comment is dropped.
	if got, ok := Section(body, "Outcome"); !ok || got != "It works.\n\n\n### Detail\nx" {
		t.Errorf("outcome %q %v", got, ok)
	}
	if got, ok := Section(body, "Open"); !ok || got != "- a" {
		t.Errorf("open %q %v", got, ok)
	}
	if _, ok := Section(body, "Tests"); ok {
		t.Error("found Tests")
	}
	if _, ok := Section("##Outcome\nx", "Outcome"); ok {
		t.Error("read ##Outcome as a heading")
	}
}
