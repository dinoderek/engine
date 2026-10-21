package tracker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Ref names one issue: Repo is owner/name.
type Ref struct {
	Repo   string
	Number int
}

// String renders the reference as owner/repo#n.
func (r Ref) String() string { return fmt.Sprintf("%s#%d", r.Repo, r.Number) }

// ParseRef reads owner/repo#n.
func ParseRef(s string) (Ref, error) {
	repo, num, ok := strings.Cut(s, "#")
	n, err := strconv.Atoi(num)
	if !ok || err != nil || n < 1 || !repoName.MatchString(repo) {
		return Ref{}, fmt.Errorf("want owner/repo#n, got %q", s)
	}
	return Ref{Repo: repo, Number: n}, nil
}

// apiPrefix starts every issue and repository URL the REST API returns.
const apiPrefix = "/repos/"

// repoFromURL returns owner/name from a repository or issue API URL.
func repoFromURL(u string) (string, error) {
	_, rest, ok := strings.Cut(u, apiPrefix)
	parts := strings.Split(rest, "/")
	if !ok || len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("not a repository API URL: %q", u)
	}
	return parts[0] + "/" + parts[1], nil
}

// refFromIssueURL reads a Ref from an issue API URL, such as a parent_issue_url.
func refFromIssueURL(u string) (Ref, error) {
	repo, err := repoFromURL(u)
	if err != nil {
		return Ref{}, err
	}
	n, err := strconv.Atoi(u[strings.LastIndex(u, "/")+1:])
	if err != nil || !strings.Contains(u, "/issues/") {
		return Ref{}, fmt.Errorf("not an issue API URL: %q", u)
	}
	return Ref{Repo: repo, Number: n}, nil
}

// apiIssue is the part of a REST issue that engine reads. The search API
// returns the same shape.
type apiIssue struct {
	Number        int    `json:"number"`
	Title         string `json:"title"`
	State         string `json:"state"`
	Body          string `json:"body"`
	RepositoryURL string `json:"repository_url"`
	ParentURL     string `json:"parent_issue_url"`
	Labels        []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (i apiIssue) labelNames() []string {
	names := make([]string, len(i.Labels))
	for k, l := range i.Labels {
		names[k] = l.Name
	}
	return names
}

func (i apiIssue) ref() (Ref, error) {
	repo, err := repoFromURL(i.RepositoryURL)
	if err != nil {
		return Ref{}, err
	}
	return Ref{Repo: repo, Number: i.Number}, nil
}

// api runs gh api as the machine user and returns its output. A failure
// carries gh's own output, which says why.
func api(run Runner, token string, args ...string) (string, error) {
	out, err := run([]string{"GH_TOKEN=" + token}, "gh", append([]string{"api"}, args...)...)
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, strings.TrimSpace(out))
	}
	return out, nil
}

// decodeAll reads every JSON value in out. gh api --paginate prints one value
// per page, back to back.
func decodeAll[T any](out string) ([]T, error) {
	dec := json.NewDecoder(strings.NewReader(out))
	var all []T
	for {
		var v T
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			return all, nil
		}
		if err != nil {
			return nil, fmt.Errorf("unreadable gh output: %w", err)
		}
		all = append(all, v)
	}
}

// decodeOne reads the single JSON value in out.
func decodeOne[T any](out string) (T, error) {
	var zero T
	all, err := decodeAll[T](out)
	if err != nil {
		return zero, err
	}
	if len(all) != 1 {
		return zero, fmt.Errorf("want one JSON value from gh, got %d", len(all))
	}
	return all[0], nil
}
