package hook

import (
	"path"
	"slices"
)

// IsMerge detects gh merge invocations, including pinned ones: a server session makes none.
func IsMerge(command string) bool { return isMerge(command, 0) }

func isMerge(command string, depth int) bool {
	if depth > maxDepth {
		return false
	}
	commands, nested := split(command)
	for _, words := range commands {
		if mergeWords(words, command, depth) {
			return true
		}
	}
	for _, inner := range nested {
		if isMerge(inner, depth+1) {
			return true
		}
	}
	return false
}

func mergeWords(words []string, command string, depth int) bool {
	words = stripPrefix(words)
	if len(words) == 0 {
		return false
	}
	name := path.Base(words[0])
	if name == "gh" {
		if rest, ok := ghSubcommand(words[1:], "pr", "merge"); ok && !slices.Contains(rest, "--help") && !slices.Contains(rest, "-h") {
			return true
		}
		return graphQL(words) && containsMutation(command)
	}
	if shells[name] {
		script, ok := shellScript(words[1:])
		return ok && isMerge(script, depth+1)
	}
	return name == "eval" && isMerge(joinWords(words[1:]), depth+1)
}

func joinWords(words []string) string {
	s := ""
	for i, w := range words {
		if i > 0 {
			s += " "
		}
		s += w
	}
	return s
}
