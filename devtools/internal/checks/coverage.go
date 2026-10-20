package checks

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// fileCoverage counts statements in one source file.
type fileCoverage struct {
	File           string
	Covered, Total int
}

func (c fileCoverage) percent() float64 {
	if c.Total == 0 {
		return 100
	}
	return 100 * float64(c.Covered) / float64(c.Total)
}

// Coverage runs the whole test suite with statement coverage across every
// package in the module and fails when the total is below -min percent.
func Coverage(e Env, args []string) int {
	const lane = "coverage"
	var minimum float64
	if err := e.flags(lane, args, func(fs *flag.FlagSet) {
		fs.Float64Var(&minimum, "min", -1, "minimum statement coverage, in percent (required)")
	}); err != nil {
		return 2
	}
	if minimum < 0 {
		fmt.Fprintln(e.Out, "usage: dev check coverage -min <percent>")
		return 2
	}
	tmp, err := os.MkdirTemp("", "engine-coverage-")
	if err != nil {
		return e.fail(lane, err)
	}
	defer removeAll(tmp)
	profile := filepath.Join(tmp, "coverage.out")
	out, err := e.Run(e.Root, "go", "test", "-count=1", "-covermode=set",
		"-coverpkg=./...", "-coverprofile="+profile, "./...")
	if err != nil {
		fmt.Fprint(e.Out, out)
		return e.fail(lane, err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		return e.fail(lane, err)
	}
	files, total, err := parseProfile(string(data))
	if err != nil {
		return e.fail(lane, err)
	}
	for _, f := range files {
		if f.percent() < minimum {
			fmt.Fprintf(e.Out, "%s: %5.1f%% of %d statements, %s\n", lane, f.percent(), f.Total, f.File)
		}
	}
	summary := fmt.Sprintf("%.1f%% of %d statements covered (minimum %.0f%%)", total.percent(), total.Total, minimum)
	if total.percent() < minimum {
		return e.report(lane, []string{summary + "; add tests for the files above"}, "")
	}
	return e.report(lane, nil, summary)
}

// parseProfile merges a cover profile's blocks, which repeat once per test
// binary under -coverpkg, and returns per-file counts, lowest coverage first,
// plus the total.
func parseProfile(text string) ([]fileCoverage, fileCoverage, error) {
	type block struct {
		stmts   int
		covered bool
	}
	blocks := map[string]block{}
	for i, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if i == 0 && strings.HasPrefix(line, "mode:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fileCoverage{}, fmt.Errorf("cover profile line %d: %q", i+1, line)
		}
		stmts, err1 := strconv.Atoi(f[1])
		count, err2 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil {
			return nil, fileCoverage{}, fmt.Errorf("cover profile line %d: %q", i+1, line)
		}
		b := blocks[f[0]]
		blocks[f[0]] = block{stmts: stmts, covered: b.covered || count > 0}
	}
	perFile := map[string]*fileCoverage{}
	var total fileCoverage
	for pos, b := range blocks {
		name := pos[:strings.LastIndex(pos, ":")]
		fc := perFile[name]
		if fc == nil {
			fc = &fileCoverage{File: name}
			perFile[name] = fc
		}
		fc.Total += b.stmts
		total.Total += b.stmts
		if b.covered {
			fc.Covered += b.stmts
			total.Covered += b.stmts
		}
	}
	files := make([]fileCoverage, 0, len(perFile))
	for _, fc := range perFile {
		files = append(files, *fc)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].percent() != files[j].percent() {
			return files[i].percent() < files[j].percent()
		}
		return files[i].File < files[j].File
	})
	return files, total, nil
}
