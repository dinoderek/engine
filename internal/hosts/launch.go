package hosts

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Launch is what a launch line's placeholders expand to, besides the tier's
// model and effort.
type Launch struct {
	Harness string
	Tier    Tier
	// Name is the session name, <repo>-<task>.
	Name string
	// Clone is engine's clone of the task's repo.
	Clone string
	// Home expands a bin path that starts with ~/.
	Home string
}

// BinPath returns the absolute path of the harness binary on host h, with a
// leading ~/ expanded against home.
func (h Host) BinPath(harness, home string) (string, error) {
	bin, ok := h.Bin[harness]
	if !ok {
		return "", fmt.Errorf("host %s has no bin for harness %q", h.Name, harness)
	}
	if rest, ok := strings.CutPrefix(bin, "~/"); ok {
		if home == "" {
			return "", fmt.Errorf("host %s: no home directory to expand %s", h.Name, bin)
		}
		bin = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(bin) {
		return "", fmt.Errorf("host %s: bin for %s is %q; want an absolute path or one under ~/", h.Name, harness, bin)
	}
	return bin, nil
}

// LaunchLine expands the harness's launch line for host h into a shell
// command. Every value is quoted for the shell unless it is a plain word, so
// a placeholder that stands as its own word stays one argument. A placeholder
// inside quotes in the launch line gets no such protection.
func (c *Config) LaunchLine(h Host, l Launch) (string, error) {
	harness, ok := c.Harnesses[l.Harness]
	if !ok {
		return "", fmt.Errorf("harness %q is not in hosts.toml", l.Harness)
	}
	if !h.Runs(l.Harness) {
		return "", fmt.Errorf("host %s does not run harness %q", h.Name, l.Harness)
	}
	model, err := c.Model(l.Harness, l.Tier)
	if err != nil {
		return "", err
	}
	bin, err := h.BinPath(l.Harness, l.Home)
	if err != nil {
		return "", err
	}
	r := strings.NewReplacer(
		"{bin}", ShellWord(bin),
		"{model}", ShellWord(model.Model),
		"{effort}", ShellWord(model.Effort),
		"{name}", ShellWord(l.Name),
		"{clone}", ShellWord(l.Clone),
	)
	return r.Replace(harness.Launch), nil
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// ShellWord returns s as one shell word: as is when it holds only characters
// the shell takes literally, else in single quotes.
func ShellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
