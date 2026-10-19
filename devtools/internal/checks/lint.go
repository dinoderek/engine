package checks

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dinoderek/engine/devtools/internal/baseline"
)

// LintBaseline holds the tolerated lint findings per file and linter.
const LintBaseline = "scripts/lint-baseline.tsv"

// nolintLinter is the baseline key for //nolint directives: each one is a
// suppression, so it is counted like a finding.
const nolintLinter = "nolint"

type lintReport struct {
	Issues []struct {
		FromLinter string
		Text       string
		Pos        struct {
			Filename string
			Line     int
		}
	}
}

// Lint runs golangci-lint (configured in .golangci.yml) and fails on any
// finding or //nolint directive beyond scripts/lint-baseline.tsv.
func Lint(e Env) int {
	const lane = "lint"
	tmp, err := os.MkdirTemp("", "engine-lint-")
	if err != nil {
		return e.fail(lane, err)
	}
	defer removeAll(tmp)
	jsonPath := filepath.Join(tmp, "report.json")
	out, err := e.Run(e.Root, "go", "tool", "golangci-lint", "run",
		"--issues-exit-code=0", "--max-issues-per-linter=0", "--max-same-issues=0",
		"--output.text.path=stdout", "--output.json.path="+jsonPath, "./...")
	fmt.Fprint(e.Out, out)
	if err != nil {
		return e.fail(lane, err)
	}
	found, err := lintFindings(jsonPath)
	if err != nil {
		return e.fail(lane, err)
	}
	if err := e.countNolint(found); err != nil {
		return e.fail(lane, err)
	}
	base, err := e.readBaseline(LintBaseline, 2)
	if err != nil {
		return e.fail(lane, err)
	}
	return e.report(lane, baseline.Compare(LintBaseline, base, found), "no findings beyond the baseline")
}

func lintFindings(jsonPath string) (baseline.Counts, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, err
	}
	var report lintReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("golangci-lint JSON report: %w", err)
	}
	found := baseline.Counts{}
	for _, issue := range report.Issues {
		found[filepath.ToSlash(issue.Pos.Filename)+"\t"+issue.FromLinter]++
	}
	return found, nil
}

// countNolint adds every //nolint directive in the Go files to found.
func (e Env) countNolint(found baseline.Counts) error {
	files, err := e.goFiles()
	if err != nil {
		return err
	}
	for _, f := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(e.Root, f), nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, group := range parsed.Comments {
			for _, c := range group.List {
				if isNolint(c.Text) {
					found[f+"\t"+nolintLinter]++
				}
			}
		}
	}
	return nil
}

// nolintDirective is golangci-lint's own test for a suppression comment,
// applied after it strips the comment's leading slashes and spaces.
var nolintDirective = regexp.MustCompile(`^nolint( |:|$)`)

// isNolint reports whether a comment's text is a nolint directive.
func isNolint(comment string) bool {
	return nolintDirective.MatchString(strings.TrimLeft(comment, "/ "))
}
