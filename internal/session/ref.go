package session

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var refRepo = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// Ref names a task by its repo and number: the form a run record keeps,
// owner/repo#n.
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
	if !ok || err != nil || n < 1 || !refRepo.MatchString(repo) {
		return Ref{}, fmt.Errorf("want owner/repo#n, got %q", s)
	}
	return Ref{Repo: repo, Number: n}, nil
}
