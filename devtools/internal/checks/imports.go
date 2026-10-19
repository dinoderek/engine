package checks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/devtools/internal/baseline"
	"github.com/dinoderek/engine/devtools/internal/repo"
)

// The import-direction registry and its baseline.
const (
	ImportRules    = "scripts/import-rules.tsv"
	ImportBaseline = "scripts/import-baseline.tsv"
)

// importRule constrains the first-party imports of the packages it matches.
type importRule struct {
	line     int
	importer string
	only     bool // true: may import only targets; false: may never import them
	targets  []string
}

// Imports enforces scripts/import-rules.tsv on the module's non-test imports.
// The Go compiler already rejects import cycles.
func Imports(e Env) int {
	const lane = "imports"
	text, err := os.ReadFile(filepath.Join(e.Root, ImportRules))
	if err != nil {
		return e.fail(lane, err)
	}
	rules, err := parseImportRules(string(text))
	if err != nil {
		return e.fail(lane, err)
	}
	module, err := e.Run(e.Root, "go", "list", "-m")
	if err != nil {
		return e.fail(lane, err)
	}
	listing, err := e.Run(e.Root, "go", "list", "-json", "./...")
	if err != nil {
		return e.fail(lane, fmt.Errorf("%w\n%s", err, listing))
	}
	graph, err := firstPartyGraph(strings.TrimSpace(module), listing)
	if err != nil {
		return e.fail(lane, err)
	}
	found, problems := checkImports(rules, graph)
	base, err := e.readBaseline(ImportBaseline, 2)
	if err != nil {
		return e.fail(lane, err)
	}
	problems = append(problems, baseline.Compare(ImportBaseline, base, found)...)
	return e.report(lane, problems, fmt.Sprintf("%d packages follow %d rules", len(graph), len(rules)))
}

func parseImportRules(text string) ([]importRule, error) {
	var rules []importRule
	for _, row := range repo.Rows(text) {
		f := row.Fields
		if len(f) != 3 || (f[1] != "only" && f[1] != "forbid") {
			return nil, fmt.Errorf("%s:%d: want '<importer>\\t<only|forbid>\\t<targets>'", ImportRules, row.Line)
		}
		var targets []string
		if f[2] != "-" {
			targets = strings.Split(f[2], ",")
		}
		rules = append(rules, importRule{line: row.Line, importer: f[0], only: f[1] == "only", targets: targets})
	}
	return rules, nil
}

// firstPartyGraph maps each package, as a path relative to the module ("."
// for the module root), to its first-party imports.
func firstPartyGraph(module, listing string) (map[string][]string, error) {
	rel := func(p string) (string, bool) {
		if p == module {
			return ".", true
		}
		r, ok := strings.CutPrefix(p, module+"/")
		return r, ok
	}
	graph := map[string][]string{}
	dec := json.NewDecoder(strings.NewReader(listing))
	for {
		var pkg struct {
			ImportPath string
			Imports    []string
		}
		if err := dec.Decode(&pkg); errors.Is(err, io.EOF) {
			return graph, nil
		} else if err != nil {
			return nil, fmt.Errorf("go list -json: %w", err)
		}
		name, ok := rel(pkg.ImportPath)
		if !ok {
			continue
		}
		graph[name] = nil
		for _, imp := range pkg.Imports {
			if r, ok := rel(imp); ok {
				graph[name] = append(graph[name], r)
			}
		}
	}
}

// matches reports whether pkg matches pattern; "x/..." matches x and every
// package below it.
func matches(pattern, pkg string) bool {
	if base, ok := strings.CutSuffix(pattern, "/..."); ok {
		return pkg == base || strings.HasPrefix(pkg, base+"/")
	}
	return pattern == pkg
}

func matchesAny(patterns []string, pkg string) bool {
	for _, p := range patterns {
		if matches(p, pkg) {
			return true
		}
	}
	return false
}

func matchesAnyPackage(pattern string, graph map[string][]string) bool {
	for pkg := range graph {
		if matches(pattern, pkg) {
			return true
		}
	}
	return false
}

// checkImports returns each violating import as a baseline key, plus a
// problem for every importer or target that matches no package (a stale or
// mistyped rule).
func checkImports(rules []importRule, graph map[string][]string) (baseline.Counts, []string) {
	found := baseline.Counts{}
	var problems []string
	for _, r := range rules {
		used := false
		for pkg, imports := range graph {
			if !matches(r.importer, pkg) {
				continue
			}
			used = true
			for _, imp := range imports {
				if r.only != matchesAny(r.targets, imp) {
					found[pkg+"\t"+imp] = 1
				}
			}
		}
		if !used {
			problems = append(problems, fmt.Sprintf("%s:%d: importer %s matches no package", ImportRules, r.line, r.importer))
		}
		for _, target := range r.targets {
			if !matchesAnyPackage(target, graph) {
				problems = append(problems, fmt.Sprintf("%s:%d: target %s matches no package", ImportRules, r.line, target))
			}
		}
	}
	return found, problems
}
