package tracker

import (
	"fmt"
	"slices"
	"strings"
)

// ClaimLabels are the task state labels that mean an executor holds the task.
var ClaimLabels = []string{"engine:running", "engine:needs-human", "engine:in-review"}

// ClaimedQuery is the search for every open issue with any claim label, in
// every repo the token can see. GitHub reads the commas as "or".
func ClaimedQuery() string {
	quoted := make([]string, len(ClaimLabels))
	for i, l := range ClaimLabels {
		quoted[i] = `"` + l + `"`
	}
	return "is:issue is:open label:" + strings.Join(quoted, ",")
}

// ClaimedTask is an open task that carries a claim label.
type ClaimedTask struct {
	Ref    Ref
	Title  string
	Labels []string
	// Claim is the task's latest claim comment, or nil when it has none, for
	// example when the label landed before the comment.
	Claim *Claim
}

// Has reports whether the task carries label.
func (t ClaimedTask) Has(label string) bool { return slices.Contains(t.Labels, label) }

type searchPage struct {
	Incomplete bool       `json:"incomplete_results"`
	Items      []apiIssue `json:"items"`
}

// Claimed finds every open issue the token can see that has a claim label,
// each with its latest claim comment. The order is GitHub's.
func Claimed(run Runner, token string) ([]ClaimedTask, error) {
	out, err := api(run, token, "search/issues", "--method", "GET",
		"-f", "q="+ClaimedQuery(), "-f", "per_page=100", "--paginate")
	if err != nil {
		return nil, fmt.Errorf("search for claimed tasks: %w", err)
	}
	pages, err := decodeAll[searchPage](out)
	if err != nil {
		return nil, fmt.Errorf("search for claimed tasks: %w", err)
	}
	var tasks []ClaimedTask
	for _, page := range pages {
		if page.Incomplete {
			return nil, fmt.Errorf("search for claimed tasks: GitHub returned incomplete results; try again")
		}
		for _, issue := range page.Items {
			ref, err := issue.ref()
			if err != nil {
				return nil, fmt.Errorf("search for claimed tasks: %w", err)
			}
			claim, err := LatestClaim(run, token, ref)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, ClaimedTask{Ref: ref, Title: issue.Title, Labels: issue.labelNames(), Claim: claim})
		}
	}
	return tasks, nil
}
