package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/tracker"
)

func ref(n int) tracker.Ref { return tracker.Ref{Repo: "o/r", Number: n} }

// card is a well-formed card numbered n, with open as its Open section and
// blocked by the given tasks.
func card(n int, open string, blockedBy ...int) tracker.Card {
	body := fmt.Sprintf("## Outcome\nIt works.\n\n## Open\n%s\n\n## Tests\nA unit test.\n\n## Requires\nnone\n\n<!-- engine:card k%d -->\n", open, n)
	c := tracker.Card{Ref: ref(n), Open: true, Body: body}
	for _, b := range blockedBy {
		c.BlockedBy = append(c.BlockedBy, tracker.Blocker{Ref: ref(b), Open: true})
	}
	return c
}

// on is a problem's text on task n.
func on(n int, text string) string { return ref(n).String() + ": " + text }

func feature(cards ...tracker.Card) tracker.Feature {
	return tracker.Feature{Ref: ref(1), Tasks: cards}
}

func texts(ps []Problem) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.String())
	}
	return strings.Join(s, "\n")
}

func TestAGoodPlanPasses(t *testing.T) {
	f := feature(card(2, "- Which name?"), card(3, "None"), card(4, "", 2, 3))
	if ps := Lint(f); len(ps) != 0 {
		t.Errorf("problems:\n%s", texts(ps))
	}
}

func TestLintFindsEachProblem(t *testing.T) {
	missing := card(3, "")
	missing.Body = "## Outcome\n\n## Requires\n<!-- engine:card k3 -->\n## Harness / tier\ndeep light\n"
	dupKey := card(5, "")
	dupKey.Body = strings.Replace(dupKey.Body, "k5", "k2", 1)
	noKey := card(6, "")
	noKey.Body = strings.Replace(noKey.Body, "<!-- engine:card k6 -->", "", 1)
	noOutcome := card(2, "")
	noOutcome.Body = strings.Replace(noOutcome.Body, "## Outcome\nIt works.\n", "", 1)
	tests := []struct {
		name string
		f    tracker.Feature
		want []string
	}{
		{"no tasks", feature(), []string{"the feature has no tasks"}},
		{"sections", feature(card(2, ""), missing, card(4, "", 2, 3)), []string{
			on(3, "an empty ## Outcome section"), on(3, "no ## Tests section"), on(3, "the harness / tier section names two tiers")}},
		{"keys", feature(card(2, ""), dupKey, noKey, card(7, "", 2, 5, 6)), []string{
			on(5, `card key "k2" is also on `+ref(2).String()), on(6, "no <!-- engine:card <key> --> marker")}},
		{"cycle", feature(card(2, "", 3), card(3, "", 2), card(4, "", 2, 3)), []string{
			on(2, "is in a dependency cycle"), on(3, "is in a dependency cycle")}},
		{"no acceptance", feature(card(2, ""), card(3, ""), card(4, "", 2)), []string{"no acceptance task"}},
		{"same question", feature(card(2, "- Which name?"), card(3, "1. which  name?"), card(4, "", 2, 3)), []string{
			on(3, `open question "which name?" is also on `+ref(2).String())}},
		{"same question, other grade", feature(card(2, "- Which name? (`low`)"), card(3, "- Which\n  name? (risk: med)"), card(4, "", 2, 3)), []string{
			on(3, `open question "which name?" is also on `+ref(2).String())}},
		{"no outcome", feature(noOutcome, card(3, "", 2)), []string{on(2, "no ## Outcome section")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := texts(Lint(tt.f))
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("problems lack %q:\n%s", w, got)
				}
			}
			if n := len(strings.Split(got, "\n")); n != len(tt.want) {
				t.Errorf("%d problems, want %d:\n%s", n, len(tt.want), got)
			}
		})
	}
}

func TestDifferentQuestionsSharingNamesPass(t *testing.T) {
	f := feature(card(2, "- Flag name in `hosts.toml`? (`low`)\n- None yet"), card(3, "- Order in `hosts.toml`? (`low`)\n- none so far", 2))
	if ps := Lint(f); len(ps) != 0 {
		t.Errorf("problems:\n%s", texts(ps))
	}
}

func TestAcceptanceThroughOthersAndOutsideBlockers(t *testing.T) {
	// 4 is blocked by 3, which is blocked by 2; 9 is in another feature.
	f := feature(card(2, "", 9), card(3, "", 2), card(4, "", 3))
	if ps := Lint(f); len(ps) != 0 {
		t.Errorf("problems:\n%s", texts(ps))
	}
}

func TestOneTaskIsItsOwnAcceptance(t *testing.T) {
	if ps := Lint(feature(card(2, ""))); len(ps) != 0 {
		t.Errorf("problems:\n%s", texts(ps))
	}
}

func TestOneCardMayRepeatItsOwnQuestion(t *testing.T) {
	f := feature(card(2, "- Use x?\n- Use x?"), card(3, "", 2))
	if ps := Lint(f); len(ps) != 0 {
		t.Errorf("problems:\n%s", texts(ps))
	}
}

func TestUpstream(t *testing.T) {
	edges := map[tracker.Ref][]tracker.Ref{ref(4): {ref(3)}, ref(3): {ref(2)}, ref(2): {}}
	if got := fmt.Sprint(Upstream(edges, ref(4))); got != fmt.Sprint([]tracker.Ref{ref(3), ref(2)}) {
		t.Errorf("upstream of 4: %s", got)
	}
}
