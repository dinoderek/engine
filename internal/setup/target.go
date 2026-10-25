package setup

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/dinoderek/engine/internal/remote"
)

// target is the host being set up. Its commands run in the non-interactive
// shell that ssh gives the coordinator, or, for this host, in sh.
type target struct {
	name string
	// address is how ssh reaches the host; empty for this host.
	address string
	exec    remote.Exec
}

func (t target) remote() bool { return t.address != "" }

// label names the target in the report.
func (t target) label() string {
	if t.remote() {
		return t.name + " (" + t.address + ")"
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

// result is a finished command.
type result struct {
	out, errOut string
	code        int
}

// lines are the non-empty lines of the output.
func (r result) lines() []string {
	var lines []string
	for _, l := range strings.Split(r.out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// why is the first line the command said about its failure.
func (r result) why() string {
	text := strings.TrimSpace(r.errOut)
	if text == "" {
		text = strings.TrimSpace(r.out)
	}
	if text == "" {
		return fmt.Sprintf("exit status %d", r.code)
	}
	first, _, _ := strings.Cut(text, "\n")
	return first
}

// sh runs a shell command line on the target, with stdin when given. A host
// that does not answer is an *remote.UnreachableError: nothing it would have
// said is known, so no step may read it as an answer.
func (t target) sh(command, stdin string) (result, error) {
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
		return result{}, err
	}
	res := result{out: out.String(), errOut: errOut.String(), code: code}
	if t.remote() && code == sshExit {
		return result{}, &remote.UnreachableError{Host: t.name, Address: t.address, Detail: res.why()}
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
