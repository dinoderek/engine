package checks

import (
	"strings"
	"testing"
)

var harnessArgs = []string{"-names", "claude,codex", "-in", "cmd/,internal/", "-allow", "internal/harness/"}

const literalLeak = `package spawn

// Pick says which harness a Codex run uses: comments may name one.
func Pick(h string) bool {
	if h == "codex" {
		return true
	}
	return false
}
`

const identLeak = `package spawn

var claudeBin = "bin"
`

func TestHarnessNamesFailsOnAStringLiteral(t *testing.T) {
	e, out := tree(t, map[string]string{"internal/spawn/pick.go": literalLeak}, nil)
	expect(t, HarnessNames(e, harnessArgs), 1, out,
		`internal/spawn/pick.go:5: string literal "codex" names harness "codex"`, "1 problem(s)")
}

func TestHarnessNamesFailsOnAnIdentifier(t *testing.T) {
	e, out := tree(t, map[string]string{"cmd/engine/main.go": identLeak}, nil)
	expect(t, HarnessNames(e, harnessArgs), 1, out,
		`cmd/engine/main.go:3: identifier claudeBin names harness "claude"`, "1 problem(s)")
}

func TestHarnessNamesPassesCleanAndAllowedCode(t *testing.T) {
	files := map[string]string{
		"internal/spawn/clean.go":        "package spawn\n\n// Claude and Codex in a comment are fine.\nvar bin = \"engine\"\n",
		"internal/harness/codex/pick.go": literalLeak,
		"internal/harness/claude/bin.go": identLeak,
		"internal/spawn/pick_test.go":    literalLeak,
		"devtools/codex/pick.go":         literalLeak,
		"internal/spawn/notes.md":        "codex",
	}
	e, out := tree(t, files, nil)
	expect(t, HarnessNames(e, harnessArgs), 0, out, "1 Go files name no harness")
}

func TestHarnessNamesUsageAndParseErrors(t *testing.T) {
	e, out := tree(t, map[string]string{"internal/bad.go": "package"}, nil)
	expect(t, HarnessNames(e, nil), 2, out, "usage: dev check harness-names")
	expect(t, HarnessNames(e, []string{"-bogus"}), 2, out)
	out.Reset()
	expect(t, HarnessNames(e, harnessArgs), 1, out, "internal/bad.go")
	if strings.Contains(out.String(), "name no harness") {
		t.Errorf("a parse error reported success:\n%s", out)
	}
}

func TestRunDispatchesHarnessNames(t *testing.T) {
	e, out := tree(t, map[string]string{"internal/a.go": identLeak}, nil)
	expect(t, Run(e, "harness-names", harnessArgs), 1, out, "identifier claudeBin")
}
