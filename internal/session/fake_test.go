package session

import (
	"errors"
	"strings"
	"testing"
)

// call is one command a fake RunFunc was asked to run.
type call struct {
	dir  string
	env  []string
	line string // name and args, space-joined
}

// fake is a RunFunc that records every call and answers from canned replies:
// the first reply whose key is a substring of the call's line wins, and a
// call nothing matches succeeds with no output.
type fake struct {
	calls   []call
	replies []reply
	// onRun runs before each call is answered, for tests that need a side effect.
	onRun func(c call)
}

type reply struct {
	key string
	out string
	err error
}

var errBoom = errors.New("exit status 1")

func (f *fake) run(dir string, env []string, name string, args ...string) (string, error) {
	c := call{dir: dir, env: env, line: strings.Join(append([]string{name}, args...), " ")}
	f.calls = append(f.calls, c)
	if f.onRun != nil {
		f.onRun(c)
	}
	for _, r := range f.replies {
		if strings.Contains(c.line, r.key) {
			return r.out, r.err
		}
	}
	return "", nil
}

func (f *fake) lines() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, c.line)
	}
	return out
}

// has reports whether some call's line contains key.
func (f *fake) has(key string) bool {
	for _, c := range f.calls {
		if strings.Contains(c.line, key) {
			return true
		}
	}
	return false
}

// syncReplies are the answers of a healthy remote whose default branch is main.
func syncReplies() []reply {
	return []reply{
		{key: "gh api user", out: "testbot 42\n"},
		{key: "symbolic-ref", out: "origin/main\n"},
		{key: "remote get-url", out: "https://github.com/o/r.git\n"},
	}
}

func newRepos(t *testing.T, f *fake) Repos {
	t.Helper()
	return Repos{Run: f.run, Home: t.TempDir(), Token: "tok"}
}

func mustRepo(t *testing.T, s string) Repo {
	t.Helper()
	repo, err := ParseRepo(s)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}
