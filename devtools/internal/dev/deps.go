package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// goReleases lists Go's published releases, newest first.
const goReleases = "https://go.dev/dl/?mode=json"

type module struct {
	Path     string
	Version  string
	Indirect bool
	Main     bool
	Update   *struct{ Version string }
}

// Deps reports outdated direct and tool modules, a newer Go release, and
// govulncheck's findings. It fails only on a vulnerability (or when the
// checks cannot run); outdated modules are a report.
func (t Tool) Deps() int {
	if err := t.reportModules(); err != nil {
		fmt.Fprintln(t.Stderr, "dev:", err)
		return 1
	}
	t.reportGo()
	out, err := t.Run(t.Root, "go", "tool", "govulncheck", "./...")
	fmt.Fprint(t.Stdout, out)
	if err != nil {
		fmt.Fprintln(t.Stdout, "deps: govulncheck reports vulnerabilities or could not run")
		return 1
	}
	return 0
}

func (t Tool) reportModules() error {
	tools, err := t.toolModules()
	if err != nil {
		return err
	}
	out, err := t.Run(t.Root, "go", "list", "-m", "-u", "-json", "all")
	if err != nil {
		return fmt.Errorf("go list -m -u: %w\n%s", err, out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	indirect := 0
	for {
		var m module
		if err := dec.Decode(&m); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
		if m.Main || m.Update == nil {
			continue
		}
		kind := "module"
		switch {
		case tools[m.Path]:
			kind = "tool"
		case m.Indirect:
			indirect++
			continue
		}
		fmt.Fprintf(t.Stdout, "outdated %-6s %s %s -> %s\n", kind, m.Path, m.Version, m.Update.Version)
	}
	fmt.Fprintf(t.Stdout, "deps: %d indirect module(s) have updates; they move with their parents\n", indirect)
	return nil
}

// toolModules returns the modules that provide go.mod's tool packages.
func (t Tool) toolModules() (map[string]bool, error) {
	out, err := t.Run(t.Root, "go", "mod", "edit", "-json")
	if err != nil {
		return nil, fmt.Errorf("go mod edit -json: %w", err)
	}
	var gomod struct {
		Require []struct{ Path string }
		Tool    []struct{ Path string }
	}
	if err := json.Unmarshal([]byte(out), &gomod); err != nil {
		return nil, err
	}
	tools := map[string]bool{}
	for _, tool := range gomod.Tool {
		best := ""
		for _, r := range gomod.Require {
			if (tool.Path == r.Path || strings.HasPrefix(tool.Path, r.Path+"/")) && len(r.Path) > len(best) {
				best = r.Path
			}
		}
		if best != "" {
			tools[best] = true
		}
	}
	return tools, nil
}

// reportGo compares go.mod's toolchain with the newest stable Go release.
// A network failure is reported, never fatal.
func (t Tool) reportGo() {
	gomod, err := os.ReadFile(filepath.Join(t.Root, "go.mod"))
	m := toolchainRe.FindSubmatch(gomod)
	if err != nil || m == nil {
		fmt.Fprintln(t.Stdout, "deps: go.mod has no toolchain line")
		return
	}
	pinned := string(m[1])
	body, err := t.Fetch(goReleases)
	var releases []struct {
		Version string
		Stable  bool
	}
	if err == nil {
		err = json.Unmarshal(body, &releases)
	}
	if err != nil {
		fmt.Fprintf(t.Stdout, "deps: could not list Go releases: %v\n", err)
		return
	}
	for _, r := range releases {
		if !r.Stable {
			continue
		}
		if r.Version == pinned {
			fmt.Fprintf(t.Stdout, "go: %s is the newest release\n", pinned)
		} else {
			fmt.Fprintf(t.Stdout, "outdated go     %s -> %s (docs/versions.md says when to move)\n", pinned, r.Version)
		}
		return
	}
	fmt.Fprintln(t.Stdout, "deps: no stable Go release listed")
}
