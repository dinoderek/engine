// Package tracker is engine's GitHub tracker adapter. It reaches GitHub only
// through the gh CLI, behind a Runner, so tests replace it.
package tracker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// TokenFile is where each host keeps the machine user's token, relative to
// the home directory.
const TokenFile = ".config/engine/github-token"

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

// Token returns the token engine acts with on GitHub: GH_TOKEN when set,
// else the token file under home.
func Token(getenv func(string) string, home string) (string, error) {
	if token := getenv("GH_TOKEN"); token != "" {
		return token, nil
	}
	path := filepath.Join(home, TokenFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("no GH_TOKEN and no token file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New(path + " is empty")
	}
	return token, nil
}

// Label is one tracker label.
type Label struct {
	Name        string
	Color       string // six hex digits, no #
	Description string
}

// Labels is every engine: label: the feature states, the task states, and
// the ruling-review flag. A task still to do has no state label.
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
	{"engine:ruling-review", "f9a825", "Task: has medium- or high-risk rulings for the operator to read"},
}

var repoName = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// SyncLabels creates every label in Labels in repo (owner/name), or updates
// its colour and description. It never deletes a label.
func SyncLabels(run Runner, token, repo string, out io.Writer) error {
	if !repoName.MatchString(repo) {
		return fmt.Errorf("want owner/repo, got %q", repo)
	}
	env := []string{"GH_TOKEN=" + token}
	for _, l := range Labels {
		output, err := run(env, "gh", "label", "create", l.Name, "--repo", repo,
			"--color", l.Color, "--description", l.Description, "--force")
		if err != nil {
			return fmt.Errorf("label %s: %w\n%s", l.Name, err, strings.TrimSpace(output))
		}
		fmt.Fprintf(out, "%-22s %s\n", l.Name, l.Description)
	}
	fmt.Fprintf(out, "%d engine labels in %s\n", len(Labels), repo)
	return nil
}
