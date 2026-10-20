package checks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const header = "> **Owns:** a thing. **Not here:** other things.\n"

// drafts is the fixtures' ephemeral directory. Fixture paths are built from
// it, and tracker-like fixtures are split, so this file passes the check it
// tests.
const drafts = "design/"

// goodDocs is a tree that follows every doc rule.
func goodDocs() map[string]string {
	return map[string]string{
		"AGENTS.md": "# AGENTS.md\n\n" + header + "\nRun `./dev test unit` or `./dev test for x.go`.\n\n" +
			"## Always load\n\n- `docs/a.md`: always.\n\n" +
			"## Load on demand\n\n| If | Load |\n| --- | --- |\n| b | [b](docs/b.md#part) |\n| drafts | `design/` |\n",
		"README.md": "# engine\n\n" + header + "\nStart at `AGENTS.md`.\n",
		"docs/a.md": "# A\n\n" + header + "\nIf you change b, load `b.md`. Lanes: `scripts/lanes.tsv`, " +
			"output `out/summary.md` in `out/`, branch `origin/main`, `internal/...`, `<placeholder>/x.md`.\n" +
			"Also [angle](<b.md>), [titled](b.md \"B\"), `127.0.0.1:8080`, `gopkg.in/yaml.v3`, `#<task>`.\n" +
			"[ref]: b.md\n" +
			"```\nnot/a/path.md inside a fence\n```\n",
		"docs/b.md":           "# B\n\n" + header + "\n[up](../AGENTS.md) and [web](https://example.com/x.md).\n",
		drafts + "draft.md":   "no header, cites #" + "12 and " + drafts + "x.md\n",
		".github/template.md": "Closes #<task>\n",
		".github/ci.yml":      "on: push\n",
		"scripts/lanes.tsv":   "unit\trequired\t*.go\tgo test ./...\n",
		"scripts/doc-budgets.tsv": "budget\t*\t100\nbudget\tAGENTS.md\t200\n" +
			"set\talways-load\t400\tAGENTS.md,docs/a.md\n" +
			"ephemeral\tdesign/\nexempt\t.github/\n",
		"main.go": "package main // see the docs\n",
	}
}

// docsTree builds files plus the CLAUDE.md symlink. git check-ignore reports
// only paths under out/ as ignored.
func docsTree(t *testing.T, files map[string]string) (Env, func() string) {
	t.Helper()
	e, out := tree(t, files, func(name string, args []string) (string, error) {
		if name == "git" && args[0] == "check-ignore" && strings.HasPrefix(args[len(args)-1], "out/") {
			return "", nil
		}
		return "", errors.New("exit 1")
	})
	if err := os.Symlink("AGENTS.md", filepath.Join(e.Root, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	return e, out.String
}

func TestDocsPass(t *testing.T) {
	e, out := docsTree(t, goodDocs())
	if code := Docs(e); code != 0 {
		t.Fatalf("exit %d\n%s", code, out())
	}
	for _, want := range []string{"4 docs follow the doc rules", "set always-load", "/   200 words  AGENTS.md"} {
		if !strings.Contains(out(), want) {
			t.Errorf("output lacks %q:\n%s", want, out())
		}
	}
}

func TestDocsFailures(t *testing.T) {
	tests := map[string]struct {
		edit func(files map[string]string)
		want string
	}{
		"over budget": {func(f map[string]string) { f["docs/b.md"] += strings.Repeat("word ", 100) },
			"docs/b.md: 1"},
		"stale budget row": {func(f map[string]string) { f["scripts/doc-budgets.tsv"] += "budget\tdocs/gone.md\t5\n" },
			"budget row for docs/gone.md"},
		"set over budget": {func(f map[string]string) {
			f["scripts/doc-budgets.tsv"] = strings.Replace(f["scripts/doc-budgets.tsv"], "\t400\t", "\t10\t", 1)
		}, "set always-load: "},
		"orphan doc": {func(f map[string]string) { f["docs/c.md"] = "# C\n\n" + header },
			`docs/c.md is in neither "## Always load" nor "## Load on demand"`},
		"set lists a doc not under Always load": {func(f map[string]string) {
			f["scripts/doc-budgets.tsv"] = strings.Replace(f["scripts/doc-budgets.tsv"], "docs/a.md", "docs/a.md,docs/b.md", 1)
		}, "docs/b.md is in the always-load set but not listed"},
		"Always load lists a doc not in the set": {func(f map[string]string) {
			f["AGENTS.md"] = strings.Replace(f["AGENTS.md"], "- `docs/a.md`: always.", "- `docs/a.md`, `docs/b.md`.", 1)
		}, "docs/b.md is listed under \"## Always load\" but is not in the always-load set"},
		"no header":        {func(f map[string]string) { f["docs/b.md"] = "# B\n\ntext\n" }, "docs/b.md: no `> **Owns:**"},
		"malformed header": {func(f map[string]string) { f["docs/b.md"] = "# B\n\n> **Owns:** only.\n" }, "header must read"},
		"two-line header":  {func(f map[string]string) { f["docs/b.md"] = "# B\n\n" + header + "> more\n" }, "header must be one line"},
		"load trigger in header": {func(f map[string]string) {
			f["docs/b.md"] = "# B\n\n> **Owns:** x. **Not here:** y. **Load when:** z.\n"
		}, "header carries no load trigger"},
		"broken link":             {func(f map[string]string) { f["docs/b.md"] += "[x](missing.md)\n" }, "docs/b.md:6: cited path does not exist: missing.md"},
		"missing path":            {func(f map[string]string) { f["docs/b.md"] += "`scripts/nope.tsv`\n" }, "cited path does not exist: scripts/nope.tsv"},
		"line citation":           {func(f map[string]string) { f["docs/b.md"] += "`main.go:12`\n" }, "cite a file, never a line: main.go:12"},
		"unknown lane":            {func(f map[string]string) { f["docs/b.md"] += "Run `./dev test nope`.\n" }, "./dev test nope names no lane"},
		"issue number":            {func(f map[string]string) { f["docs/b.md"] += "Fixed in #" + "42.\n" }, `tracker reference "#` + `42"`},
		"repo issue":              {func(f map[string]string) { f["main.go"] += "// owner/repo#" + "7\n" }, "main.go:2: tracker reference"},
		"issue URL":               {func(f map[string]string) { f["docs/b.md"] += "https://github.com/o/r/" + "pull/3\n" }, "tracker reference"},
		"plan path":               {func(f map[string]string) { f["main.go"] += "// see " + drafts + "pl" + "ans/m1/01-x.md\n" }, `tracker reference "/plans/`},
		"ephemeral file":          {func(f map[string]string) { f["main.go"] += "// " + drafts + "draft.md\n" }, "cites a file in design/, which is ephemeral"},
		"line citation link":      {func(f map[string]string) { f["docs/b.md"] += "[x](b.md#L" + "3)\n" }, "cite a file, never a line: b.md#L3"},
		"reference definition":    {func(f map[string]string) { f["docs/b.md"] += "[r]: nowhere.md\n" }, "does not exist: nowhere.md"},
		"titled link":             {func(f map[string]string) { f["docs/b.md"] += "[x](nowhere.md \"t\")\n" }, "does not exist: nowhere.md"},
		"href":                    {func(f map[string]string) { f["docs/b.md"] += "<a href=\"nowhere.md\">x</a>\n" }, "does not exist: nowhere.md"},
		"PR word":                 {func(f map[string]string) { f["docs/b.md"] += "Since PR#" + "12.\n" }, "tracker reference"},
		"issue word":              {func(f map[string]string) { f["main.go"] += "// issue " + "42\n" }, "tracker reference"},
		"GH number":               {func(f map[string]string) { f["docs/b.md"] += "See GH-" + "9.\n" }, "tracker reference"},
		"relative URL":            {func(f map[string]string) { f["main.go"] += "// ../../iss" + "ues/12\n" }, "tracker reference"},
		"exempt workflow":         {func(f map[string]string) { f[".github/ci.yml"] += "# works around #" + "123\n" }, ".github/ci.yml:2: tracker reference"},
		"relative ephemeral link": {func(f map[string]string) { f["docs/b.md"] += "[q](../" + drafts + "draft.md)\n" }, "docs/b.md:6: cites a file in design/"},
		"dot-relative ephemeral":  {func(f map[string]string) { f["main.go"] += "// ./" + drafts + "draft.md\n" }, "main.go:2: cites a file in design/"},
		"duplicate set":           {func(f map[string]string) { f["scripts/doc-budgets.tsv"] += "set\talways-load\t1\tAGENTS.md\n" }, "malformed row"},
		"no entry doc":            {func(f map[string]string) { delete(f, "AGENTS.md") }, "AGENTS.md is missing"},
		"no always-load set": {func(f map[string]string) {
			f["scripts/doc-budgets.tsv"] = "budget\t*\t100\n"
		}, `no "always-load" set`},
		"set member missing": {func(f map[string]string) {
			f["scripts/doc-budgets.tsv"] += "set\tother\t10\tdocs/gone/\n"
		}, "set other: member docs/gone/ is not a persistent doc"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			files := goodDocs()
			tt.edit(files)
			e, out := docsTree(t, files)
			if code := Docs(e); code != 1 || !strings.Contains(out(), tt.want) {
				t.Errorf("exit %d, want 1 and %q in:\n%s", code, tt.want, out())
			}
		})
	}
}

func TestDocsNeedsTheSymlink(t *testing.T) {
	e, out := tree(t, goodDocs(), func(string, []string) (string, error) { return "", errors.New("exit 1") })
	if code := Docs(e); code != 1 || !strings.Contains(out.String(), "CLAUDE.md must be a symlink to AGENTS.md") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestDocsAcceptsADotSlashSymlink(t *testing.T) {
	e, out := docsTree(t, goodDocs())
	link := filepath.Join(e.Root, "CLAUDE.md")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("./AGENTS.md", link); err != nil {
		t.Fatal(err)
	}
	if code := Docs(e); code != 0 {
		t.Errorf("exit %d:\n%s", code, out())
	}
}

func TestDocsRegistryErrors(t *testing.T) {
	for _, registry := range []string{"budget\t*\tx\n", "set\ta\tx\tb\n", "bogus\trow\n", "unindexed\tAGENTS.md\n"} {
		files := goodDocs()
		files["scripts/doc-budgets.tsv"] = registry
		e, out := docsTree(t, files)
		if code := Docs(e); code != 1 || !strings.Contains(out(), DocBudgets) {
			t.Errorf("registry %q: exit %d\n%s", registry, code, out())
		}
	}
	files := goodDocs()
	delete(files, "scripts/lanes.tsv")
	e, out := docsTree(t, files)
	if code := Docs(e); code != 1 {
		t.Errorf("no lane registry: exit %d\n%s", code, out())
	}
}
