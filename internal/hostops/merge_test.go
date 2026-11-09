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

// gated is a green, mergeable PR at the reviewed head with the given merge
// state and review decision.
func gated(mergeState, decision string) string {
	return `{"state":"OPEN","headRefOid":"` + reviewed + `","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"` + mergeState +
		`","reviewDecision":"` + decision + `","statusCheckRollup":[` + green + `]}`
}

func TestMergeHandsBackOrWaits(t *testing.T) {
	other := strings.Repeat("f", 40)
	policyErr := errors.New("GraphQL: Base branch policy prohibits the merge")
	for name, c := range map[string]struct {
		g       ghFake
		outcome string
		code    string
		reason  string
	}{
		"already merged":    {ghFake{view: view("MERGED", reviewed, "UNKNOWN", green)}, MergeDone, CodeMerged, "already merged"},
		"merged elsewhere":  {ghFake{view: view("MERGED", other, "UNKNOWN", green)}, MergeRefused, CodeMergedElsewhere, "not the reviewed"},
		"closed":            {ghFake{view: view("CLOSED", reviewed, "UNKNOWN", green)}, MergeRefused, CodeClosed, "the PR is closed"},
		"head changed":      {ghFake{view: view("OPEN", other, "MERGEABLE", green)}, MergeRefused, CodeHeadMoved, "gate and review it again"},
		"red checks":        {ghFake{view: view("OPEN", reviewed, "MERGEABLE", running+","+red)}, MergeRefused, CodeRed, "check test is not green"},
		"running checks":    {ghFake{view: view("OPEN", reviewed, "MERGEABLE", running)}, MergePending, CodePendingChecks, "still running"},
		"pending status":    {ghFake{view: view("OPEN", reviewed, "MERGEABLE", waiting)}, MergePending, CodePendingChecks, "ci/legacy"},
		"conflicting":       {ghFake{view: view("OPEN", reviewed, "CONFLICTING", green)}, MergeRefused, CodeConflict, "conflicts with main"},
		"unknown":           {ghFake{view: view("OPEN", reviewed, "UNKNOWN", green)}, MergePending, CodePendingMergeable, "mergeable"},
		"review required":   {ghFake{view: gated("BLOCKED", "REVIEW_REQUIRED")}, MergeRefused, CodeBlocked, "REVIEW_REQUIRED"},
		"changes requested": {ghFake{view: gated("CLEAN", "CHANGES_REQUESTED")}, MergeRefused, CodeBlocked, "CHANGES_REQUESTED"},
		"protected":         {ghFake{view: gated("BLOCKED", "")}, MergeRefused, CodeBlocked, "BLOCKED"},
		"checks not yet registered": {ghFake{view: `{"state":"OPEN","headRefOid":"` + reviewed + `","baseRefName":"main","mergeable":"MERGEABLE","mergeStateStatus":"BLOCKED"}`},
			MergePending, CodePendingChecks, "no check has reported"},
		"policy on merge": {ghFake{view: gated("CLEAN", "APPROVED"), behind: "0", fail: map[string]error{"pr merge": policyErr}}, MergeRefused, CodeBlocked, "Base branch policy"},
		"main moved":      {ghFake{view: view("OPEN", reviewed, "MERGEABLE", green), behind: "3"}, MergeRefused, CodeBehind, "main moved"},
		"no CI":           {ghFake{view: view("OPEN", reviewed, "MERGEABLE", ""), behind: "0"}, MergeDone, CodeMerged, "merged at"},
	} {
		t.Run(name, func(t *testing.T) {
			g := c.g
			m, err := merge(&g)
			if err != nil || m.Outcome != c.outcome || m.Code != c.code || !strings.Contains(m.Reason, c.reason) {
				t.Fatalf("merge %+v, %v; want %s (%s) with %q", m, err, c.outcome, c.code, c.reason)
			}
			if c.code != CodeMerged || strings.Contains(c.reason, "already") {
				for _, call := range g.calls {
					if strings.Contains(call, "pr merge") && c.g.fail == nil {
						t.Errorf("merged anyway: %v", g.calls)
					}
				}
			}
		})
	}
}

func TestACheckJudgesWithoutMerging(t *testing.T) {
	for want, v := range map[string]string{
		CodeReady: view("OPEN", reviewed, "MERGEABLE", green),
		CodeRed:   view("OPEN", reviewed, "MERGEABLE", red),
	} {
		g := &ghFake{view: v, behind: "5"}
		m, err := Merge(Deps{Run: g.run}, MergeRequest{Repo: "o/r", PR: 42, Head: reviewed, Method: "squash", Check: true})
		if err != nil || m.Code != want || len(g.calls) != 1 {
			t.Errorf("check %+v, %v, calls %v; want %s from one read", m, err, g.calls, want)
		}
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
