package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var toolchainRe = regexp.MustCompile(`(?m)^toolchain (go\S+)\s*$`)

// prerequisite is one thing doctor checks. brew names the Homebrew formula
// that installs it, if any.
type prerequisite struct {
	name  string
	check func() error
	brew  string
	hint  string
}

// Doctor checks every prerequisite, installs what Homebrew can, and reports
// the rest as gaps to fix. It fails while any gap remains.
func (t Tool) Doctor() int {
	gaps := 0
	for _, p := range t.prerequisites() {
		err := p.check()
		if err != nil && p.brew != "" {
			if _, brewErr := t.LookPath("brew"); brewErr == nil {
				fmt.Fprintf(t.Stdout, "...  %s: installing with brew install %s\n", p.name, p.brew)
				if out, installErr := t.Run(t.Root, "brew", "install", p.brew); installErr != nil {
					fmt.Fprint(t.Stdout, out)
				}
				err = p.check()
			}
		}
		if err != nil {
			gaps++
			fmt.Fprintf(t.Stdout, "FAIL %s: %v\n     fix: %s\n", p.name, err, p.hint)
			continue
		}
		fmt.Fprintf(t.Stdout, "ok   %s\n", p.name)
	}
	if gaps > 0 {
		fmt.Fprintf(t.Stdout, "doctor: %d gap(s) to fix\n", gaps)
		return 1
	}
	fmt.Fprintln(t.Stdout, "doctor: ready")
	return 0
}

func (t Tool) prerequisites() []prerequisite {
	onPath := func(name string) func() error {
		return func() error {
			_, err := t.LookPath(name)
			return err
		}
	}
	runs := func(name string, args ...string) func() error {
		return func() error {
			out, err := t.Run(t.Root, name, args...)
			if err != nil {
				return fmt.Errorf("%w\n%s", err, strings.TrimSpace(out))
			}
			return nil
		}
	}
	return []prerequisite{
		{name: "git", check: onPath("git"), brew: "git", hint: "install git from https://git-scm.com/downloads"},
		{name: "gh (pull requests and CI)", check: onPath("gh"), brew: "gh", hint: "install the GitHub CLI from https://cli.github.com, then run gh auth login"},
		{name: "Go toolchain pinned in go.mod", check: t.checkToolchain, hint: "run the verbs through ./dev, which sets GOTOOLCHAIN from go.mod"},
		{name: "C toolchain (go test -race)", check: t.checkCgo, hint: "macOS: xcode-select --install; Linux: install gcc"},
		{name: "Go modules", check: runs("go", "mod", "download"), hint: "check network access to proxy.golang.org"},
		{name: "golangci-lint (go tool)", check: runs("go", "tool", "golangci-lint", "version"), hint: "run go mod download, then retry"},
		{name: "govulncheck (go tool)", check: runs("go", "tool", "govulncheck", "-version"), hint: "run go mod download, then retry"},
	}
}

func (t Tool) checkToolchain() error {
	gomod, err := os.ReadFile(filepath.Join(t.Root, "go.mod"))
	if err != nil {
		return err
	}
	m := toolchainRe.FindSubmatch(gomod)
	if m == nil {
		return fmt.Errorf("go.mod has no toolchain line")
	}
	if want := string(m[1]); t.GoVersion != want {
		return fmt.Errorf("running %s, go.mod pins %s", t.GoVersion, want)
	}
	return nil
}

func (t Tool) checkCgo() error {
	out, err := t.Run(t.Root, "go", "env", "CGO_ENABLED", "CC")
	if err != nil {
		return err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != "1" {
		return fmt.Errorf("cgo is disabled; the race detector needs it")
	}
	if _, err := t.LookPath(fields[1]); err != nil {
		return fmt.Errorf("no C compiler %q on PATH", fields[1])
	}
	return nil
}
