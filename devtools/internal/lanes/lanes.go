// Package lanes reads the lane registry (scripts/lanes.tsv), selects lanes and
// runs them in parallel with no fail-fast, writing each lane's output and a
// summary to the results directory.
package lanes

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dinoderek/engine/devtools/internal/repo"
)

// Registry and Results are relative to the repository root.
const (
	Registry = "scripts/lanes.tsv"
	Results  = ".test-ai-results"
)

// Lane is one registry row.
type Lane struct {
	Name     string
	Gate     string   // "required" runs in ./dev test; "extra" runs by name only
	Triggers []string // patterns that select the lane in ./dev test for <paths>
	Command  []string // argv; a leading "dev" is this tool itself
}

// Parse reads the registry text.
func Parse(text string) ([]Lane, error) {
	var lanes []Lane
	seen := map[string]bool{}
	for _, row := range repo.Rows(text) {
		f := row.Fields
		if len(f) != 4 || (f[1] != "required" && f[1] != "extra") || f[0] == "" || seen[f[0]] ||
			len(strings.Fields(f[3])) == 0 {
			return nil, fmt.Errorf("%s:%d: want a unique '<name>\\t<required|extra>\\t<triggers>\\t<command>'",
				Registry, row.Line)
		}
		seen[f[0]] = true
		lanes = append(lanes, Lane{
			Name:     f[0],
			Gate:     f[1],
			Triggers: strings.Split(f[2], ","),
			Command:  strings.Fields(f[3]),
		})
	}
	if len(lanes) == 0 {
		return nil, fmt.Errorf("%s: no lanes", Registry)
	}
	return lanes, nil
}

// Load reads the registry under root.
func Load(root string) ([]Lane, error) {
	text, err := os.ReadFile(filepath.Join(root, Registry))
	if err != nil {
		return nil, err
	}
	return Parse(string(text))
}

// Select returns the required lanes when names is empty, else the named lanes.
func Select(lanes []Lane, names []string) ([]Lane, error) {
	if len(names) == 0 {
		var out []Lane
		for _, l := range lanes {
			if l.Gate == "required" {
				out = append(out, l)
			}
		}
		return out, nil
	}
	byName := map[string]Lane{}
	for _, l := range lanes {
		byName[l.Name] = l
	}
	var out []Lane
	for _, n := range names {
		l, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("unknown lane %q; ./dev test --list shows the registry", n)
		}
		out = append(out, l)
	}
	return out, nil
}

// For returns the required lanes that any of paths triggers. A change to the
// registry itself triggers every required lane.
func For(lanes []Lane, paths []string) []Lane {
	var out []Lane
	for _, l := range lanes {
		if l.Gate != "required" {
			continue
		}
		for _, p := range paths {
			p = path.Clean(filepath.ToSlash(p))
			if p == Registry || l.triggeredBy(p) {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// triggeredBy reports whether p matches one of the lane's patterns. A pattern
// ending in "/" matches any path with that directory in it, at any depth; one
// containing "/" matches the whole path; any other matches the file name.
func (l Lane) triggeredBy(p string) bool {
	for _, pat := range l.Triggers {
		switch {
		case strings.HasSuffix(pat, "/"):
			if strings.Contains("/"+p, "/"+pat) {
				return true
			}
		case strings.Contains(pat, "/"):
			if ok, _ := path.Match(pat, p); ok {
				return true
			}
		default:
			if ok, _ := path.Match(pat, path.Base(p)); ok {
				return true
			}
		}
	}
	return false
}

// List renders the registry for ./dev test --list.
func List(lanes []Lane) string {
	var b strings.Builder
	for _, l := range lanes {
		fmt.Fprintf(&b, "%-12s %-8s %s\n", l.Name, l.Gate, strings.Join(l.Command, " "))
	}
	return b.String()
}

// Result is the outcome of one lane.
type Result struct {
	Lane    Lane
	Code    int
	Elapsed time.Duration
}

// Run runs lanes in parallel from root, replaces the results directory with
// one <lane>.txt per lane and summary.md, prints the summary and returns 0 only if every lane
// passed. self is the path of this tool, which a leading "dev" runs.
func Run(lanes []Lane, root, self string, out io.Writer) int {
	dir := filepath.Join(root, Results)
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	started := time.Now().UTC()
	results := make([]Result, len(lanes))
	var wg sync.WaitGroup
	for i, l := range lanes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = runLane(l, root, self, dir)
		}()
	}
	wg.Wait()
	summary := Summary(results, started)
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(summary), 0o644); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprint(out, summary)
	for _, r := range results {
		if r.Code != 0 {
			return 1
		}
	}
	return 0
}

func runLane(l Lane, root, self, dir string) Result {
	start := time.Now()
	argv := append([]string(nil), l.Command...)
	if argv[0] == "dev" {
		argv[0] = self
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			output = append(output, fmt.Sprintf("dev: could not start lane: %v\n", err)...)
		}
	}
	elapsed := time.Since(start)
	header := fmt.Sprintf("# lane: %s\n# command: %s\n# exit: %d\n# seconds: %.1f\n\n",
		l.Name, strings.Join(l.Command, " "), code, elapsed.Seconds())
	if len(output) == 0 {
		output = []byte("(no output)\n")
	}
	if err := os.WriteFile(filepath.Join(dir, l.Name+".txt"), append([]byte(header), output...), 0o644); err != nil {
		code = 1
	}
	return Result{Lane: l, Code: code, Elapsed: elapsed}
}

// Summary renders summary.md: one PASS or FAIL line per lane and an overall
// verdict.
func Summary(results []Result, started time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Quality gate results\n\nRun started: %s\n\n", started.Format(time.RFC3339))
	overall := "PASS"
	for _, r := range results {
		status := "PASS"
		if r.Code != 0 {
			status, overall = "FAIL", "FAIL"
		}
		fmt.Fprintf(&b, "- %s %s (%.1fs) — %s/%s.txt\n", status, r.Lane.Name, r.Elapsed.Seconds(), Results, r.Lane.Name)
	}
	fmt.Fprintf(&b, "\nOverall: %s\n", overall)
	return b.String()
}
