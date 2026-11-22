// Package serverops is `engine server status` and `engine server restart`.
// From any host in hosts.toml they reach the control server's host, the one
// that records a services table: over ssh, or in sh on that host itself.
// Status reads the unit, the API, the database, the disk and the unit's log;
// restart restarts the unit and waits for the server to answer.
package serverops

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/remote"
	"github.com/dinoderek/engine/internal/setup"
)

// Usage is the command's usage.
const Usage = "usage: engine server status|restart [--json]\n"

// Deps are the outside world. Tests replace each one.
type Deps struct {
	Home string
	// Exec runs ssh, or sh on the server's own host.
	Exec remote.Exec
	// Answers makes one authenticated call to the server; an error is a
	// server that does not answer.
	Answers func() error
	// Sleep paces the wait for a restarted server.
	Sleep func(time.Duration)
	// DBPath is the server's database, relative to the server host's home.
	DBPath string
}

// answerTries and answerPause bound the wait for a restarted server to
// answer: about half a minute.
const (
	answerTries = 30
	answerPause = time.Second
)

// server is the server's host as this host reaches it.
type server struct {
	host hosts.Host
	sh   setup.Shell
}

// locate finds the server's host and its shell from this host.
func locate(home string, exec remote.Exec) (server, error) {
	cfg, caller, err := hosts.Open(hosts.ConfigDir(home))
	if err != nil {
		return server{}, err
	}
	h, err := cfg.ServerHost()
	if err != nil {
		return server{}, err
	}
	address := h.Address
	if h.Name == caller.Name {
		address = ""
	}
	return server{host: h, sh: setup.NewShell(h.Name, address, exec)}, nil
}

// run runs one systemctl operation on the server's unit.
func (s server) run(op hosts.ServerOp) (harness.Result, error) {
	line, err := s.host.ServerShellLine(op)
	if err != nil {
		return harness.Result{}, err
	}
	return s.sh.Sh(line, "")
}

// Main runs `engine server <op> [--json]` and returns the exit code: 0 when
// the server is healthy or restarted and answering, 1 when not, 2 on bad
// arguments.
func Main(args []string, d Deps, out, errOut io.Writer) int {
	asJSON := slices.Contains(args, "--json")
	rest := slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--json" })
	if len(rest) != 1 || (rest[0] != "status" && rest[0] != "restart") {
		fmt.Fprint(errOut, Usage)
		return 2
	}
	var r report
	s, err := locate(d.Home, d.Exec)
	switch {
	case err != nil && !asJSON:
		fmt.Fprintf(errOut, "engine server %s: %v\n", rest[0], err)
		return 1
	case err != nil:
		r = report{Op: rest[0]}
		r.add("locate", false, err.Error())
	case rest[0] == "status":
		r = status(s, d)
	default:
		r = restart(s, d)
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
	} else {
		r.print(out)
	}
	if !r.OK {
		return 1
	}
	return 0
}

// report is what a command found, in text or JSON.
type report struct {
	Op      string  `json:"op"`
	Host    string  `json:"host"`
	Address string  `json:"address"`
	Unit    string  `json:"unit"`
	OK      bool    `json:"ok"`
	Checks  []check `json:"checks"`
	// Log is the unit's last log lines; status only.
	Log []string `json:"log,omitempty"`
}

// check is one finding: its name, whether it passed and what it saw.
type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func newReport(op string, s server) report {
	return report{Op: op, Host: s.host.Name, Address: s.host.Address, Unit: s.host.Services.Server, OK: true}
}

func (r *report) add(name string, ok bool, detail string) {
	r.Checks = append(r.Checks, check{Name: name, OK: ok, Detail: detail})
	r.OK = r.OK && ok
}

func (r report) print(out io.Writer) {
	fmt.Fprintf(out, "engine server %s: %s (%s), unit %s\n", r.Op, r.Host, r.Address, r.Unit)
	for _, c := range r.Checks {
		state := "ok"
		if !c.OK {
			state = "FAILED"
		}
		fmt.Fprintf(out, "%s: %s (%s)\n", c.Name, state, c.Detail)
	}
	if r.Op != "status" {
		return
	}
	fmt.Fprintf(out, "last %d log lines:\n", len(r.Log))
	for _, l := range r.Log {
		fmt.Fprintln(out, "  "+l)
	}
}

// why is a failed command's reason: its error, or its output and code.
func why(res harness.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(res.Why())
}

// restart restarts the unit, then waits until the server answers.
func restart(s server, d Deps) report {
	r := newReport("restart", s)
	res, err := s.run(hosts.ServerRestart)
	if err != nil || res.Code != 0 {
		r.add("restart", false, why(res, err)+"; read its log with engine server status")
		return r
	}
	r.add("restart", true, "systemctl restarted "+s.host.Services.Server)
	var last error
	for i := range answerTries {
		if i > 0 {
			d.Sleep(answerPause)
		}
		if last = d.Answers(); last == nil {
			r.add("api", true, "the server answers")
			return r
		}
	}
	r.add("api", false, fmt.Sprintf("no answer within %s: %v; read its log with engine server status", answerTries*answerPause, last))
	return r
}
