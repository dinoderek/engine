package checks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/devtools/internal/repo"
)

var (
	goLine        = regexp.MustCompile(`(?m)^go (\d+\.\d+(?:\.\d+)?)\s*$`)
	toolchainLine = regexp.MustCompile(`(?m)^toolchain go(\d+\.\d+\.\d+)\s*$`)
	usesLine      = regexp.MustCompile(`^\s*(?:-\s*)?uses:\s*(\S+)(.*)$`)
	pinnedUses    = regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	versionNote   = regexp.MustCompile(`^\s*#\s*v\d+(\.\d+)*\s*$`)
	goVersionKey  = regexp.MustCompile(`^\s*go-version\s*:`)
	golangciGo    = regexp.MustCompile(`(?m)^\s+go\s*:`)
	versionInDoc  = regexp.MustCompile(`(?i)\bgo ?1\.\d+`)
)

// Versions fails when the version pins disagree: go.mod's go and toolchain
// lines are the only source of the Go version, every workflow and the lint
// config take it from there, the gates run on it, docs never restate it, and
// every GitHub Action is pinned to a commit.
func Versions(e Env) int {
	const lane = "versions"
	gomod, err := os.ReadFile(filepath.Join(e.Root, "go.mod"))
	if err != nil {
		return e.fail(lane, err)
	}
	toolchain, problems := goModProblems(string(gomod))
	if toolchain != "" && e.GoVersion != "go"+toolchain {
		problems = append(problems, fmt.Sprintf("the gates are running on %s, not go%s from go.mod: run them through ./dev",
			e.GoVersion, toolchain))
	}
	files, err := repo.Files(e.Run, e.Root)
	if err != nil {
		return e.fail(lane, err)
	}
	for _, f := range files {
		text, err := os.ReadFile(filepath.Join(e.Root, f))
		if err != nil {
			return e.fail(lane, err)
		}
		problems = append(problems, fileVersionProblems(f, string(text))...)
	}
	problems = append(problems, pluginVersionProblems(files, func(name string) (string, bool) {
		text, err := os.ReadFile(filepath.Join(e.Root, name))
		return string(text), err == nil
	})...)
	return e.report(lane, problems, "go"+toolchain+" from go.mod everywhere; actions pinned by commit; plugin manifests agree")
}

// pluginVersionProblems checks that each plugin carries a Claude and a Codex
// manifest with the same version: both harnesses install one skills tree, so a
// bump of one is a bump of the other.
func pluginVersionProblems(files []string, read func(name string) (string, bool)) []string {
	const claude, codex = "/.claude-plugin/plugin.json", "/.codex-plugin/plugin.json"
	dirs := map[string]bool{}
	for _, f := range files {
		for _, suffix := range []string{claude, codex} {
			if dir, ok := strings.CutSuffix(f, suffix); ok {
				dirs[dir] = true
			}
		}
	}
	names := make([]string, 0, len(dirs))
	for d := range dirs {
		names = append(names, d)
	}
	sort.Strings(names)
	var problems []string
	for _, d := range names {
		c, cok := manifestVersion(read, d+claude)
		x, xok := manifestVersion(read, d+codex)
		switch {
		case !cok || !xok:
			problems = append(problems, fmt.Sprintf("%s: needs a readable `version` in both %s and %s", d, claude[1:], codex[1:]))
		case c != x:
			problems = append(problems, fmt.Sprintf("%s: Claude manifest version %s and Codex manifest version %s differ", d, c, x))
		}
	}
	return problems
}

// manifestVersion returns the version a plugin manifest declares.
func manifestVersion(read func(name string) (string, bool), name string) (string, bool) {
	text, ok := read(name)
	if !ok {
		return "", false
	}
	var m struct{ Version string }
	if json.Unmarshal([]byte(text), &m) != nil || m.Version == "" {
		return "", false
	}
	return m.Version, true
}

// goModProblems checks the go and toolchain lines and returns the toolchain
// version (without "go").
func goModProblems(gomod string) (string, []string) {
	g := goLine.FindStringSubmatch(gomod)
	t := toolchainLine.FindStringSubmatch(gomod)
	switch {
	case g == nil:
		return "", []string{"go.mod has no `go` line"}
	case t == nil:
		return "", []string{"go.mod has no `toolchain goX.Y.Z` line: it pins the exact Go for every gate"}
	}
	gv, tv := versionParts(g[1]), versionParts(t[1])
	var problems []string
	if gv[0] != tv[0] || gv[1] != tv[1] {
		problems = append(problems, fmt.Sprintf("go.mod: go %s and toolchain go%s name different minors", g[1], t[1]))
	} else if gv[2] > tv[2] {
		problems = append(problems, fmt.Sprintf("go.mod: toolchain go%s is older than go %s", t[1], g[1]))
	}
	return t[1], problems
}

func versionParts(v string) [3]int {
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

// fileVersionProblems checks one repository file for pins that bypass go.mod.
func fileVersionProblems(name, text string) []string {
	switch {
	case strings.HasPrefix(name, ".github/workflows/") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")):
		return workflowProblems(name, text)
	case name == ".golangci.yml":
		if golangciGo.MatchString(text) {
			return []string{name + ": sets a Go version; golangci-lint takes it from go.mod"}
		}
	case strings.HasSuffix(name, ".md") && !strings.HasPrefix(name, "design/"):
		var problems []string
		for i, line := range strings.Split(text, "\n") {
			if m := versionInDoc.FindString(line); m != "" {
				problems = append(problems, fmt.Sprintf("%s:%d: restates a Go version (%q); cite go.mod instead", name, i+1, m))
			}
		}
		return problems
	}
	return nil
}

func workflowProblems(name, text string) []string {
	var problems []string
	usesSetupGo := false
	for i, line := range strings.Split(text, "\n") {
		at := fmt.Sprintf("%s:%d", name, i+1)
		if strings.Contains(line, "GOTOOLCHAIN") {
			problems = append(problems, at+": sets GOTOOLCHAIN; ./dev takes it from go.mod")
		}
		if goVersionKey.MatchString(line) {
			problems = append(problems, at+": sets go-version; use go-version-file: go.mod")
		}
		m := usesLine.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(m[1], "./") || strings.HasPrefix(m[1], "docker://") {
			continue
		}
		if strings.HasPrefix(m[1], "actions/setup-go@") {
			usesSetupGo = true
		}
		if !pinnedUses.MatchString(m[1]) || !versionNote.MatchString(m[2]) {
			problems = append(problems, at+": pin the action to a full commit SHA with a `# vX.Y.Z` comment")
		}
	}
	if usesSetupGo && !strings.Contains(text, "go-version-file: go.mod") {
		problems = append(problems, name+": setup-go needs `go-version-file: go.mod`")
	}
	return problems
}
