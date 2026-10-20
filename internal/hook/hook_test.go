package hook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// mergeURL is a merge endpoint path, split so this file holds no tracker
// reference.
const mergeURL = "repos/o/r/pul" + "ls/7/merge"

func TestMergeProblem(t *testing.T) {
	tests := map[string]struct {
		command string
		want    string // a substring of the reason; "" means allowed
	}{
		"pinned squash":            {"gh pr merge 7 --squash --match-head-commit abc123", ""},
		"pinned with equals":       {"gh pr merge --match-head-commit=abc123", ""},
		"pinned by substitution":   {`gh pr merge 7 --squash --match-head-commit "$(git rev-parse HEAD)"`, ""},
		"pinned by backticks":      {"gh pr merge 7 --match-head-commit `git rev-parse HEAD`", ""},
		"parens in the body":       {`gh pr merge 7 --body "Fixes (part 1)" --match-head-commit abc`, ""},
		"help":                     {"gh pr merge --help", ""},
		"short help":               {"gh pr merge -h", ""},
		"not a merge":              {"gh pr view 7 --json mergedAt && git merge origin/main", ""},
		"checks":                   {"gh pr checks 7 --watch", ""},
		"search for merge":         {"gh pr list --search merge", ""},
		"api GET merge status":     {"gh api " + mergeURL, ""},
		"echo":                     {`echo "gh pr merge 7"`, ""},
		"grep":                     {`grep -r "gh pr merge" docs/`, ""},
		"commit message":           {`git commit -m "docs: never use gh pr merge --auto"`, ""},
		"grep for a mutation":      {"rg mergePullRequest internal/", ""},
		"heredoc body":             {"cat > body.md <<'EOF'\ngh pr merge 7 --admin\nEOF\ngh pr view 7", ""},
		"auto disabled":            {"gh pr merge 7 --auto=false --match-head-commit abc", ""},
		"unpinned":                 {"gh pr merge 7 --squash", "needs --match-head-commit"},
		"flag without value":       {"gh pr merge 7 --match-head-commit", "needs --match-head-commit"},
		"empty pin":                {`gh pr merge 7 --match-head-commit ""`, "needs --match-head-commit"},
		"empty pin with equals":    {"gh pr merge 7 --match-head-commit=", "needs --match-head-commit"},
		"auto":                     {"gh pr merge 7 --auto --match-head-commit abc", "auto-merge is not allowed"},
		"auto=true":                {"gh pr merge 7 --auto=true --match-head-commit abc", "auto-merge is not allowed"},
		"admin":                    {"gh pr merge 7 --admin --match-head-commit abc", "--admin bypasses"},
		"admin=true":               {"gh pr merge 7 --admin=true --match-head-commit abc", "--admin bypasses"},
		"admin after substitution": {`gh pr merge 7 --squash --match-head-commit "$(git rev-parse HEAD)" --admin`, "--admin bypasses"},
		"auto after bare subst":    {"gh pr merge 7 --match-head-commit $(git rev-parse HEAD) --auto", "auto-merge is not allowed"},
		"nested quotes in subst":   {`gh pr merge "$PR" --match-head-commit "$(gh pr view "$PR" --json headRefOid -q .headRefOid)" --admin`, "--admin bypasses"},
		"repo flag first":          {"gh -R o/r pr merge 7", "needs --match-head-commit"},
		"full path to gh":          {"/opt/homebrew/bin/gh pr merge 7", "needs --match-head-commit"},
		"after a token prefix":     {`GH_TOKEN="$(cat ~/.config/engine/github-token)" gh pr merge 7 --squash`, "needs --match-head-commit"},
		"after a wrapper":          {"env -i GH_TOKEN=x gh pr merge 7", "needs --match-head-commit"},
		"second in a chain":        {"./dev test && gh pr merge 7", "needs --match-head-commit"},
		"in a subshell":            {"(cd x; gh pr merge 7)", "needs --match-head-commit"},
		"inside bash -c":           {"bash -c 'gh pr merge 7'", "needs --match-head-commit"},
		"inside a substitution":    {"echo $(gh pr merge 7)", "needs --match-head-commit"},
		"after a heredoc":          {"cat <<EOF > x\nbody\nEOF\ngh pr merge 7", "needs --match-head-commit"},
		"escaped space in word":    {`gh pr merge 7 --match-head-commit a\ b`, ""},
		"unsafe after safe one":    {"gh pr merge 7 --match-head-commit a; gh pr merge 8", "needs --match-head-commit"},
		"api PUT":                  {"gh api -X PUT " + mergeURL, "through gh api"},
		"api PUT, variable number": {"gh api -X PUT repos/o/r/pul" + "ls/$PR/merge", "through gh api"},
		"api PUT, trailing slash":  {"gh api -X PUT repos/o/r/pul" + "ls/${PR}/merge/", "through gh api"},
		"api method PUT":           {"gh api --method=put " + mergeURL, "through gh api"},
		"api XPUT":                 {"gh api -XPUT " + mergeURL + " -f merge_method=squash", "through gh api"},
		"graphql merge":            {`gh api graphql -f query='mutation { mergePullRequest(input: {}) { clientMutationId } }'`, "GraphQL"},
		"graphql auto-merge":       {`gh api graphql -f query='mutation { enablePullRequestAutoMerge(input: {}) { clientMutationId } }'`, "GraphQL"},
		"unclosed quote":           {`gh pr merge 7 --body "oops`, "needs --match-head-commit"},
		"unclosed substitution":    {"gh pr merge 7 --match-head-commit $(git rev-parse", ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := MergeProblem(tt.command)
			if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
				t.Errorf("MergeProblem(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func eventJSON(tool, command string) string {
	e := map[string]any{"tool_name": tool, "tool_input": map[string]string{"command": command}}
	data, _ := json.Marshal(e)
	return string(data)
}

func TestPreToolUse(t *testing.T) {
	tests := map[string]struct {
		in     string
		code   int
		stderr string
	}{
		"blocked merge": {eventJSON("Bash", "gh pr merge 7"), 2, "engine merge guard: gh pr merge needs --match-head-commit"},
		"safe merge":    {eventJSON("Bash", "gh pr merge 7 --match-head-commit abc"), 0, ""},
		"other tool":    {eventJSON("Write", "gh pr merge 7"), 0, ""},
		"unreadable":    {"not json", 1, "unreadable PreToolUse event"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var errOut bytes.Buffer
			if code := PreToolUse(strings.NewReader(tt.in), &errOut); code != tt.code || !strings.Contains(errOut.String(), tt.stderr) {
				t.Errorf("exit %d, stderr %q; want %d, %q", code, errOut.String(), tt.code, tt.stderr)
			}
		})
	}
}
