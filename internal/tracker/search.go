package tracker

import (
	"fmt"
	"slices"
	"strings"
)

// ClaimLabels are the task state labels that mean an executor holds the task.
var ClaimLabels = []string{"engine:running", "engine:needs-human", "engine:in-review"}

// ClaimedQuery is the search for the open issues with any claim label on which
// user has commented, in every repo the token can see. GitHub reads the commas
// as "or". Naming a trusted commenter keeps other people's repos out.
func ClaimedQuery(user string) string {
	quoted := make([]string, len(ClaimLabels))
	for i, l := range ClaimLabels {
		quoted[i] = `"` + l + `"`
	}
	return "is:issue is:open commenter:" + user + " label:" + strings.Join(quoted, ",")
}

// ClaimedTask is an open task that carries a claim label and a trusted claim.
type ClaimedTask struct {
	Ref    Ref
	Title  string
	Labels []string
	// Claim is the task's latest claim comment by a trusted user.
	Claim Claim
}

// Has reports whether the task carries label.
func (t ClaimedTask) Has(label string) bool { return slices.Contains(t.Labels, label) }

type searchPage struct {
	Incomplete bool       `json:"incomplete_results"`
	Items      []apiIssue `json:"items"`
}

// Claimed finds every open issue with a claim label and a claim comment by a
// trusted user. It searches once per trusted user, since GitHub cannot "or"
// commenters, and leaves out an issue that none of them claimed. The order is
// GitHub's, the first trusted user's results first.
func Claimed(run Runner, token string) ([]ClaimedTask, error) {
	var tasks []ClaimedTask
	seen := map[Ref]bool{}
	for _, user := range TrustedUsers {
		issues, err := searchClaimed(run, token, user)
		if err != nil {
			return nil, err
		}
		for _, issue := range issues {
			ref, err := issue.ref()
			if err != nil {
				return nil, fmt.Errorf("search for claimed tasks: %w", err)
			}
			if seen[ref] {
				continue
			}
			seen[ref] = true
			claim, err := LatestClaim(run, token, ref)
			if err != nil {
				return nil, err
			}
			if claim != nil {
				tasks = append(tasks, ClaimedTask{Ref: ref, Title: issue.Title, Labels: issue.labelNames(), Claim: *claim})
			}
		}
	}
	return tasks, nil
}

func searchClaimed(run Runner, token, user string) ([]apiIssue, error) {
	out, err := api(run, token, "search/issues", "--method", "GET",
		"-f", "q="+ClaimedQuery(user), "-f", "per_page=100", "--paginate")
	if err != nil {
		return nil, fmt.Errorf("search for claimed tasks: %w", err)
	}
	pages, err := decodeAll[searchPage](out)
	if err != nil {
		return nil, fmt.Errorf("search for claimed tasks: %w", err)
	}
	var issues []apiIssue
	for _, page := range pages {
		if page.Incomplete {
			return nil, fmt.Errorf("search for claimed tasks: GitHub returned incomplete results; try again")
		}
		issues = append(issues, page.Items...)
	}
	return issues, nil
}
