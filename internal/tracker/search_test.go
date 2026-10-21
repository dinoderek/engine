package tracker

import (
	"errors"
	"strings"
	"testing"
)

const searchPath = "search/issues"

func item(repo string, n int, title string, labels ...string) string {
	var ls []string
	for _, l := range labels {
		ls = append(ls, `{"name":"`+l+`"}`)
	}
	return `{"number":` + string(rune('0'+n)) + `,"title":"` + title + `","state":"open","repository_url":"https://api.github.com/repos/` +
		repo + `","labels":[` + strings.Join(ls, ",") + `]}`
}

func TestClaimedQuery(t *testing.T) {
	want := `is:issue is:open label:"engine:running","engine:needs-human","engine:in-review"`
	if got := ClaimedQuery(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClaimed(t *testing.T) {
	page1 := `{"total_count":3,"incomplete_results":false,"items":[` +
		item("o/r", 3, "one", "engine:running") + "," + item("o/r", 4, "two", "engine:needs-human", "engine:ruling-review") + `]}`
	page2 := `{"total_count":3,"incomplete_results":false,"items":[` + item("p/q", 5, "three", "engine:in-review") + `]}`
	gh := &fakeGH{replies: map[string]string{
		searchPath:                         page1 + "\n" + page2,
		commentsPath(3):                    comments("x", fullClaim),
		commentsPath(4):                    comments("no claim here"),
		"repos/p/q/issues/" + "5/comments": "[]",
	}}
	tasks, err := Claimed(gh.run, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("got %d tasks: %+v", len(tasks), tasks)
	}
	first, second, third := tasks[0], tasks[1], tasks[2]
	if first.Ref != (Ref{"o/r", 3}) || first.Title != "one" || !first.Has("engine:running") || first.Claim == nil || first.Claim.Name != "engine-13" {
		t.Errorf("first: %+v", first)
	}
	if second.Ref != (Ref{"o/r", 4}) || !second.Has("engine:ruling-review") || second.Has("engine:running") || second.Claim != nil {
		t.Errorf("second: %+v", second)
	}
	if third.Ref != (Ref{"p/q", 5}) || third.Claim != nil {
		t.Errorf("third: %+v", third)
	}
	call := gh.calls[0]
	for _, part := range []string{"GH_TOKEN=tok gh api search/issues", "--method GET", "-f q=" + ClaimedQuery(), "per_page=100", "--paginate"} {
		if !strings.Contains(call, part) {
			t.Errorf("search call %q lacks %q", call, part)
		}
	}
}

func TestClaimedNothing(t *testing.T) {
	gh := &fakeGH{replies: map[string]string{searchPath: `{"total_count":0,"incomplete_results":false,"items":[]}`}}
	tasks, err := Claimed(gh.run, "tok")
	if err != nil || len(tasks) != 0 {
		t.Errorf("got %v, %v", tasks, err)
	}
}

func TestClaimedFailures(t *testing.T) {
	good := `{"incomplete_results":false,"items":[` + item("o/r", 3, "one", "engine:running") + `]}`
	cases := []struct {
		name string
		gh   *fakeGH
		want string
	}{
		{"search fails", &fakeGH{errs: map[string]error{searchPath: errors.New("exit 1")}}, "search for claimed tasks: exit 1"},
		{"search not JSON", &fakeGH{replies: map[string]string{searchPath: "nope"}}, "search for claimed tasks: unreadable"},
		{"incomplete", &fakeGH{replies: map[string]string{searchPath: `{"incomplete_results":true,"items":[]}`}}, "incomplete results"},
		{"item without repo", &fakeGH{replies: map[string]string{searchPath: `{"items":[{"number":1}]}`}}, "not a repository API URL"},
		{"comments fail", &fakeGH{replies: map[string]string{searchPath: good}, errs: map[string]error{commentsPath(3): errors.New("exit 1")}}, "comments on " + short("o/r", 3)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Claimed(tc.gh.run, "tok"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}
