package tracker

import (
	"fmt"
	"strconv"
)

// FindClaim returns the claimed task whose session is named name, by the
// claim posted last: a task that ran twice has two claims under one name.
func FindClaim(tasks []ClaimedTask, name string) (ClaimedTask, bool) {
	var latest ClaimedTask
	found := false
	for _, t := range tasks {
		if t.Claim.Name != name {
			continue
		}
		if !found || t.Claim.PostedAt.After(latest.Claim.PostedAt) {
			latest, found = t, true
		}
	}
	return latest, found
}

// Comment posts body on the issue ref as the machine user and returns the
// comment's web URL.
func Comment(run Runner, token string, ref Ref, body string) (string, error) {
	out, err := api(run, token, "repos/"+ref.Repo+"/issues/"+strconv.Itoa(ref.Number)+"/comments", "-f", "body="+body)
	if err != nil {
		return "", fmt.Errorf("comment on %s: %w", ref, err)
	}
	c, err := decodeOne[struct {
		URL string `json:"html_url"`
	}](out)
	if err != nil {
		return "", fmt.Errorf("comment on %s: %w", ref, err)
	}
	return c.URL, nil
}
