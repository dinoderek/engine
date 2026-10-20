// Package dev implements the ./dev verbs: doctor, run, test, worktree and
// deps. The ./dev shell script pins the toolchain from go.mod and then runs
// this package's Main.
package dev

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/dinoderek/engine/devtools/internal/checks"
	"github.com/dinoderek/engine/devtools/internal/lanes"
	"github.com/dinoderek/engine/devtools/internal/repo"
)

const usage = `usage: ./dev <verb>

  doctor                       check this machine's prerequisites and install the missing ones
  run [args]                   build and run engine with args
  test                         run every required lane in parallel, no fail-fast
  test <lane>...               run the named lanes
  test for <path>...           run the required lanes those paths trigger
  test --list                  print the lane registry (scripts/lanes.tsv)
  worktree create <branch>     new branch and worktree from the remote's default branch
  worktree release <branch>    remove the worktree and delete its merged branch
  deps                         outdated modules, Go releases and govulncheck findings

There is no deploy verb: engine has no deploy target yet.
`

// Tool carries everything the verbs touch outside the process, so tests can
// replace it.
type Tool struct {
	Root      string
	Self      string // path of the running dev binary, used for "dev" lane commands
	GoVersion string
	Run       repo.Runner
	Attach    func(dir, name string, args ...string) int // runs with the terminal attached
	LookPath  func(string) (string, error)
	Fetch     func(url string) ([]byte, error)
	Stdout    io.Writer
	Stderr    io.Writer
}

// Main is the entry point of the dev binary.
func Main(args []string) int {
	wd, err := os.Getwd()
	if err == nil {
		wd, err = repo.Root(wd)
	}
	self, selfErr := os.Executable()
	if err = errors.Join(err, selfErr); err != nil {
		fmt.Fprintln(os.Stderr, "dev:", err)
		return 1
	}
	t := Tool{
		Root: wd, Self: self, GoVersion: runtime.Version(),
		Run: repo.Exec, Attach: attach, LookPath: exec.LookPath, Fetch: fetch,
		Stdout: os.Stdout, Stderr: os.Stderr,
	}
	return t.Main(args)
}

// Main dispatches one verb.
func (t Tool) Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(t.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "doctor":
		return t.Doctor()
	case "run":
		return t.Attach(t.Root, "go", append([]string{"run", "./cmd/engine"}, args[1:]...)...)
	case "test":
		return t.Test(args[1:])
	case "check":
		if len(args) < 2 {
			fmt.Fprintf(t.Stderr, "usage: ./dev check <%s> [flags]\n", checks.Names)
			return 2
		}
		env := checks.Env{Root: t.Root, Run: t.Run, Out: t.Stdout, GoVersion: t.GoVersion}
		return checks.Run(env, args[1], args[2:])
	case "worktree":
		return t.Worktree(args[1:])
	case "deps":
		return t.Deps()
	case "help", "-h", "--help":
		fmt.Fprint(t.Stdout, usage)
		return 0
	default:
		fmt.Fprintf(t.Stderr, "dev: unknown verb %q\n\n%s", args[0], usage)
		return 2
	}
}

// Test runs lanes: all required ones, the named ones, or those that paths
// trigger.
func (t Tool) Test(args []string) int {
	registry, err := lanes.Load(t.Root)
	if err != nil {
		fmt.Fprintln(t.Stderr, "dev:", err)
		return 1
	}
	var selected []lanes.Lane
	switch {
	case len(args) == 1 && args[0] == "--list":
		fmt.Fprint(t.Stdout, lanes.List(registry))
		return 0
	case len(args) > 0 && args[0] == "for":
		if len(args) == 1 {
			fmt.Fprintln(t.Stderr, "usage: ./dev test for <path>...")
			return 2
		}
		selected = lanes.For(registry, args[1:])
		if len(selected) == 0 {
			fmt.Fprintln(t.Stdout, "dev: no lane is triggered by those paths")
			return 0
		}
	default:
		if selected, err = lanes.Select(registry, args); err != nil {
			fmt.Fprintln(t.Stderr, "dev:", err)
			return 2
		}
	}
	return lanes.Run(selected, t.Root, t.Self, t.Stdout)
}

func attach(dir, name string, args ...string) int {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "dev:", err)
		return 1
	}
	return 0
}

func fetch(url string) ([]byte, error) {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
