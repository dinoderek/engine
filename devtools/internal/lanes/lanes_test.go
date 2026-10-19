package lanes

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const registry = "# comment\n" +
	"format\trequired\t*.go\tgofmt -l .\n" +
	"docs\trequired\t*.md,docs/\tdev check docs\n" +
	"ci\trequired\t.github/workflows/*.yml\tdev check ci\n" +
	"slow\textra\t*\tdev slow\n"

func names(lanes []Lane) string {
	var n []string
	for _, l := range lanes {
		n = append(n, l.Name)
	}
	return strings.Join(n, ",")
}

func TestParse(t *testing.T) {
	lanes, err := Parse(registry)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(lanes); got != "format,docs,ci,slow" {
		t.Errorf("names = %s", got)
	}
	if got := strings.Join(lanes[1].Command, " "); got != "dev check docs" {
		t.Errorf("command = %q", got)
	}
	for _, bad := range []string{"", "a\trequired\tx\n", "a\tsometimes\t*\tcmd\n", "a\trequired\t*\tx\na\trequired\t*\ty\n"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q): want an error", bad)
		}
	}
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root); err == nil {
		t.Error("Load without a registry: want an error")
	}
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, Registry), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	if lanes, err := Load(root); err != nil || len(lanes) != 4 {
		t.Errorf("Load = %d lanes, %v", len(lanes), err)
	}
}

func TestSelect(t *testing.T) {
	lanes, _ := Parse(registry)
	all, _ := Select(lanes, nil)
	if got := names(all); got != "format,docs,ci" {
		t.Errorf("default selection = %s", got)
	}
	named, err := Select(lanes, []string{"slow", "format"})
	if err != nil || names(named) != "slow,format" {
		t.Errorf("named selection = %s, %v", names(named), err)
	}
	if _, err := Select(lanes, []string{"nope"}); err == nil {
		t.Error("unknown lane: want an error")
	}
}

func TestFor(t *testing.T) {
	lanes, _ := Parse(registry)
	tests := []struct {
		paths []string
		want  string
	}{
		{[]string{"internal/x/y.go"}, "format"},
		{[]string{"docs/a.txt"}, "docs"},
		{[]string{"./README.md"}, "docs"},
		{[]string{".github/workflows/ci.yml"}, "ci"},
		{[]string{".github/dependabot.yml"}, ""},
		{[]string{"a.go", "b.md"}, "format,docs"},
		{[]string{"scripts/lanes.tsv"}, "format,docs,ci"},
	}
	for _, tt := range tests {
		if got := names(For(lanes, tt.paths)); got != tt.want {
			t.Errorf("For(%v) = %q, want %q", tt.paths, got, tt.want)
		}
	}
}

func TestList(t *testing.T) {
	lanes, _ := Parse(registry)
	if got := List(lanes); !strings.Contains(got, "slow         extra    dev slow") {
		t.Errorf("List =\n%s", got)
	}
}

func TestRunWritesEveryLaneWithoutFailFast(t *testing.T) {
	root := t.TempDir()
	lanes := []Lane{
		{Name: "fails", Command: []string{"go", "no-such-subcommand"}},
		{Name: "passes", Command: []string{"go", "env", "GOROOT"}},
		{Name: "self", Command: []string{"dev", "env", "GOOS"}},
		{Name: "missing", Command: []string{"no-such-binary-anywhere"}},
	}
	var out bytes.Buffer
	if code := Run(lanes, root, "go", &out); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	summary := out.String()
	for _, want := range []string{"- FAIL fails", "- PASS passes", "- PASS self", "- FAIL missing", "Overall: FAIL"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary lacks %q:\n%s", want, summary)
		}
	}
	missing, err := os.ReadFile(filepath.Join(root, Results, "missing.txt"))
	if err != nil || !strings.Contains(string(missing), "could not start lane") {
		t.Errorf("missing.txt = %q, %v", missing, err)
	}
	if _, err := os.Stat(filepath.Join(root, Results, "summary.md")); err != nil {
		t.Error(err)
	}

	out.Reset()
	if code := Run(lanes[1:2], root, "go", &out); code != 0 {
		t.Errorf("passing run: exit code %d\n%s", code, out.String())
	}
}

func TestSummary(t *testing.T) {
	got := Summary([]Result{{Lane: Lane{Name: "unit"}, Elapsed: 1500 * time.Millisecond}}, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	want := "# Quality gate results\n\nRun started: 2026-01-02T03:04:05Z\n\n" +
		"- PASS unit (1.5s) — .test-ai-results/unit.txt\n\nOverall: PASS\n"
	if got != want {
		t.Errorf("Summary =\n%s\nwant\n%s", got, want)
	}
}
