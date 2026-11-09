package setup

import (
	"bytes"
	"io"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
)

// target is the host being set up. Its commands run in the non-interactive
// shell that ssh gives the coordinator, or, for this host, in sh.
type target struct {
	name string
	// address is how ssh reaches the host; empty for this host.
	address string
	exec    remote.Exec
	// acts names the harnesses whose steps change this host when setup
	// checks it.
	acts []string
}

func (t target) remote() bool { return t.address != "" }

// label names the target in the report.
func (t target) label() string {
	if t.remote() {
		return t.name + " (" + t.address + ")"
	}
	if len(t.acts) > 0 {
		return t.name + " (this host; the " + strings.Join(t.acts, " and ") + " steps act)"
	}
	return t.name + " (this host, checked only)"
}

// flag is the part of the rerun command that names the host.
func (t target) flag() string {
	if t.remote() {
		return " --host " + t.name
	}
	return ""
}

// Sh runs a shell command line on the target, with stdin when given. A host
// that does not answer is an *remote.UnreachableError: nothing it would have
// said is known, so no step may read it as an answer.
func (t target) Sh(command, stdin string) (harness.Result, error) {
	var in io.Reader
	if stdin != "" {
		in = strings.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	var code int
	var err error
	if t.remote() {
		code, err = remote.SSH{Name: t.name, Address: t.address, Exec: t.exec}.Run(in, &out, &errOut, command)
	} else {
		code, err = t.exec(in, &out, &errOut, "sh", "-c", command)
	}
	if err != nil {
		return harness.Result{}, err
	}
	res := harness.Result{Out: out.String(), ErrOut: errOut.String(), Code: code}
	if t.remote() && code == sshExit {
		return harness.Result{}, &remote.UnreachableError{Host: t.name, Address: t.address, Detail: res.Why()}
	}
	return res, nil
}

// sshExit is the exit code ssh gives when it could not connect or log in.
const sshExit = 255

// onHost is the command as the operator would run it: over ssh for another
// host, as it is for this one. A command that asks for input gets a terminal.
func (t target) onHost(command string, tty bool) string {
	if !t.remote() {
		return command
	}
	flags := ""
	if tty {
		flags = "-t "
	}
	return "ssh " + flags + t.address + " " + remote.ShellJoin([]string{command})
}

// stepTarget is the target as the adapters' steps see it, with what the
// run has found so far.
type stepTarget struct{ r *runner }

func (s stepTarget) Sh(command, stdin string) (harness.Result, error) {
	return s.r.t.Sh(command, stdin)
}
func (s stepTarget) OnHost(command string, tty bool) string { return s.r.t.onHost(command, tty) }
func (s stepTarget) Remote() bool                           { return s.r.t.remote() }
func (s stepTarget) Rerun() string                          { return s.r.rerun() }
func (s stepTarget) Missing(tool string) bool               { return s.r.missing[tool] }
func (s stepTarget) Plugin() string                         { return s.r.want.plugin }
func (s stepTarget) Host() hosts.Host                       { return s.r.host }

// Ref numbers the named steps as this run's report does.
func (s stepTarget) Ref(names ...string) string {
	numbers := make([]int, len(names))
	for i, name := range names {
		numbers[i] = slices.Index(s.r.names, name) + 1
	}
	return harness.Ref(numbers...)
}
