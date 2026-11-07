// Package tracker is engine's GitHub tracker adapter. It reaches GitHub only
// through the gh CLI, behind a Runner, so tests replace it.
package tracker

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Runner runs name with args, adding env to the process environment, and
// returns its combined output.
type Runner func(env []string, name string, args ...string) (string, error)

// Exec is the Runner that starts real processes.
func Exec(env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), err
}

// Label is one tracker label.
type Label struct {
	Name        string
	Color       string // six hex digits, no #
	Description string
}

// Labels is every engine: label: the feature states, the task states, and
// the merging and ruling-review flags. A task still to do has no state label.
var Labels = []Label{
	{"engine:feature", "1d76db", "An engine feature: its tasks are sub-issues"},
	{"engine:prepare", "c5def5", "Feature: requirements and design with the operator"},
	{"engine:approved", "0e8a16", "Feature: approved by the operator, ready to plan"},
	{"engine:planned", "bfdadc", "Feature: tasks written and linted"},
	{"engine:executing", "5319e7", "Feature: tasks running"},
	{"engine:verifying", "d4c5f9", "Feature: acceptance task running"},
	{"engine:done", "0b5d1e", "Feature: every task merged"},
	{"engine:blocked", "b60205", "Feature: a task needs the operator"},
	{"engine:running", "fbca04", "Task: an executor session holds it"},
	{"engine:needs-human", "d93f0b", "Task: stopped at a hard stop, waiting for the operator"},
	{"engine:in-review", "0052cc", "Task: PR open"},
	{"engine:failed", "e11d21", "Task: its session died"},
	{"engine:merging", "006b75", "Task: holds its feature's merge lock"},
	{"engine:ruling-review", "f9a825", "Task: has high-risk rulings for the operator to read"},
}

var repoName = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// SyncLabels creates every label in Labels in repo (owner/name), or updates
// its colour and description. It never deletes a label.
func SyncLabels(run Runner, repo string, out io.Writer) error {
	if !repoName.MatchString(repo) {
		return fmt.Errorf("want owner/repo, got %q", repo)
	}
	for _, l := range Labels {
		output, err := run(nil, "gh", "label", "create", l.Name, "--repo", repo,
			"--color", l.Color, "--description", l.Description, "--force")
		if err != nil {
			return fmt.Errorf("label %s: %w\n%s", l.Name, err, strings.TrimSpace(output))
		}
		fmt.Fprintf(out, "%-22s %s\n", l.Name, l.Description)
	}
	fmt.Fprintf(out, "%d engine labels in %s\n", len(Labels), repo)
	return nil
}

// CurrentUser returns the account authenticated by the host's gh installation.
func CurrentUser(run Runner) (string, error) {
	out, err := api(run, "user", "--jq", ".login")
	if err != nil {
		return "", fmt.Errorf("GitHub account: %w", err)
	}
	login := strings.TrimSpace(out)
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`).MatchString(login) {
		return "", fmt.Errorf("GitHub account: unexpected login %q", login)
	}
	return login, nil
}
