// Package hook holds engine's harness hooks: checks that block an action the
// executor's rules forbid, whatever the prompt said. They guard against
// mistakes, not against a session set on getting round them.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
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
	// separators split a shell command into simple commands, roughly: the
	// guard reads words, it does not parse shell.
	separators = regexp.MustCompile("&&|\\|\\||[;|&\n()`]")
	apiMerge   = regexp.MustCompile(`(?:^|/)pulls/\d+/merge$`)
	// valueFlags are gh flags that take the next word as their value.
	valueFlags = map[string]bool{"-R": true, "--repo": true, "--hostname": true}
)

// MergeProblem returns why command's pull request merge is not allowed, or
// "" when it merges nothing or merges safely.
func MergeProblem(command string) string {
	if strings.Contains(command, "mergePullRequest") || strings.Contains(command, "enablePullRequestAutoMerge") {
		return "merging through the GraphQL API is not allowed; use gh pr merge --match-head-commit <sha>"
	}
	for _, segment := range separators.Split(command, -1) {
		words := strings.FieldsFunc(segment, func(r rune) bool { return r == ' ' || r == '\t' || r == '"' || r == '\'' })
		if rest, ok := ghSubcommand(words, "pr", "merge"); ok {
			if reason := mergeFlagsProblem(rest); reason != "" {
				return reason
			}
		}
		if rest, ok := ghSubcommand(words, "api"); ok && apiMergePut(rest) {
			return "merging through gh api is not allowed; use gh pr merge --match-head-commit <sha>"
		}
	}
	return ""
}

// ghSubcommand reports whether words run gh with the given subcommand, and
// returns the words after it. Flags before or between subcommand words are
// skipped.
func ghSubcommand(words []string, sub ...string) ([]string, bool) {
	for i, w := range words {
		if path.Base(w) != "gh" {
			continue
		}
		rest, matched := words[i+1:], 0
		for len(rest) > 0 && matched < len(sub) {
			switch {
			case valueFlags[rest[0]]:
				rest = rest[min(2, len(rest)):]
			case strings.HasPrefix(rest[0], "-"):
				rest = rest[1:]
			case rest[0] == sub[matched]:
				rest, matched = rest[1:], matched+1
			default:
				return nil, false
			}
		}
		return rest, matched == len(sub)
	}
	return nil, false
}

func mergeFlagsProblem(args []string) string {
	pinned := false
	for i, a := range args {
		switch {
		case a == "--auto" || (strings.HasPrefix(a, "--auto=") && a != "--auto=false"):
			return "auto-merge is not allowed; merge on green with gh pr merge --match-head-commit <sha>"
		case a == "--admin" || (strings.HasPrefix(a, "--admin=") && a != "--admin=false"):
			return "--admin bypasses the checks; merge only on green"
		case a == "--match-head-commit" && i+1 < len(args), strings.HasPrefix(a, "--match-head-commit="):
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
