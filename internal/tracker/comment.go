package tracker

import (
	"fmt"
	"strconv"
)

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
		return "", fmt.Errorf("commented on %s, but could not read the comment's address: %w", ref, err)
	}
	return c.URL, nil
}
