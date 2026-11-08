package hostops

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const reviewed = "0123456789abcdef0123456789abcdef01234567"

// ghFake answers gh pr view with view, the compare with behind, and records
// every call.
type ghFake struct {
	view, behind string
	fail         map[string]error
	calls        []string
}

func (g *ghFake) run(_ string, _ []string, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	g.calls = append(g.calls, line)
	for key, err := range g.fail {
		if strings.Contains(line, key) {
			return "", err
		}
	}
	switch {
	case strings.Contains(line, "pr view"):
		return g.view, nil
	case strings.Contains(line, "compare"):
		return g.behind + "\n", nil
	}
	return "", nil
}

func view(state, head, mergeable, checks string) string {
	return `{"state":"` + state + `","headRefOid":"` + head + `","baseRefName":"main","mergeable":"` + mergeable + `","statusCheckRollup":[` + checks + `]}`
}

const (
	green   = `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"StatusContext","context":"ci/legacy","state":"SUCCESS"}`
	red     = `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"}`
	running = `{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS","conclusion":""}`
	waiting = `{"__typename":"StatusContext","context":"ci/legacy","state":"PENDING"}`
)

func merge(g *ghFake) (Merged, error) {
	return Merge(Deps{Run: g.run, Home: "/home"}, MergeRequest{Repo: "o/r", PR: 42, Head: reviewed, Method: "squash"})
}

func TestMergeMergesAGreenPinnedPR(t *testing.T) {
	g := &ghFake{view: view("OPEN", reviewed, "MERGEABLE", green), behind: "0"}
	m, err := merge(g)
	if err != nil || m.Outcome != MergeDone {
		t.Fatalf("merge %+v, %v", m, err)
	}
	want := "gh pr merge 42 -R o/r --squash --match-head-commit " + reviewed
	if !slices.Contains(g.calls, want) || !slices.Contains(g.calls, "gh api repos/o/r/compare/main..."+reviewed+" --jq .behind_by") {
		t.Errorf("calls %v", g.calls)
	}
}

func TestMergeHandsBackOrWaits(t *testing.T) {
	other := strings.Repeat("f", 40)
	for name, c := range map[string]struct {
		g       ghFake
		outcome string
		reason  string
	}{
		"already merged":   {ghFake{view: view("MERGED", reviewed, "UNKNOWN", green)}, MergeDone, "already merged"},
		"merged elsewhere": {ghFake{view: view("MERGED", other, "UNKNOWN", green)}, MergeRefused, "not the reviewed"},
		"closed":           {ghFake{view: view("CLOSED", reviewed, "UNKNOWN", green)}, MergeRefused, "the PR is closed"},
		"head changed":     {ghFake{view: view("OPEN", other, "MERGEABLE", green)}, MergeRefused, "gate and review it again"},
		"red checks":       {ghFake{view: view("OPEN", reviewed, "MERGEABLE", running+","+red)}, MergeRefused, "check test is not green"},
		"running checks":   {ghFake{view: view("OPEN", reviewed, "MERGEABLE", running)}, MergePending, "still running"},
		"pending status":   {ghFake{view: view("OPEN", reviewed, "MERGEABLE", waiting)}, MergePending, "ci/legacy"},
		"conflicting":      {ghFake{view: view("OPEN", reviewed, "CONFLICTING", green)}, MergeRefused, "conflicts with main"},
		"unknown":          {ghFake{view: view("OPEN", reviewed, "UNKNOWN", green)}, MergePending, "mergeable"},
		"main moved":       {ghFake{view: view("OPEN", reviewed, "MERGEABLE", green), behind: "3"}, MergeRefused, "main moved"},
		"no CI":            {ghFake{view: view("OPEN", reviewed, "MERGEABLE", ""), behind: "0"}, MergeDone, "merged at"},
	} {
		t.Run(name, func(t *testing.T) {
			g := c.g
			m, err := merge(&g)
			if err != nil || m.Outcome != c.outcome || !strings.Contains(m.Reason, c.reason) {
				t.Fatalf("merge %+v, %v; want %s with %q", m, err, c.outcome, c.reason)
			}
			if c.outcome != MergeDone || strings.Contains(c.reason, "already") {
				for _, call := range g.calls {
					if strings.Contains(call, "pr merge") {
						t.Errorf("merged anyway: %v", g.calls)
					}
				}
			}
		})
	}
}

func TestMergeFailuresAreErrors(t *testing.T) {
	auth := errors.New("gh: authentication required")
	for name, g := range map[string]*ghFake{
		"view":    {fail: map[string]error{"pr view": auth}},
		"garbled": {view: "not json"},
		"compare": {view: view("OPEN", reviewed, "MERGEABLE", green), fail: map[string]error{"compare": auth}},
		"merge":   {view: view("OPEN", reviewed, "MERGEABLE", green), behind: "0", fail: map[string]error{"pr merge": auth}},
	} {
		if m, err := merge(g); err == nil || isRefusal(err) {
			t.Errorf("%s: merge %+v, %v; want a failure that is not a refusal", name, m, err)
		}
	}
}

func TestMergeRefusesABadRequest(t *testing.T) {
	g := &ghFake{}
	for _, r := range []MergeRequest{
		{Repo: "o", PR: 1, Head: reviewed, Method: "squash"},
		{Repo: "o/r", PR: 0, Head: reviewed, Method: "squash"},
		{Repo: "o/r", PR: 1, Head: "abc", Method: "squash"},
		{Repo: "o/r", PR: 1, Head: reviewed, Method: "admin"},
	} {
		if _, err := Merge(Deps{Run: g.run}, r); !isRefusal(err) {
			t.Errorf("%+v: %v, want a refusal", r, err)
		}
	}
	if len(g.calls) != 0 {
		t.Errorf("a refused merge ran %v", g.calls)
	}
}

func TestMergeOpParsesItsFlags(t *testing.T) {
	h := newHome(t, "mac")
	g := &ghFake{view: view("MERGED", reviewed, "UNKNOWN", green)}
	code, reply, _ := call(t, Deps{Run: g.run, Home: h.dir}, "", "merge", "--json", "--repo", "o/r", "--pr", "42", "--head", reviewed, "--method", "squash")
	if code != 0 || reply["outcome"] != MergeDone {
		t.Errorf("exit %d, reply %v", code, reply)
	}
}
