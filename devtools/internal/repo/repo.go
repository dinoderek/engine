// Package repo finds the repository root, runs commands in it and lists its
// files. Every devtools package reaches git and go through a Runner, so tests
// can replace them.
package repo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Runner runs name with args in dir and returns its combined output.
type Runner func(dir, name string, args ...string) (string, error)

// Exec is the Runner that starts real processes.
func Exec(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), err
}

// Root returns the nearest directory at or above dir that holds go.mod.
func Root(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod at or above the current directory")
		}
		dir = parent
	}
}

// Files lists the tracked and untracked, non-ignored files under root as
// slash-separated paths relative to it. Tracked files deleted from the working
// tree are left out. The list is sorted.
func Files(run Runner, root string) ([]string, error) {
	out, err := run(root, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, f)
	}
	sort.Strings(files)
	return files, nil
}

// Rows splits a tab-separated registry into rows, dropping blank lines and
// lines that start with '#'. Each row keeps its 1-based line number.
func Rows(text string) []Row {
	var rows []Row
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		rows = append(rows, Row{Line: i + 1, Fields: strings.Split(line, "\t")})
	}
	return rows
}

// Row is one registry line.
type Row struct {
	Line   int
	Fields []string
}
