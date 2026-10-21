package tracker

import (
	"errors"
	"strings"
	"testing"
)

const (
	operator = "dinoderek"
	machine  = "dinoderek-agents"
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
	want := `is:issue is:open commenter:dinoderek-agents label:"engine:running","engine:needs-human","engine:in-review"`
	if got := ClaimedQuery(machine); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTrustedUsers(t *testing.T) {
	if len(TrustedUsers) != 2 || TrustedUsers[0] != operator || TrustedUsers[1] != machine {
		t.Errorf("got %v", TrustedUsers)
	}
	if !trusted("DinoDerek") || !trusted(machine) || trusted("stranger") || trusted("") || trusted(machine+"2") {
		t.Error("trusted is wrong")
	}
}

func TestClaimed(t *testing.T) {
	// The machine user's search finds tasks 3 and 4; the operator's finds 4
	// again (claimed by hand) and 5; task 6 sits in a foreign repo where only
	// a stranger claimed it.
	page1 := results(item("o/r", 3, "one", "engine:running"), item("o/r", 4, "two", "engine:needs-human", "engine:ruling-review"))
	page2 := results(item("o/r", 4, "two", "engine:needs-human", "engine:ruling-review"), item("p/q", 5, "three", "engine:in-review"), item("x/y", 6, "foreign", "engine:running"))
	gh := &fakeGH{replies: map[string]string{
		searchKey(machine):                 page1,
		searchKey(operator):                page2,
		commentsPath(3):                    thread(0, cm{machine, fullClaim}),
		commentsPath(4):                    thread(0, cm{machine, "no claim here"}, cm{operator, fullClaim}),
		"repos/p/q/issues/" + "5/comments": thread(0, cm{operator, fullClaim}),
		"repos/x/y/issues/" + "6/comments": thread(0, cm{"stranger", fullClaim}),
	}}
	tasks, err := Claimed(gh.run, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("got %d tasks: %+v", len(tasks), tasks)
	}
	// The operator is searched first, so their results lead.
	second, third, first := tasks[0], tasks[1], tasks[2]
	if first.Ref != (Ref{"o/r", 3}) || first.Title != "one" || !first.Has("engine:running") || first.Claim.Name != "a-21" || first.Claim.Author != machine {
		t.Errorf("first: %+v", first)
	}
	if second.Ref != (Ref{"o/r", 4}) || !second.Has("engine:ruling-review") || second.Has("engine:running") || second.Claim.Author != operator {
		t.Errorf("second: %+v", second)
	}
	if third.Ref != (Ref{"p/q", 5}) || third.Claim.Author != operator {
		t.Errorf("third: %+v", third)
	}
	searches := 0
	for _, call := range gh.calls {
		if strings.Contains(call, "search/issues") {
			searches++
			for _, part := range []string{"GH_TOKEN=tok gh api search/issues", "--method GET", "per_page=100", "--paginate"} {
				if !strings.Contains(call, part) {
					t.Errorf("search call %q lacks %q", call, part)
				}
			}
		}
		if strings.Contains(call, "x/y") && !strings.Contains(call, "comments") {
			t.Errorf("unexpected call about the foreign repo: %q", call)
		}
	}
	if searches != len(TrustedUsers) {
		t.Errorf("%d searches, want %d", searches, len(TrustedUsers))
	}
}

func TestClaimedLeavesOutAnIssueWithoutATrustedClaim(t *testing.T) {
	gh := &fakeGH{replies: map[string]string{
		searchKey(machine):  results(item("o/r", 3, "labelled early", "engine:running")),
		searchKey(operator): results(),
		commentsPath(3):     thread(0, cm{machine, "Ruling (risk: med): x"}),
	}}
	tasks, err := Claimed(gh.run, "tok")
	if err != nil || len(tasks) != 0 {
		t.Errorf("got %v, %v", tasks, err)
	}
}

func TestClaimedNothing(t *testing.T) {
	gh := &fakeGH{replies: map[string]string{searchKey(machine): results(), searchKey(operator): results()}}
	tasks, err := Claimed(gh.run, "tok")
	if err != nil || len(tasks) != 0 {
		t.Errorf("got %v, %v", tasks, err)
	}
}

func TestClaimedFailures(t *testing.T) {
	good := results(item("o/r", 3, "one", "engine:running"))
	empty := results()
	key := searchKey(TrustedUsers[0])
	cases := []struct {
		name string
		gh   *fakeGH
		want string
	}{
		{"search fails", &fakeGH{errs: map[string]error{key: errors.New("exit 1")}}, "search for claimed tasks: exit 1"},
		{"search not JSON", &fakeGH{replies: map[string]string{key: "nope"}}, "search for claimed tasks: unreadable"},
		{"incomplete", &fakeGH{replies: map[string]string{key: `{"incomplete_results":true,"items":[]}`}}, "incomplete results"},
		{"item without repo", &fakeGH{replies: map[string]string{key: `{"items":[{"number":1}]}`}}, "not a repository API URL"},
		{"second search fails", &fakeGH{replies: map[string]string{key: empty}, errs: map[string]error{searchKey(TrustedUsers[1]): errors.New("exit 1")}}, "search for claimed tasks: exit 1"},
		{"comments fail", &fakeGH{replies: map[string]string{key: good}, errs: map[string]error{commentsPath(3): errors.New("exit 1")}}, "comments on " + short("o/r", 3)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Claimed(tc.gh.run, "tok"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}
