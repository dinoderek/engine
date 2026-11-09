package checks

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/dinoderek/engine/devtools/internal/repo"
)

// HarnessNames fails when non-test Go code under the -in prefixes, outside
// the -allow prefixes, names a harness from -names: in a string literal or an
// identifier, in any case. Comments are not checked. All three flags take
// comma-separated values; a prefix matches the start of the file's path.
func HarnessNames(e Env, args []string) int {
	const lane = "harness-names"
	var names, in, allow string
	if err := e.flags(lane, args, func(fs *flag.FlagSet) {
		fs.StringVar(&names, "names", "", "comma-separated harness names (required)")
		fs.StringVar(&in, "in", "", "comma-separated path prefixes to check (required)")
		fs.StringVar(&allow, "allow", "", "comma-separated path prefixes that may name a harness")
	}); err != nil {
		return 2
	}
	words, roots, allowed := splitList(strings.ToLower(names)), splitList(in), splitList(allow)
	if len(words) == 0 || len(roots) == 0 {
		fmt.Fprintln(e.Out, "usage: dev check harness-names -names <names> -in <prefixes> [-allow <prefixes>]")
		return 2
	}
	files, err := repo.Files(e.Run, e.Root)
	if err != nil {
		return e.fail(lane, err)
	}
	var problems []string
	checked := 0
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") ||
			!hasPrefix(f, roots) || hasPrefix(f, allowed) {
			continue
		}
		found, err := harnessNamesIn(filepath.Join(e.Root, f), f, words)
		if err != nil {
			return e.fail(lane, err)
		}
		checked++
		problems = append(problems, found...)
	}
	return e.report(lane, problems, fmt.Sprintf("%d Go files name no harness", checked))
}

// harnessNamesIn parses one file, without its comments, and lists every
// string literal and identifier that holds one of words.
func harnessNamesIn(path, rel string, words []string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var problems []string
	add := func(pos token.Pos, kind, text string) {
		for _, w := range words {
			if strings.Contains(strings.ToLower(text), w) {
				problems = append(problems, fmt.Sprintf("%s:%d: %s %s names harness %q; move it behind the harness adapter",
					rel, fset.Position(pos).Line, kind, text, w))
				return
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				add(n.Pos(), "string literal", n.Value)
			}
		case *ast.Ident:
			add(n.Pos(), "identifier", n.Name)
		}
		return true
	})
	return problems, nil
}

// splitList splits a comma-separated flag value, dropping empty items.
func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
