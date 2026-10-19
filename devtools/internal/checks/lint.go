package checks

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		file, err := os.Open(filepath.Join(e.Root, f))
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if isNolint(scanner.Text()) {
				found[f+"\t"+nolintLinter]++
			}
		}
		closeErr := file.Close()
		if err := errors.Join(scanner.Err(), closeErr); err != nil {
			return err
		}
	}
	return nil
}

// isNolint reports whether a line's comment is a //nolint directive. The
// comment starts at the line's first "//", which is close enough for Go
// source that keeps such text out of string literals.
func isNolint(line string) bool {
	i := strings.Index(line, "//")
	return i >= 0 && strings.HasPrefix(line[i+2:], "nolint")
}
