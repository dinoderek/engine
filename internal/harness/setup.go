package harness

import (
	"fmt"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
)

// PluginID is engine's plugin as both harnesses' plugin commands name it:
// the engine plugin from engine's own marketplace.
const PluginID = "engine@engine"

// PluginRepo is the GitHub repository whose marketplace publishes the plugin.
const PluginRepo = "dinoderek/engine"

// Setup is what an adapter adds to `engine setup` on a host that runs it.
type Setup interface {
	// Tools are the binaries the core's tools step requires on the host's
	// PATH. A harness whose binary hosts.toml pins checks it in its own step.
	Tools() []string
	// Plugin reads, through sh, the version of engine's plugin this harness
	// has installed for the user; found is false when it has none, or keeps
	// none it can read.
	Plugin(sh Shell) (version string, found bool, err error)
	// Steps run after the core's host steps, in order. Each call is a fresh
	// run: what one step finds, the later ones read.
	Steps(t Target) []Step
	// CheckFailed is the open item when this harness's preflight finds that
	// an unattended session on the host would stop.
	CheckFailed(t Target) (detail, command string)
}

// Shell runs a command line on a host.
type Shell interface {
	// Sh runs command in the host's non-interactive shell, with stdin when
	// given. A host that does not answer is an error, never a Result.
	Sh(command, stdin string) (Result, error)
}

// Target is the host being set up, as setup's runner reaches it.
type Target interface {
	Shell
	// OnHost is command as the operator would run it: over ssh for another
	// host, as it is for this one. A command that asks for input gets a
	// terminal.
	OnHost(command string, tty bool) string
	// Remote is true for another host, which setup changes; this host it
	// only checks.
	Remote() bool
	// Rerun is the command that repeats this setup.
	Rerun() string
	// Missing is true for a tool the tools step did not find.
	Missing(tool string) bool
	// Plugin is the caller's plugin version, which a remote host is brought
	// to; empty for this host.
	Plugin() string
	// Host is the target's entry in hosts.toml.
	Host() hosts.Host
	// Ref names the steps of this run called names, as the report numbers
	// them: "step 7", or "steps 8 and 9".
	Ref(names ...string) string
}

// Step is one numbered line of setup's report.
type Step struct {
	Name string
	Run  func() (Outcome, error)
	// Acts is set on a step that changes this host even when setup only
	// checks it.
	Acts bool
}

// StepState is how a step ended.
type StepState string

const (
	StepOK      StepState = "ok"
	StepChanged StepState = "changed"
	StepOpen    StepState = "OPEN"
)

// Outcome is what one step found or did. An open step carries the exact
// command that closes it, for the operator to run.
type Outcome struct {
	State   StepState
	Detail  string
	Command string
}

// Passed is a step that found nothing to do.
func Passed(detail string) Outcome { return Outcome{State: StepOK, Detail: detail} }

// Changed is a step that changed the host.
func Changed(detail string) Outcome { return Outcome{State: StepChanged, Detail: detail} }

// Open is a step that left an item for the operator, closed by command.
func Open(detail, command string) Outcome {
	return Outcome{State: StepOpen, Detail: detail, Command: command}
}

// Result is a finished command.
type Result struct {
	Out, ErrOut string
	Code        int
}

// Lines are the non-empty lines of the output.
func (r Result) Lines() []string { return lines(r.Out) }

func lines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Why is the first line the command said about its failure.
func (r Result) Why() string {
	text := strings.TrimSpace(r.ErrOut)
	if text == "" {
		text = strings.TrimSpace(r.Out)
	}
	if text == "" {
		return fmt.Sprintf("exit status %d", r.Code)
	}
	first, _, _ := strings.Cut(text, "\n")
	return first
}

// FirstLine is the first non-empty line of the output, else of the errors.
func (r Result) FirstLine() string {
	for _, text := range []string{r.Out, r.ErrOut} {
		if l := lines(text); len(l) > 0 {
			return l[0]
		}
	}
	return ""
}

// LastLine is the last non-empty line of the errors, else of the output: the
// line that says why, after any progress lines.
func (r Result) LastLine() string {
	for _, text := range []string{r.ErrOut, r.Out} {
		if l := lines(text); len(l) > 0 {
			return l[len(l)-1]
		}
	}
	return fmt.Sprintf("exit status %d", r.Code)
}

// OrNone is version, or "none" when empty.
func OrNone(version string) string {
	if version == "" {
		return "none"
	}
	return version
}

// Ref names report steps by number: "step 7", "steps 8 and 9".
func Ref(numbers ...int) string {
	words := make([]string, len(numbers))
	for i, n := range numbers {
		words[i] = fmt.Sprint(n)
	}
	switch len(words) {
	case 0:
		return "no step"
	case 1:
		return "step " + words[0]
	}
	return "steps " + strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}
