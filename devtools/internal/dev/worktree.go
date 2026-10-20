package dev

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Worktree runs `worktree create <branch>` or `worktree release <branch>`.
// Worktrees live outside the checkout, in <checkout>-worktrees/ beside the
// main checkout. Release removes the worktree, then deletes the branch when
// git sees it as merged or its PR is merged (and then its remote branch too).
func (t Tool) Worktree(args []string) int {
	if len(args) != 2 || (args[0] != "create" && args[0] != "release") {
		fmt.Fprintln(t.Stderr, "usage: ./dev worktree create|release <branch>")
		return 2
	}
	main, err := t.mainCheckout()
	if err != nil {
		fmt.Fprintln(t.Stderr, "dev:", err)
		return 1
	}
	if args[0] == "create" {
		err = t.createWorktree(main, args[1])
	} else {
		err = t.releaseWorktree(main, args[1])
	}
	if err != nil {
		fmt.Fprintln(t.Stderr, "dev:", err)
		return 1
	}
	return 0
}

// WorktreePath is where create puts the worktree for branch.
func WorktreePath(main, branch string) string {
	return filepath.Join(filepath.Dir(main), filepath.Base(main)+"-worktrees", strings.ReplaceAll(branch, "/", "-"))
}

func (t Tool) git(dir string, args ...string) (string, error) {
	out, err := t.Run(dir, "git", args...)
	if err != nil {
		return out, fmt.Errorf("%w\n%s", err, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}

// mainCheckout is the working tree that owns the repository's .git directory.
func (t Tool) mainCheckout() (string, error) {
	common, err := t.git(t.Root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Dir(common), nil
}

// defaultBranch asks the remote, so a stale local origin/HEAD does not matter.
func (t Tool) defaultBranch(main string) (string, error) {
	out, err := t.git(main, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			return strings.Fields(ref)[0], nil
		}
	}
	return "", fmt.Errorf("origin did not report its default branch")
}

func (t Tool) createWorktree(main, branch string) error {
	base, err := t.defaultBranch(main)
	if err != nil {
		return err
	}
	if _, err := t.git(main, "fetch", "--quiet", "origin", base); err != nil {
		return err
	}
	dir := WorktreePath(main, branch)
	if _, err := t.git(main, "worktree", "add", "--quiet", "--no-track", "-b", branch, dir, "origin/"+base); err != nil {
		return err
	}
	fmt.Fprintf(t.Stdout, "worktree %s on branch %s from origin/%s\n", dir, branch, base)
	return nil
}

func (t Tool) releaseWorktree(main, branch string) error {
	list, err := t.git(main, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	dir := ""
	current := ""
	for _, line := range strings.Split(list, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			current = p
		}
		if line == "branch refs/heads/"+branch {
			dir = current
		}
	}
	switch dir {
	case "":
		return fmt.Errorf("no worktree has branch %s checked out", branch)
	case main:
		return fmt.Errorf("%s is the main checkout; release only removes task worktrees", dir)
	}
	if _, err := t.git(main, "worktree", "remove", dir); err != nil {
		return fmt.Errorf("worktree kept (commit or discard its changes first): %w", err)
	}
	fmt.Fprintf(t.Stdout, "removed worktree %s\n", dir)
	if _, err := t.git(main, "branch", "-d", branch); err == nil {
		fmt.Fprintf(t.Stdout, "deleted branch %s\n", branch)
		return nil
	}
	// A squash merge looks unmerged to git; the PR settles it, but only when
	// the branch still points at the commit the PR merged, so no later commit
	// is lost.
	head, err := t.mergedHead(main, branch)
	if err != nil {
		fmt.Fprintf(t.Stdout, "kept branch %s: %v\n", branch, err)
		return nil
	}
	if _, err := t.git(main, "branch", "-D", branch); err != nil {
		return err
	}
	fmt.Fprintf(t.Stdout, "deleted branch %s: its PR merged it\n", branch)
	lease := fmt.Sprintf("--force-with-lease=%s:%s", branch, head)
	if _, err := t.git(main, "push", "--quiet", lease, "origin", "--delete", branch); err == nil {
		fmt.Fprintf(t.Stdout, "deleted remote branch %s\n", branch)
	}
	return nil
}

// mergedHead returns the branch's tip when its PR is merged at exactly that
// commit, and an error saying why not otherwise.
func (t Tool) mergedHead(main, branch string) (string, error) {
	out, err := t.Run(main, "gh", "pr", "view", branch, "--json", "state,headRefOid", "--jq", `.state + " " + .headRefOid`)
	fields := strings.Fields(out)
	if err != nil || len(fields) != 2 || fields[0] != "MERGED" {
		return "", fmt.Errorf("it is not merged and has no merged PR")
	}
	tip, err := t.git(main, "rev-parse", "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if tip != fields[1] {
		return "", fmt.Errorf("it has commits after its merged PR (%s); push them or delete it with git branch -D", short(fields[1]))
	}
	return tip, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
