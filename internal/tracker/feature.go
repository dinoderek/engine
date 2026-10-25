package tracker

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Feature is a feature issue and its tasks, as plan lint and the coordinator
// read them.
type Feature struct {
	Ref    Ref
	Title  string
	Open   bool
	Labels []string
	Tasks  []Card
}

// Has reports whether the feature carries label.
func (f Feature) Has(label string) bool { return slices.Contains(f.Labels, label) }

// Card is one task of a feature: its issue, and every issue it is blocked by,
// open or closed.
type Card struct {
	Ref       Ref
	Title     string
	Open      bool
	Labels    []string
	Body      string
	BlockedBy []Blocker
	// Requires are the capabilities the Requires section names.
	Requires []string
}

// Has reports whether the card carries label.
func (c Card) Has(label string) bool { return slices.Contains(c.Labels, label) }

// Blocker is an issue a card is blocked by.
type Blocker struct {
	Ref  Ref
	Open bool
}

// ReadFeature reads the feature issue ref, its sub-issues and each one's
// blockers.
func ReadFeature(run Runner, token string, ref Ref) (Feature, error) {
	out, err := api(run, token, fmt.Sprintf("repos/%s/issues/%d", ref.Repo, ref.Number))
	if err != nil {
		return Feature{}, fmt.Errorf("read %s: %w", ref, err)
	}
	issue, err := decodeOne[apiIssue](out)
	if err != nil {
		return Feature{}, fmt.Errorf("read %s: %w", ref, err)
	}
	f := Feature{Ref: ref, Title: issue.Title, Open: issue.State == "open", Labels: issue.labelNames()}
	out, err = api(run, token, fmt.Sprintf("repos/%s/issues/%d/sub_issues", ref.Repo, ref.Number), "--paginate")
	if err != nil {
		return Feature{}, fmt.Errorf("tasks of %s: %w", ref, err)
	}
	pages, err := decodeAll[[]apiIssue](out)
	if err != nil {
		return Feature{}, fmt.Errorf("tasks of %s: %w", ref, err)
	}
	for _, page := range pages {
		for _, sub := range page {
			card, err := readCard(run, token, sub)
			if err != nil {
				return Feature{}, err
			}
			f.Tasks = append(f.Tasks, card)
		}
	}
	return f, nil
}

func readCard(run Runner, token string, issue apiIssue) (Card, error) {
	ref, err := issue.ref()
	if err != nil {
		return Card{}, fmt.Errorf("a task: %w", err)
	}
	c := Card{Ref: ref, Title: issue.Title, Open: issue.State == "open", Labels: issue.labelNames(), Body: issue.Body, Requires: ParseRequires(issue.Body)}
	if c.BlockedBy, err = blockedBy(run, token, ref); err != nil {
		return Card{}, err
	}
	return c, nil
}

// blockedBy lists every issue ref is blocked by, open or closed.
func blockedBy(run Runner, token string, ref Ref) ([]Blocker, error) {
	path := fmt.Sprintf("repos/%s/issues/%d/dependencies/blocked_by", ref.Repo, ref.Number)
	out, err := api(run, token, path, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("blockers of %s: %w", ref, err)
	}
	pages, err := decodeAll[[]apiIssue](out)
	if err != nil {
		return nil, fmt.Errorf("blockers of %s: %w", ref, err)
	}
	var all []Blocker
	for _, page := range pages {
		for _, b := range page {
			bref, err := b.ref()
			if err != nil {
				return nil, fmt.Errorf("blockers of %s: %w", ref, err)
			}
			all = append(all, Blocker{Ref: bref, Open: b.State == "open"})
		}
	}
	return all, nil
}

var cardKey = regexp.MustCompile(`<!--\s*engine:card\s+(\S+)\s*-->`)

// CardKey returns the card's stable key, from its `<!-- engine:card <key> -->`
// marker, or "".
func CardKey(body string) string {
	if m := cardKey.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

// Section returns the text under the `## <heading>` line of a card body, up
// to the next heading, without HTML comments, and whether the heading is
// there.
func Section(body, heading string) (string, bool) {
	body = htmlComment.ReplaceAllString(strings.ReplaceAll(body, "\r\n", "\n"), "")
	var section []string
	in, found := false, false
	for _, line := range strings.Split(body, "\n") {
		switch {
		case isHeading(line, heading):
			in, found = true, true
		case in && nextHeading.MatchString(line):
			in = false
		case in:
			section = append(section, line)
		}
	}
	return strings.TrimSpace(strings.Join(section, "\n")), found
}

func isHeading(line, heading string) bool {
	rest, ok := strings.CutPrefix(line, "##")
	return ok && strings.TrimSpace(rest) == heading && (strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t"))
}
