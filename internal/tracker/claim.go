package tracker

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Claim is one executor's claim comment on a task issue. A field the comment
// leaves out, or gives as `none`, is empty.
type Claim struct {
	Name          string // <repo>-<task>, the session name
	Run           string
	Host          string // the host's logical name
	Harness       string // the raw line: <harness> <version> (<model>)
	Mode          string
	StartedBy     string // hand, engine start or coordinator
	Tmux          string // session:window
	RemoteControl string
	Branch        string

	Author   string
	URL      string
	PostedAt time.Time
}

// HarnessName is the harness's own name, such as claude-code.
func (c Claim) HarnessName() string {
	name, _, _ := strings.Cut(c.Harness, " ")
	return name
}

// claimPrefix starts every claim comment. The skill's execute reference owns
// the format.
const claimPrefix = "Claim:"

// ParseClaim reads a claim comment body. It reports false unless the first
// line is `Claim: <name>`. Unknown lines are ignored, a repeated line keeps
// its first value, and a missing line leaves its field empty.
func ParseClaim(body string) (Claim, bool) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n"), "\n")
	if !strings.HasPrefix(lines[0], claimPrefix) {
		return Claim{}, false
	}
	var c Claim
	seen := map[string]bool{}
	fields := map[string]*string{
		"Claim": &c.Name, "Run": &c.Run, "Host": &c.Host, "Harness": &c.Harness,
		"Mode": &c.Mode, "Started by": &c.StartedBy, "Tmux": &c.Tmux,
		"Remote Control": &c.RemoteControl, "Branch": &c.Branch,
	}
	for _, line := range lines {
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		dst := fields[key]
		if !ok || dst == nil || seen[key] {
			continue
		}
		seen[key] = true
		if value = strings.TrimSpace(value); !strings.EqualFold(value, "none") {
			*dst = value
		}
	}
	return c, c.Name != ""
}

type apiComment struct {
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// TrustedUsers are the only GitHub users whose claim comments engine reads: the
// operator and the machine user. A claim from anyone else is ignored.
var TrustedUsers = []string{"dinoderek", "dinoderek-agents"}

func trusted(login string) bool {
	return slices.ContainsFunc(TrustedUsers, func(u string) bool { return strings.EqualFold(u, login) })
}

// LatestClaim returns the newest claim comment by a trusted user on the
// issue, or nil when it has none.
func LatestClaim(run Runner, token string, ref Ref) (*Claim, error) {
	path := fmt.Sprintf("repos/%s/issues/%d/comments", ref.Repo, ref.Number)
	out, err := api(run, token, path, "--paginate", "-f", "per_page=100", "--method", "GET")
	if err != nil {
		return nil, fmt.Errorf("comments on %s: %w", ref, err)
	}
	pages, err := decodeAll[[]apiComment](out)
	if err != nil {
		return nil, fmt.Errorf("comments on %s: %w", ref, err)
	}
	var latest *Claim
	for _, page := range pages {
		for _, comment := range page {
			if c, ok := ParseClaim(comment.Body); ok && trusted(comment.User.Login) {
				c.Author, c.URL, c.PostedAt = comment.User.Login, comment.HTMLURL, comment.CreatedAt
				latest = &c
			}
		}
	}
	return latest, nil
}

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
