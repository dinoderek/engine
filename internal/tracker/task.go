package tracker

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// DefaultTier is the tier of a card whose Harness / tier section is empty.
const DefaultTier = "standard"

var (
	tiers = []string{"deep", "standard", "light"}
	// skipWords are labels and "no harness" markers, none of them a harness.
	skipWords = []string{"harness", "tier", "none", "default"}
)

// Task is a task issue as the coordinator reads it.
type Task struct {
	Ref    Ref
	Title  string
	Open   bool
	Labels []string
	// OpenBlockers are the issues this one is blocked by that are still open.
	OpenBlockers []Ref
	// Parent is the feature the task belongs to, or nil for a standalone task.
	Parent *Ref
	// Harness is the card's harness, empty for the host's default.
	Harness string
	// Tier is deep, standard or light; DefaultTier when the card names none.
	Tier string
	// Requires are the capabilities the card's Requires section names.
	Requires []string
}

// Has reports whether the task carries label.
func (t Task) Has(label string) bool { return slices.Contains(t.Labels, label) }

// ReadTask reads a task issue: its labels, its open blockers, its parent
// feature, and its Harness / tier and Requires sections. It does not check that the issue is
// a task.
func ReadTask(run Runner, token string, ref Ref) (Task, error) {
	path := fmt.Sprintf("repos/%s/issues/%d", ref.Repo, ref.Number)
	out, err := api(run, token, path)
	if err != nil {
		return Task{}, fmt.Errorf("read %s: %w", ref, err)
	}
	issue, err := decodeOne[apiIssue](out)
	if err != nil {
		return Task{}, fmt.Errorf("read %s: %w", ref, err)
	}
	harness, tier, err := ParseHarnessTier(issue.Body)
	if err != nil {
		return Task{}, fmt.Errorf("read %s: %w", ref, err)
	}
	task := Task{
		Ref: ref, Title: issue.Title, Open: issue.State == "open",
		Labels: issue.labelNames(), Harness: harness, Tier: tier, Requires: ParseRequires(issue.Body),
	}
	if issue.ParentURL != "" {
		parent, err := refFromIssueURL(issue.ParentURL)
		if err != nil {
			return Task{}, fmt.Errorf("read %s: parent: %w", ref, err)
		}
		task.Parent = &parent
	}
	if task.OpenBlockers, err = openBlockers(run, token, ref); err != nil {
		return Task{}, err
	}
	return task, nil
}

// openBlockers lists the issues blocking ref that are not closed.
func openBlockers(run Runner, token string, ref Ref) ([]Ref, error) {
	all, err := blockedBy(run, token, ref)
	if err != nil {
		return nil, err
	}
	var open []Ref
	for _, b := range all {
		if b.Open {
			open = append(open, b.Ref)
		}
	}
	return open, nil
}

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	nextHeading = regexp.MustCompile(`^#{1,2}[ \t]`)
	tierWord    = regexp.MustCompile(`[a-z][a-z0-9_-]*`)
)

// ParseHarnessTier reads a card's `## Harness / tier` section. The words are
// a tier (deep, standard or light) and a harness (any other word), in either
// order. Words start with a letter, so punctuation, bullets and numbering
// between them are ignored. The words "harness" and "tier" are read as labels
// and skipped, and "none" and "default" mean the default harness. An empty or
// absent section is the default harness, which is "", and DefaultTier. A
// second tier or a second harness is an error. A misspelt tier reads as a
// harness, which placement must reject when no host config lists it.
func ParseHarnessTier(body string) (harness, tier string, err error) {
	tier = DefaultTier
	section := tierSection(body)
	var sawTier bool
	for _, word := range tierWord.FindAllString(strings.ToLower(section), -1) {
		switch {
		case slices.Contains(skipWords, word):
		case slices.Contains(tiers, word):
			if sawTier {
				return "", "", fmt.Errorf("the harness / tier section names two tiers (%s and %s)", tier, word)
			}
			tier, sawTier = word, true
		case harness != "":
			return "", "", fmt.Errorf("the harness / tier section names two harnesses (%s and %s)", harness, word)
		default:
			harness = word
		}
	}
	return harness, tier, nil
}

// tierSection returns the text under the Harness / tier heading.
func tierSection(body string) string {
	section, _ := Section(body, "Harness / tier")
	return section
}

// ParseRequires reads a card's `## Requires` section: every word in it is a
// capability the host must declare, in the order written and once, except
// "none". Words start with a letter, so punctuation, bullets and numbering
// between them are ignored. Like engine lint, it does not judge the words: a
// word no host declares is a requirement no host meets. An empty or absent
// section, or "none", requires nothing.
func ParseRequires(body string) []string {
	section, _ := Section(body, "Requires")
	var caps []string
	for _, word := range tierWord.FindAllString(strings.ToLower(section), -1) {
		if word != "none" && !slices.Contains(caps, word) {
			caps = append(caps, word)
		}
	}
	return caps
}
