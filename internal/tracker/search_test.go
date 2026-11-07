package tracker

import (
	"errors"
	"strings"
	"testing"
)

const (
	operator = "developer"
	other    = "another-developer"
)

// searchKey is how the fake names the search a trusted user's query runs.
func searchKey(user string) string { return "search/issues?" + ClaimedQuery(user) }

func item(repo string, n int, title string, labels ...string) string {
	var ls []string
	for _, l := range labels {
		ls = append(ls, `{"name":"`+l+`"}`)
	}
	return `{"number":` + string(rune('0'+n)) + `,"title":"` + title + `","state":"open","repository_url":"https://api.github.com/repos/` +
		repo + `","labels":[` + strings.Join(ls, ",") + `]}`
}

func results(items ...string) string {
	return `{"total_count":1,"incomplete_results":false,"items":[` + strings.Join(items, ",") + `]}`
}

func TestClaimedQuery(t *testing.T) {
	want := `is:issue is:open commenter:developer label:"engine:running","engine:needs-human","engine:in-review"`
	if got := ClaimedQuery(operator); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClaimedLeavesOutAnIssueWithoutATrustedClaim(t *testing.T) {
	gh := &fakeGH{replies: map[string]string{
		searchKey(operator): results(item("o/r", 3, "labelled early", "engine:running")),
		commentsPath(3):     thread(0, cm{operator, "Ruling (risk: med): x"}),
	}}
	tasks, err := Claimed(gh.run)
	if err != nil || len(tasks) != 0 {
		t.Errorf("got %v, %v", tasks, err)
	}
}

func TestClaimedNothing(t *testing.T) {
	gh := &fakeGH{replies: map[string]string{searchKey(operator): results()}}
	tasks, err := Claimed(gh.run)
	if err != nil || len(tasks) != 0 {
		t.Errorf("got %v, %v", tasks, err)
	}
}

func TestClaimedFailures(t *testing.T) {
	good := results(item("o/r", 3, "one", "engine:running"))
	key := searchKey(operator)
	cases := []struct {
		name string
		gh   *fakeGH
		want string
	}{
		{"search fails", &fakeGH{errs: map[string]error{key: errors.New("exit 1")}}, "search for claimed tasks: exit 1"},
		{"search not JSON", &fakeGH{replies: map[string]string{key: "nope"}}, "search for claimed tasks: unreadable"},
		{"incomplete", &fakeGH{replies: map[string]string{key: `{"incomplete_results":true,"items":[]}`}}, "incomplete results"},
		{"item without repo", &fakeGH{replies: map[string]string{key: `{"items":[{"number":1}]}`}}, "not a repository API URL"},
		{"comments fail", &fakeGH{replies: map[string]string{key: good}, errs: map[string]error{commentsPath(3): errors.New("exit 1")}}, "comments on " + short("o/r", 3)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Claimed(tc.gh.run); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestClaimed(t *testing.T) {
	page1 := results(item("o/r", 3, "one", "engine:running"), item("o/r", 4, "two", "engine:needs-human"))
	page2 := results(item("o/r", 4, "two", "engine:needs-human"), item("p/q", 5, "three", "engine:in-review"), item("x/y", 6, "foreign", "engine:running"))
	gh := &fakeGH{replies: map[string]string{
		"user": "developer", searchKey(operator): page1 + "\n" + page2,
		commentsPath(3):                    thread(0, cm{operator, fullClaim}),
		commentsPath(4):                    thread(0, cm{operator, fullClaim}, cm{other, fullClaim}),
		"repos/p/q/issues/" + "5/comments": thread(0, cm{operator, fullClaim}),
		"repos/x/y/issues/" + "6/comments": thread(0, cm{other, fullClaim}),
	}}
	tasks, err := Claimed(gh.run)
	if err != nil || len(tasks) != 3 {
		t.Fatalf("got %+v, %v", tasks, err)
	}
	for i, ref := range []Ref{{"o/r", 3}, {"o/r", 4}, {"p/q", 5}} {
		if tasks[i].Ref != ref || tasks[i].Claim.Author != operator {
			t.Fatalf("task %+v", tasks[i])
		}
	}
	if !tasks[0].Has("engine:running") || tasks[0].Has("engine:failed") {
		t.Fatal(tasks[0])
	}
	searches := 0
	for _, call := range gh.calls {
		if strings.Contains(call, "search/issues") {
			searches++
			if !strings.Contains(call, "commenter:developer") {
				t.Fatal(call)
			}
		}
	}
	if searches != 1 {
		t.Fatalf("%d searches", searches)
	}
}
