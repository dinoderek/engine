package hostops

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/session"
)

// MergeRequest is what the coordinator asks a task's host to merge: the PR,
// the head commit that was reviewed and gated, and the repo's merge method.
type MergeRequest struct {
	Repo   string `json:"repo"`
	PR     int    `json:"pr"`
	Head   string `json:"head"`
	Method string `json:"method"`
}

// Merge's outcomes. A refusal hands the PR back to its executor; pending
// means nothing is wrong yet, such as checks still running.
const (
	MergeDone    = "merged"
	MergeRefused = "refused"
	MergePending = "pending"
)

// Merged is merge's reply.
type Merged struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// prState is the part of gh pr view the merge reads.
type prState struct {
	State     string `json:"state"`
	Head      string `json:"headRefOid"`
	Base      string `json:"baseRefName"`
	Mergeable string `json:"mergeable"`
	Checks    []struct {
		Type       string `json:"__typename"`
		Name       string `json:"name"`
		Context    string `json:"context"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
	} `json:"statusCheckRollup"`
}

// Merge merges a PR with this host's gh, pinned to the reviewed head, once
// the PR is open at that head, every check is green, it is mergeable and the
// base branch is in the head's history. A PR already merged at that head is
// reported merged, so a merge whose result was lost is never retried.
func Merge(d Deps, r MergeRequest) (Merged, error) {
	if _, err := session.ParseRepo(r.Repo); err != nil {
		return Merged{}, refuse("%v", err)
	}
	if r.PR < 1 || !fullSHA.MatchString(r.Head) || !slices.Contains([]string{"squash", "merge", "rebase"}, r.Method) {
		return Merged{}, refuse("merge needs a PR number, the full head commit and squash, merge or rebase")
	}
	pr := strconv.Itoa(r.PR)
	out, err := d.Run(d.Home, nil, "gh", "pr", "view", pr, "-R", r.Repo, "--json", "state,headRefOid,baseRefName,mergeable,statusCheckRollup")
	if err != nil {
		return Merged{}, fmt.Errorf("read PR %s#%d: %w", r.Repo, r.PR, err)
	}
	var p prState
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return Merged{}, fmt.Errorf("read PR %s#%d: %w", r.Repo, r.PR, err)
	}
	if verdict, done := judge(p, r.Head); done {
		return verdict, nil
	}
	behind, err := d.Run(d.Home, nil, "gh", "api", fmt.Sprintf("repos/%s/compare/%s...%s", r.Repo, p.Base, r.Head), "--jq", ".behind_by")
	if err != nil {
		return Merged{}, fmt.Errorf("compare %s with %s: %w", r.Head, p.Base, err)
	}
	if n := strings.TrimSpace(behind); n != "0" {
		return Merged{MergeRefused, fmt.Sprintf("%s moved: the head is %s commits behind it; merge it in and gate again", p.Base, n)}, nil
	}
	if _, err := d.Run(d.Home, nil, "gh", "pr", "merge", pr, "-R", r.Repo, "--"+r.Method, "--match-head-commit", r.Head); err != nil {
		return Merged{}, fmt.Errorf("merge PR %s#%d: %w", r.Repo, r.PR, err)
	}
	return Merged{MergeDone, "merged at " + r.Head}, nil
}

// judge decides from the PR alone, and reports false when the base must
// still be compared.
func judge(p prState, head string) (Merged, bool) {
	switch {
	case p.State == "MERGED" && p.Head == head:
		return Merged{MergeDone, "already merged at " + head}, true
	case p.State == "MERGED":
		return Merged{MergeRefused, "the PR merged at " + p.Head + ", not the reviewed " + head}, true
	case p.State != "OPEN":
		return Merged{MergeRefused, "the PR is " + strings.ToLower(p.State)}, true
	case p.Head != head:
		return Merged{MergeRefused, "the PR's head is " + p.Head + ", not the reviewed " + head + "; gate and review it again"}, true
	}
	if failed, pending := checks(p); failed != "" {
		return Merged{MergeRefused, "check " + failed + " is not green"}, true
	} else if pending != "" {
		return Merged{MergePending, "check " + pending + " is still running"}, true
	}
	switch p.Mergeable {
	case "MERGEABLE":
		return Merged{}, false
	case "CONFLICTING":
		return Merged{MergeRefused, "the PR conflicts with " + p.Base + "; merge it in and gate again"}, true
	}
	return Merged{MergePending, "GitHub has not worked out whether the PR is mergeable"}, true
}

// checks returns the first failed check and the first pending one.
func checks(p prState) (failed, pending string) {
	for _, c := range p.Checks {
		name, ok, waiting := c.Name, false, false
		if c.Type == "StatusContext" {
			name, ok, waiting = c.Context, c.State == "SUCCESS", c.State == "PENDING" || c.State == "EXPECTED"
		} else {
			ok = slices.Contains([]string{"SUCCESS", "NEUTRAL", "SKIPPED"}, c.Conclusion)
			waiting = c.Status != "COMPLETED"
		}
		switch {
		case waiting && pending == "":
			pending = name
		case !waiting && !ok && failed == "":
			failed = name
		}
	}
	return failed, pending
}
