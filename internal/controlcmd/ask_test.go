package controlcmd

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

func TestAskBuildsChoicesARecommendationAndAClass(t *testing.T) {
	r, err := Parse([]string{"ask", "t7", "Which", "fix?", "--choice", "a, then b", "--choice=b", "--recommend", "1", "--class", "outward or costly"}, Env{Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if r.Op != "ask" || r.ID != "t7" || r.Body != "Which fix?" || !slices.Equal(r.Choices, []string{"a, then b", "b"}) ||
		r.Recommended == nil || *r.Recommended != 1 || r.Class != "outward or costly" {
		t.Fatalf("request %+v", r)
	}
	plain, err := Parse([]string{"ask", "t7", "Why?"}, Env{Getenv: func(string) string { return "" }})
	if err != nil || plain.Choices != nil || plain.Recommended != nil || plain.Class != "" {
		t.Fatalf("a plain ask: %+v, %v", plain, err)
	}
}

func TestBadQuestionFlagsExitWithUsage(t *testing.T) {
	for _, args := range [][]string{
		{"ask", "t7", "Which?", "--choice", "a", "--recommend", "first"},
		{"tell", "t7", "Note", "--choice", "a"},
		{"reply", "12", "Sure", "--class", "product"},
		{"resolve", "12", "Yes", "--recommend", "0"},
	} {
		var out, errs bytes.Buffer
		code := Main(args, Env{Out: &out, Err: &errs, Getenv: func(string) string { return "" }})
		if code != 2 || !strings.Contains(errs.String(), "engine ask <id> <text> [--choice <text>]...") {
			t.Errorf("%v exited %d: %s", args, code, errs.String())
		}
	}
}

func TestCLIAskWithChoicesAndASecondResolveRefused(t *testing.T) {
	e, _ := environment(t)
	repo := command(t, e, "repo", "add", "--title", "Demo", "--slug", "owner/demo", "--remote", "ssh://example.test/demo").Item
	plan := command(t, e, "plan", "create", "--repo", repo.ID, "--title", "Feature", "--body", "Requirements").Item
	asked := command(t, e, "ask", plan.ID, "Ship it?", "--choice", "Yes", "--choice", "No", "--recommend", "0", "--class", "product").Updates[0]
	if !slices.Equal(asked.Choices, []string{"Yes", "No"}) || asked.Recommended == nil || *asked.Recommended != 0 || asked.Class != "product" {
		t.Fatalf("question %+v", asked)
	}
	inbox := command(t, e, "inbox").Updates
	if len(inbox) != 1 || !slices.Equal(inbox[0].Choices, asked.Choices) || inbox[0].Class != "product" {
		t.Fatalf("inbox %+v", inbox)
	}
	if code := Main([]string{"ask", plan.ID, "Ship?", "--choice", "Yes", "--recommend", "3"}, e); code != 1 || !strings.Contains(e.Err.(*bytes.Buffer).String(), "recommended 3 is not a choice") {
		t.Fatalf("an out-of-range recommendation exited %d: %s", code, e.Err)
	}
	resolution := command(t, e, "resolve", itoa(asked.ID), "Yes", "--request-key", "answer").Updates[0]
	e.Err.(*bytes.Buffer).Reset()
	if code := Main([]string{"resolve", itoa(asked.ID), "No"}, e); code != 1 ||
		!strings.Contains(e.Err.(*bytes.Buffer).String(), control.ReasonAlreadyResolved+": question "+itoa(asked.ID)+" is already resolved, by update "+itoa(resolution.ID)) {
		t.Fatalf("a second resolve exited %d: %s", code, e.Err)
	}
	if replay := command(t, e, "resolve", itoa(asked.ID), "Yes", "--request-key", "answer").Updates[0]; replay.ID != resolution.ID {
		t.Fatalf("the replay returned update %d, want %d", replay.ID, resolution.ID)
	}
}
