package lint

import (
	"regexp"
	"strings"
)

var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	nextHeading = regexp.MustCompile(`^#{1,2}[ \t]`)
)

// Section returns the text under the `## <heading>` line of a task body, up
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
