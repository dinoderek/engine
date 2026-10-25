package remote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/hostops"
)

// sshOptions make every call fail rather than ask, or hang: ssh never prompts
// for a password or a host key, and gives up on a host that does not answer.
var sshOptions = []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}

// probeOp is an op no engine defines. Every engine replies to it with a
// refusal that carries the envelope, which is all the protocol check needs.
const probeOp = "protocol" // reserved: hostops must never define it

// UnreachableError is a host that did not answer: ssh could not connect, or
// the call did not complete. Nothing it would have said is known, so a
// caller must not read it as a session that is gone.
type UnreachableError struct{ Host, Address, Detail string }

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("host %s is unreachable over ssh (%s): %s", e.Host, e.Address, e.Detail)
}

// SSH is a host reached over SSH at Address, as `ssh <address> engine host
// <op> --json`. Name is the host's name in hosts.toml.
type SSH struct {
	Name, Address string
	Exec          Exec
}

var _ Client = SSH{}

func (s SSH) SyncRepo(repo string) (hostops.Synced, error) {
	var reply hostops.Synced
	return reply, s.call("sync-repo", []string{"--repo", repo}, "", &reply)
}

func (s SSH) Spawn(r hostops.SpawnRequest) (hostops.Spawned, error) {
	args := []string{"--repo", r.Repo, "--task", strconv.Itoa(r.Task), "--mode", r.Mode,
		"--model", r.Model, "--effort", r.Effort, "--run-id", r.RunID}
	if r.Tier != "" {
		args = append(args, "--tier", r.Tier)
	}
	if r.Feature != "" {
		args = append(args, "--feature", r.Feature)
	}
	var reply hostops.Spawned
	return reply, s.call("spawn", args, r.Kickoff, &reply)
}

func (s SSH) Status() (hostops.Status, error) {
	var reply hostops.Status
	return reply, s.call("status", nil, "", &reply)
}

func (s SSH) Collect(runID string) (hostops.Collected, error) {
	var reply hostops.Collected
	return reply, s.call("collect", []string{"--run-id", runID}, "", &reply)
}

func (s SSH) Release(name string, failed bool) (hostops.Released, error) {
	args := []string{"--name", name}
	if failed {
		args = append(args, "--failed")
	}
	var reply hostops.Released
	return reply, s.call("release", args, "", &reply)
}

// Check streams `engine start --check` on the host to out, after confirming
// the host speaks this protocol, and reports whether it exited 0. Exit 1 is a
// failed check, and its report is already in out.
func (s SSH) Check(out io.Writer) (bool, error) {
	if _, _, _, err := s.exchange(probeOp, nil, ""); err != nil {
		return false, err
	}
	code, err := s.run(nil, out, out, ShellJoin([]string{"engine", "start", "--check"}))
	if err != nil {
		return false, err
	}
	switch code {
	case 0, 1:
		return code == 0, nil
	case sshCode:
		return false, &UnreachableError{s.Name, s.Address, "ssh exited 255 during the check"}
	}
	return false, fmt.Errorf("host %s: engine start --check exited %d", s.Name, code)
}

// run runs one remote command line over ssh and returns its exit code.
func (s SSH) run(in io.Reader, out, errOut io.Writer, command string) (int, error) {
	args := append(append([]string{}, sshOptions...), s.Address, command)
	return s.Exec(in, out, errOut, "ssh", args...)
}

// call runs an op and decodes its reply into reply. A refusal on the far side
// comes back as a *hostops.RefusedError, as if this host had refused.
func (s SSH) call(op string, args []string, stdin string, reply any) error {
	body, code, env, err := s.exchange(op, args, stdin)
	if err != nil {
		return err
	}
	if code != 0 {
		var f hostops.Failure
		if err := json.Unmarshal(body, &f); err != nil || f.Error == "" {
			return fmt.Errorf("host %s: engine host %s exited %d with no error: %s", s.Name, op, code, bytes.TrimSpace(body))
		}
		if f.Refused {
			return &hostops.RefusedError{Reason: strings.TrimSuffix(f.Error, "; nothing was changed")}
		}
		return fmt.Errorf("host %s: %s", s.Name, f.Error)
	}
	if err := json.Unmarshal(body, reply); err != nil {
		return fmt.Errorf("host %s: engine host %s: unreadable reply from engine %s: %w", s.Name, op, env.Engine, err)
	}
	return nil
}

// exchange runs `engine host <op> --json` and returns its reply body and exit
// code, once it is known to be an engine's reply in this protocol. A host that
// did not answer is an *UnreachableError; one that answered something else, or
// in another protocol, is told to run engine setup.
func (s SSH) exchange(op string, args []string, stdin string) ([]byte, int, hostops.Envelope, error) {
	var in io.Reader
	if stdin != "" {
		in = strings.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	command := ShellJoin(append([]string{"engine", "host", op, "--json"}, args...))
	code, err := s.run(in, &out, &errOut, command)
	if err != nil {
		return nil, 0, hostops.Envelope{}, err
	}
	detail := strings.TrimSpace(errOut.String())
	if code == sshCode {
		return nil, 0, hostops.Envelope{}, &UnreachableError{s.Name, s.Address, orExit(detail, code)}
	}
	var env hostops.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		return nil, 0, env, fmt.Errorf("host %s: engine host %s did not reply (%s); is engine current there? run: engine setup --host %s",
			s.Name, op, orExit(detail, code), s.Name)
	}
	if env.Protocol != hostops.Protocol {
		return nil, 0, env, fmt.Errorf("host %s speaks protocol %d (engine %s), this engine speaks %d; run: engine setup --host %s",
			s.Name, env.Protocol, env.Engine, hostops.Protocol, s.Name)
	}
	return out.Bytes(), code, env, nil
}

// orExit is the detail ssh or the shell printed, or else the exit code.
func orExit(detail string, code int) string {
	if detail != "" {
		return detail
	}
	return fmt.Sprintf("exit status %d", code)
}
