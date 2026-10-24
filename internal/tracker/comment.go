package tracker

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Comment posts body on the issue ref as the machine user and returns the
// comment's web URL.
func Comment(run Runner, token string, ref Ref, body string) (string, error) {
	out, err := api(run, token, restPath(ref)+"/comments", "-f", "body="+body)
	if err != nil {
		return "", fmt.Errorf("comment on %s: %w", ref, err)
	}
	c, err := decodeOne[struct {
		URL string `json:"html_url"`
	}](out)
	if err != nil {
		return "", fmt.Errorf("commented on %s, but could not read the comment's address: %w", ref, err)
	}
	return c.URL, nil
}

// restPath is the REST path of the issue ref.
func restPath(ref Ref) string {
	return "repos/" + ref.Repo + "/issues/" + strconv.Itoa(ref.Number)
}

// AddLabels adds labels to the issue ref.
func AddLabels(run Runner, token string, ref Ref, labels ...string) error {
	args := []string{restPath(ref) + "/labels", "-X", "POST"}
	for _, l := range labels {
		args = append(args, "-f", "labels[]="+l)
	}
	if _, err := api(run, token, args...); err != nil {
		return fmt.Errorf("label %s: %w", ref, err)
	}
	return nil
}

// RemoveLabel removes label from the issue ref. A label the issue does not
// carry is not an error.
func RemoveLabel(run Runner, token string, ref Ref, label string) error {
	out, err := api(run, token, restPath(ref)+"/labels/"+url.PathEscape(label), "-X", "DELETE")
	if err != nil && !strings.Contains(out+err.Error(), "Label does not exist") {
		return fmt.Errorf("unlabel %s: %w", ref, err)
	}
	return nil
}

// CloseCompleted closes the issue ref as completed.
func CloseCompleted(run Runner, token string, ref Ref) error {
	if _, err := api(run, token, restPath(ref), "-X", "PATCH", "-f", "state=closed", "-f", "state_reason=completed"); err != nil {
		return fmt.Errorf("close %s: %w", ref, err)
	}
	return nil
}

// Operator is the login engine @mentions about the issue ref: its first
// assignee, else its author.
func Operator(run Runner, token string, ref Ref) (string, error) {
	out, err := api(run, token, restPath(ref))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", ref, err)
	}
	type user struct {
		Login string `json:"login"`
	}
	issue, err := decodeOne[struct {
		User      user   `json:"user"`
		Assignees []user `json:"assignees"`
	}](out)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", ref, err)
	}
	if len(issue.Assignees) > 0 {
		return issue.Assignees[0].Login, nil
	}
	return issue.User.Login, nil
}

// PullRequests returns the web URLs of the merged pull requests that closed
// the issue ref.
func PullRequests(run Runner, token string, ref Ref) ([]string, error) {
	owner, name, _ := strings.Cut(ref.Repo, "/")
	query := `query($o:String!,$n:String!,$i:Int!){repository(owner:$o,name:$n){issue(number:$i){` +
		`closedByPullRequestsReferences(first:10,includeClosedPrs:true){nodes{url merged}}}}}`
	out, err := api(run, token, "graphql", "-f", "query="+query, "-f", "o="+owner, "-f", "n="+name, "-F", "i="+strconv.Itoa(ref.Number))
	if err != nil {
		return nil, fmt.Errorf("pull requests of %s: %w", ref, err)
	}
	resp, err := decodeOne[struct {
		Data struct {
			Repository struct {
				Issue struct {
					Closed struct {
						Nodes []struct {
							URL    string `json:"url"`
							Merged bool   `json:"merged"`
						} `json:"nodes"`
					} `json:"closedByPullRequestsReferences"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}](out)
	if err != nil {
		return nil, fmt.Errorf("pull requests of %s: %w", ref, err)
	}
	var urls []string
	for _, n := range resp.Data.Repository.Issue.Closed.Nodes {
		if n.Merged {
			urls = append(urls, n.URL)
		}
	}
	return urls, nil
}
