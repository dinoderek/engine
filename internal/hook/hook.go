// Package hook holds engine's harness hooks: checks that block an action the
// executor's rules forbid, whatever the prompt said. They guard against
// mistakes, not against a session set on getting round them: they read a
// command's words, they do not run a shell.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
)

// event is the part of a Claude Code PreToolUse event the guard reads.
type event struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// PreToolUse reads a Claude Code PreToolUse event from in. When a Bash
// command merges a pull request unsafely it writes the reason to stderr and
// returns 2, which blocks the call and shows the reason to the agent.
// Otherwise it returns 0.
func PreToolUse(in io.Reader, stderr io.Writer) int {
	var e event
	if err := json.NewDecoder(in).Decode(&e); err != nil {
		fmt.Fprintln(stderr, "engine hook: unreadable PreToolUse event:", err)
		return 1
	}
	if e.ToolName != "Bash" {
		return 0
	}
	if reason := MergeProblem(e.ToolInput.Command); reason != "" {
		fmt.Fprintln(stderr, "engine merge guard: "+reason)
		return 2
	}
	return 0
}

var (
	apiMerge = regexp.MustCompile(`(?:^|/)pulls/[^/\s]+/merge/?(?:\?.*)?$`)
	// valueFlags are gh flags that take the next word as their value.
	valueFlags = map[string]bool{"-R": true, "--repo": true, "--hostname": true}
	// wrappers run the command that follows them.
	wrappers = map[string]bool{"sudo": true, "env": true, "command": true, "time": true, "xargs": true, "nohup": true, "exec": true, "timeout": true, "nice": true, "stdbuf": true}
	// keywords open or continue a compound command; a command follows them.
	keywords  = map[string]bool{"if": true, "then": true, "else": true, "elif": true, "do": true, "while": true, "until": true, "{": true, "!": true}
	shells    = map[string]bool{"sh": true, "bash": true, "zsh": true}
	mutations = []string{"mergePullRequest", "enablePullRequestAutoMerge"}
	shellName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// maxDepth bounds how deep the guard reads into `bash -c` and command
// substitutions.
const maxDepth = 4

// MergeProblem returns why command's pull request merge is not allowed, or
// "" when it merges nothing or merges safely.
func MergeProblem(command string) string {
	return problem(command, 0)
}

func problem(command string, depth int) string {
	if depth > maxDepth {
		return ""
	}
	commands, nested := split(command)
	for _, words := range commands {
		if reason := commandProblem(words, depth); reason != "" {
			return reason
		}
		if graphQL(words) && containsMutation(command) {
			return "merging through the GraphQL API is not allowed; use gh pr merge --match-head-commit <sha>"
		}
	}
	for _, inner := range nested {
		if reason := problem(inner, depth+1); reason != "" {
			return reason
		}
	}
	return ""
}

// commandProblem judges one simple command: gh itself, or the script a
// shell's -c or eval runs.
func commandProblem(words []string, depth int) string {
	words = stripPrefix(words)
	if len(words) == 0 {
		return ""
	}
	switch name := path.Base(words[0]); {
	case shells[name]:
		if script, ok := shellScript(words[1:]); ok {
			return problem(script, depth+1)
		}
	case name == "eval":
		return problem(strings.Join(words[1:], " "), depth+1)
	case name == "gh":
		return ghProblem(words[1:])
	}
	return ""
}

// stripPrefix drops leading keywords and variable assignments, and reads
// `engine gh` as gh. After a wrapper command it skips to the first gh or
// shell, since a wrapper's own flags may take values.
func stripPrefix(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case wrappers[path.Base(w)]:
			for i, v := range words {
				if name := path.Base(v); name == "gh" || shells[name] {
					return words[i:]
				}
			}
			return nil
		case keywords[w] || isAssignment(w):
			words = words[1:]
		case path.Base(w) == "engine" && len(words) > 1 && words[1] == "gh":
			return words[1:]
		default:
			return words
		}
	}
	return words
}

// shellScript finds the -c script in a shell's args: after -c or a short
// flag cluster holding c, such as -lc or -euc.
func shellScript(args []string) (string, bool) {
	for i, a := range args {
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") || !strings.Contains(a, "c") {
			continue
		}
		for _, script := range args[i+1:] {
			if !strings.HasPrefix(script, "-") {
				return script, true
			}
		}
	}
	return "", false
}

// graphQL reports whether words run gh api graphql.
func graphQL(words []string) bool {
	words = stripPrefix(words)
	if len(words) == 0 || path.Base(words[0]) != "gh" {
		return false
	}
	rest, ok := ghSubcommand(words[1:], "api")
	return ok && slices.Contains(rest, "graphql")
}

func containsMutation(text string) bool {
	for _, m := range mutations {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

func isAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok {
		return false
	}
	return shellName.MatchString(name)
}

func ghProblem(args []string) string {
	if rest, ok := ghSubcommand(args, "pr", "merge"); ok {
		return mergeFlagsProblem(rest)
	}
	if rest, ok := ghSubcommand(args, "api"); ok {
		if apiMergePut(rest) {
			return "merging through gh api is not allowed; use gh pr merge --match-head-commit <sha>"
		}
	}
	return ""
}

// ghSubcommand reports whether gh's args name the given subcommand, and
// returns the args after it. Flags before or between subcommand words are
// skipped.
func ghSubcommand(args []string, sub ...string) ([]string, bool) {
	matched := 0
	for len(args) > 0 && matched < len(sub) {
		switch {
		case valueFlags[args[0]]:
			args = args[min(2, len(args)):]
		case strings.HasPrefix(args[0], "-"):
			args = args[1:]
		case args[0] == sub[matched]:
			args, matched = args[1:], matched+1
		default:
			return nil, false
		}
	}
	return args, matched == len(sub)
}

func mergeFlagsProblem(args []string) string {
	pinned := false
	for i, a := range args {
		switch {
		case a == "--help" || a == "-h":
			return ""
		case a == "--auto" || (strings.HasPrefix(a, "--auto=") && a != "--auto=false"):
			return "auto-merge is not allowed; merge on green with gh pr merge --match-head-commit <sha>"
		case a == "--admin" || (strings.HasPrefix(a, "--admin=") && a != "--admin=false"):
			return "--admin bypasses the checks; merge only on green"
		case a == "--match-head-commit" && i+1 < len(args) && args[i+1] != "",
			strings.HasPrefix(a, "--match-head-commit=") && len(a) > len("--match-head-commit="):
			pinned = true
		}
	}
	if !pinned {
		return "gh pr merge needs --match-head-commit <sha>, the commit that was reviewed and gated"
	}
	return ""
}

// apiMergePut reports whether gh api args PUT to a pull request's merge
// endpoint. A GET there only asks whether it merged.
func apiMergePut(args []string) bool {
	put, endpoint := false, false
	for i, a := range args {
		switch {
		case (a == "-X" || a == "--method") && i+1 < len(args) && strings.EqualFold(args[i+1], "PUT"),
			strings.EqualFold(a, "-XPUT"), strings.EqualFold(a, "--method=PUT"):
			put = true
		case apiMerge.MatchString(a):
			endpoint = true
		}
	}
	return put && endpoint
}
