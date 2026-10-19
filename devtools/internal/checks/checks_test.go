package checks

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dinoderek/engine/devtools/internal/repo"
)

// tree writes files under a fresh root and returns an Env whose runner
// answers git ls-files with those files and sends every other command to
// handle.
func tree(t *testing.T, files map[string]string, handle func(name string, args []string) (string, error)) (Env, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	var names []string
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	run := func(_, name string, args ...string) (string, error) {
		if name == "git" {
			return strings.Join(names, "\x00"), nil
		}
		if handle == nil {
			return "", errors.New("unexpected command " + name)
		}
		return handle(name, args)
	}
	out := &bytes.Buffer{}
	return Env{Root: root, Run: run, Out: out, GoVersion: "go1.27.2"}, out
}

// flagValue returns the value of a --name=value argument.
func flagValue(args []string, prefix string) string {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, prefix); ok {
			return v
		}
	}
	return ""
}

func expect(t *testing.T, code, want int, out *bytes.Buffer, contains ...string) {
	t.Helper()
	if code != want {
		t.Errorf("exit code %d, want %d\n%s", code, want, out)
	}
	for _, c := range contains {
		if !strings.Contains(out.String(), c) {
			t.Errorf("output lacks %q:\n%s", c, out)
		}
	}
}

func TestRunUnknownCheck(t *testing.T) {
	e, out := tree(t, nil, nil)
	expect(t, Run(e, "nope", nil), 2, out, `unknown check "nope"`)
}

func TestFormat(t *testing.T) {
	for _, tool := range []string{"git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	root := t.TempDir()
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.Exec(root, "git", "init", "-q"); err != nil {
		t.Fatal(err)
	}
	write("good.go", "package x\n")
	out := &bytes.Buffer{}
	e := Env{Root: root, Run: repo.Exec, Out: out}
	expect(t, Run(e, "format", nil), 0, out, "1 Go files formatted")

	write("bad.go", "package   x\n")
	out.Reset()
	expect(t, Run(e, "format", nil), 1, out, "bad.go: not gofmt-formatted")
}

func TestFormatWithoutGoFiles(t *testing.T) {
	e, out := tree(t, map[string]string{"a.md": "x"}, func(string, []string) (string, error) { return "/goroot", nil })
	expect(t, Format(e), 0, out, "no Go files")
}

func TestLint(t *testing.T) {
	report := `{"Issues":[{"FromLinter":"funlen","Text":"too long","Pos":{"Filename":"a.go","Line":3}}]}`
	golangci := func(_ string, args []string) (string, error) {
		path := flagValue(args, "--output.json.path=")
		return "a.go:3: too long (funlen)\n", os.WriteFile(path, []byte(report), 0o644)
	}
	files := map[string]string{
		"a.go":                      "package a\n\nfunc f() {} //" + "nolint:funlen // reason\n// prose about //nolint is fine\n",
		"scripts/lint-baseline.tsv": "# header\n",
	}

	e, out := tree(t, files, golangci)
	expect(t, Lint(e), 1, out, "too long (funlen)", "a.go funlen: 1 found", "a.go nolint: 1 found")

	files["scripts/lint-baseline.tsv"] = "a.go\tfunlen\t1\na.go\tnolint\t1\n"
	e, out = tree(t, files, golangci)
	expect(t, Lint(e), 0, out, "no findings beyond the baseline")
}

func TestLintFailures(t *testing.T) {
	e, out := tree(t, nil, func(string, []string) (string, error) { return "boom\n", errors.New("exit 3") })
	expect(t, Lint(e), 1, out, "boom", "lint: exit 3")

	e, out = tree(t, nil, func(_ string, args []string) (string, error) {
		return "", os.WriteFile(flagValue(args, "--output.json.path="), []byte("not json"), 0o644)
	})
	expect(t, Lint(e), 1, out, "golangci-lint JSON report")
}

func TestCoverage(t *testing.T) {
	profile := "mode: set\n" +
		"m/a.go:1.1,2.1 3 1\n" +
		"m/a.go:3.1,4.1 1 0\n" +
		"m/b.go:1.1,2.1 1 0\n" +
		"m/b.go:1.1,2.1 1 1\n" // the same block from a second test binary
	gotest := func(_ string, args []string) (string, error) {
		return "ok\n", os.WriteFile(flagValue(args, "-coverprofile="), []byte(profile), 0o644)
	}
	e, out := tree(t, nil, gotest)
	expect(t, Coverage(e, []string{"-min", "80"}), 0, out, "80.0% of 5 statements covered (minimum 80%)", "75.0% of 4 statements, m/a.go")

	e, out = tree(t, nil, gotest)
	expect(t, Coverage(e, []string{"-min", "90"}), 1, out, "add tests")

	e, out = tree(t, nil, func(string, []string) (string, error) { return "FAIL x\n", errors.New("exit 1") })
	expect(t, Coverage(e, nil), 1, out, "FAIL x")

	e, out = tree(t, nil, nil)
	expect(t, Coverage(e, []string{"-bogus"}), 2, out)
}

func TestParseProfileRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"mode: set\nm/a.go:1.1,2.1 x 1\n", "mode: set\nonly two\n"} {
		if _, _, err := parseProfile(bad); err == nil {
			t.Errorf("parseProfile(%q): want an error", bad)
		}
	}
	files, total, err := parseProfile("mode: set\n")
	if err != nil || len(files) != 0 || total.percent() != 100 {
		t.Errorf("empty profile: %v %v %v", files, total, err)
	}
}

func TestFileLength(t *testing.T) {
	long := strings.Repeat("x\n", 6)
	files := map[string]string{
		"long.go":                          long,
		"long.md":                          long,
		"vendor/long.txt":                  long,
		"no-newline.go":                    "a\nb",
		"bin.dat":                          "\x00" + long,
		"scripts/file-length-baseline.tsv": "",
	}
	args := []string{"-max", "5", "-exclude", "*.md,vendor/long.txt"}
	e, out := tree(t, files, nil)
	expect(t, FileLength(e, args), 1, out, "long.go: 6 lines, over 5,")

	files["scripts/file-length-baseline.tsv"] = "long.go\t6\n"
	e, out = tree(t, files, nil)
	expect(t, FileLength(e, args), 0, out, "files within 5 lines")

	files["long.go"] = "x\n"
	e, out = tree(t, files, nil)
	expect(t, FileLength(e, args), 1, out, "lower the row")
}

func TestImports(t *testing.T) {
	listing := `{"ImportPath":"m","Imports":["fmt"]}
{"ImportPath":"m/internal/a","Imports":["fmt","m/internal/b"]}
{"ImportPath":"m/internal/b","Imports":["m/devtools/x"]}
{"ImportPath":"m/devtools/x"}`
	golist := func(_ string, args []string) (string, error) {
		if strings.Join(args, " ") == "list -m" {
			return "m\n", nil
		}
		return listing, nil
	}
	files := map[string]string{
		"scripts/import-rules.tsv":    "internal/...\tforbid\tdevtools/...\ninternal/a\tonly\tinternal/b\n.\tonly\t-\n",
		"scripts/import-baseline.tsv": "",
	}
	e, out := tree(t, files, golist)
	expect(t, Imports(e), 1, out, "internal/b devtools/x: 1 found")

	files["scripts/import-baseline.tsv"] = "internal/b\tdevtools/x\t1\n"
	e, out = tree(t, files, golist)
	expect(t, Imports(e), 0, out, "4 packages follow 3 rules")

	files["scripts/import-rules.tsv"] += "gone/...\tforbid\t-\n"
	e, out = tree(t, files, golist)
	expect(t, Imports(e), 1, out, "importer gone/... matches no package")

	files["scripts/import-rules.tsv"] = "internal/a\tmaybe\tx\n"
	e, out = tree(t, files, golist)
	expect(t, Imports(e), 1, out, "want '<importer>")
}

func TestVersions(t *testing.T) {
	good := map[string]string{
		"go.mod": "module m\n\ngo 1.27.0\n\ntoolchain go1.27.2\n",
		".github/workflows/ci.yml": "steps:\n" +
			"  - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1\n" +
			"  - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0\n" +
			"    with:\n      go-version-file: go.mod\n" +
			"  - uses: ./local-action\n",
		".golangci.yml":   "version: \"2\"\nrun:\n  timeout: 5m\n",
		"docs/a.md":       "Cite go.mod.\n",
		"design/draft.md": "We tried Go 1.26.\n",
	}
	e, out := tree(t, good, nil)
	expect(t, Versions(e), 0, out, "go1.27.2 from go.mod everywhere")

	bad := map[string]string{
		"go.mod": "module m\n\ngo 1.27.0\n\ntoolchain go1.28.1\n",
		".github/workflows/ci.yml": "env:\n  GOTOOLCHAIN: local\nsteps:\n" +
			"  - uses: actions/checkout@v7\n" +
			"  - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0\n" +
			"    with:\n      go-version: '1.27'\n",
		".golangci.yml": "run:\n  go: '1.26'\n",
		"docs/a.md":     "Needs go1.27 or later.\n",
	}
	e, out = tree(t, bad, nil)
	expect(t, Versions(e), 1, out,
		"go 1.27.0 and toolchain go1.28.1 name different minors",
		"running on go1.27.2, not go1.28.1",
		"ci.yml:2: sets GOTOOLCHAIN",
		"ci.yml:4: pin the action",
		"ci.yml:7: sets go-version",
		"setup-go needs `go-version-file: go.mod`",
		".golangci.yml: sets a Go version",
		`docs/a.md:1: restates a Go version ("go1.27")`)
}

func TestGoModProblems(t *testing.T) {
	tests := map[string]string{
		"module m\n":            "no `go` line",
		"module m\ngo 1.27.0\n": "no `toolchain",
		"module m\ngo 1.27.3\ntoolchain go1.27.2\n": "older than go 1.27.3",
	}
	for gomod, want := range tests {
		_, problems := goModProblems(gomod)
		if len(problems) != 1 || !strings.Contains(problems[0], want) {
			t.Errorf("goModProblems(%q) = %q, want %q", gomod, problems, want)
		}
	}
}
