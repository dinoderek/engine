package harnesstest

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
)

// Target is a host for an adapter's setup steps, answering through Shell as
// setup's runner would.
type Target struct {
	// Shell answers each command; nil fails every one with exit 127.
	Shell func(command, stdin string) (harness.Result, error)
	// Address is how ssh reaches the host; empty is this host.
	Address string
	Entry   hosts.Host
	// Version is the caller's plugin version.
	Version string
	// Absent are the tools the tools step did not find.
	Absent []string
	// Before are the names of the steps the report numbers ahead of the
	// adapter's own: the core's.
	Before []string

	steps []string
}

func (t *Target) Sh(command, stdin string) (harness.Result, error) {
	if t.Shell == nil {
		return harness.Result{ErrOut: "sh: not found", Code: 127}, nil
	}
	return t.Shell(command, stdin)
}

// OnHost is command over ssh, quoted as setup quotes it, for another host.
func (t *Target) OnHost(command string, tty bool) string {
	if !t.Remote() {
		return command
	}
	flags := ""
	if tty {
		flags = "-t "
	}
	return "ssh " + flags + t.Address + " '" + strings.ReplaceAll(command, "'", `'\''`) + "'"
}

func (t *Target) Remote() bool { return t.Address != "" }

func (t *Target) Rerun() string {
	if t.Remote() {
		return "engine setup --host " + t.Entry.Name
	}
	return "engine setup"
}

func (t *Target) Missing(tool string) bool { return slices.Contains(t.Absent, tool) }
func (t *Target) Plugin() string           { return t.Version }
func (t *Target) Host() hosts.Host         { return t.Entry }

// Ref numbers the steps as the report does: Before first, then the
// adapter's.
func (t *Target) Ref(names ...string) string {
	all := append(append([]string(nil), t.Before...), t.steps...)
	numbers := make([]int, len(names))
	for i, name := range names {
		numbers[i] = slices.Index(all, name) + 1
	}
	return harness.Ref(numbers...)
}

// RunSteps runs the adapter's setup steps on t, in order, and returns the
// report setup would print for them: a line per step, numbered after
// t.Before, then a line per open item with its command.
func RunSteps(a harness.Adapter, t *Target) (string, error) {
	steps := a.Setup().Steps(t)
	t.steps = t.steps[:0]
	for _, s := range steps {
		t.steps = append(t.steps, s.Name)
	}
	var out, opens strings.Builder
	for i, s := range steps {
		res, err := s.Run()
		if err != nil {
			return out.String(), err
		}
		n := len(t.Before) + i + 1
		line := fmt.Sprintf("%d. %s: %s", n, s.Name, res.State)
		if res.Detail != "" {
			line += " (" + res.Detail + ")"
		}
		fmt.Fprintln(&out, line)
		if res.State == harness.StepOpen {
			fmt.Fprintf(&opens, "  %d. %s: %s\n", n, s.Name, res.Command)
		}
	}
	return out.String() + opens.String(), nil
}

// checkSetup: the steps have names, unique within the adapter, on another
// host and on this one; on a host where every command fails, every step
// ends open with a command for the operator, and none fails setup; a failed
// check names its fix; the tools are single words.
func checkSetup(t *testing.T, build New) {
	a := build(env(t, failing))
	s := a.Setup()
	for _, tool := range s.Tools() {
		if tool == "" || strings.ContainsAny(tool, " \t") {
			t.Errorf("tool %q: want one word", tool)
		}
	}
	host := hosts.Host{Name: "box", Harnesses: []string{a.Name()}, Bin: map[string]string{a.Name(): "~/bin/" + a.Name()}}
	for _, address := range []string{"dev@box", ""} {
		tgt := &Target{Address: address, Entry: host, Version: "0.1.0", Before: []string{"tools"}}
		var names []string
		for _, st := range s.Steps(tgt) {
			if st.Name == "" || slices.Contains(names, st.Name) || st.Run == nil {
				t.Errorf("address %q: step %q is unnamed, repeated or empty", address, st.Name)
			}
			names = append(names, st.Name)
		}
		report, err := RunSteps(a, tgt)
		if err != nil {
			t.Errorf("address %q: a failing host fails setup: %v", address, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(report), "\n") {
			if strings.Contains(line, ": ok") || strings.Contains(line, ": changed") || strings.HasSuffix(line, ": ") {
				t.Errorf("address %q: on a failing host %q", address, line)
			}
		}
		if detail, command := s.CheckFailed(tgt); detail == "" || command == "" {
			t.Errorf("address %q: a failed check names no fix: %q %q", address, detail, command)
		}
	}
	if _, found, err := s.Plugin(&Target{}); found || err != nil {
		t.Errorf("a host without the harness has its plugin: %v %v", found, err)
	}
}
