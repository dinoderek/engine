package hostops

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/version"
)

// Usage is `engine host`'s usage text.
const Usage = `usage: engine host <op> --json

ops, each printing one JSON object:
  sync-repo --repo <owner/repo>      sync engine's clone of a repo
  spawn --repo <owner/repo> --task <n> --mode hands-off|gated --model <m>
        --effort <e> --run-id <id> [--tier <t>] [--feature <owner/repo#n>]
        [--heavy]                    launch a session; the kickoff is on stdin
  status                             windows, Claude sessions, newest runs, free
                                     slots and free heavy slots
  collect --run-id <id>              a run directory and its run.json
  release --name <repo>-<task> [--failed]
                                     close a session's window and remove its
                                     worktree, kept when --failed
`

// Envelope is what every reply carries.
type Envelope struct {
	Protocol int    `json:"protocol"`
	Engine   string `json:"engine"`
	Host     string `json:"host"`
}

// Failure is the reply of an op that failed. Refused means it changed
// nothing, so the caller can read it as a refusal of its own.
type Failure struct {
	Error   string `json:"error"`
	Refused bool   `json:"refused"`
}

// Main runs `engine host <op> --json`, with args after "host", and returns
// the exit code: 0 for a reply, 1 for a failure, which is a reply too, and 2
// for a call without --json, which gets the usage text.
func Main(args []string, d Deps, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 || !slices.Contains(args[1:], "--json") {
		fmt.Fprint(errOut, Usage)
		return 2
	}
	reply, err := dispatch(args[0], args[1:], d, in)
	env := Envelope{Protocol: Protocol, Engine: strings.TrimPrefix(version.Current().String(), "engine "), Host: hostName(d.Home)}
	code := 0
	if err != nil {
		reply, code = Failure{Error: err.Error(), Refused: isRefusal(err)}, 1
	}
	if err := writeReply(out, env, reply); err != nil {
		fmt.Fprintln(errOut, "engine host:", err)
		return 1
	}
	return code
}

// hostName is this host's name, or "" when its host file says none.
func hostName(home string) string {
	name, _ := hosts.ReadHostName(filepath.Join(hosts.ConfigDir(home), "host"))
	return name
}

// dispatch parses the op's flags and runs it. A call this engine cannot
// parse is refused: a caller on a different engine may send one.
// An unknown op's refusal carries the envelope, which callers read to check
// the protocol; the op name "protocol" stays undefined for that.
func dispatch(op string, args []string, d Deps, in io.Reader) (any, error) {
	fs := flag.NewFlagSet("engine host "+op, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Bool("json", true, "print one JSON object")
	var req SpawnRequest
	var name string
	var failed bool
	run := map[string]func() (any, error){
		"sync-repo": func() (any, error) { return SyncRepo(d, req.Repo) },
		"spawn": func() (any, error) {
			kickoff, err := io.ReadAll(in)
			if err != nil {
				return nil, fmt.Errorf("read the kickoff from stdin: %w", err)
			}
			req.Kickoff = strings.TrimSuffix(string(kickoff), "\n")
			return Spawn(d, req)
		},
		"status":  func() (any, error) { return ReadStatus(d) },
		"collect": func() (any, error) { return Collect(d, req.RunID) },
		"release": func() (any, error) { return Release(d, name, failed) },
	}[op]
	if run == nil {
		return nil, refuse("unknown op %q", op)
	}
	switch op {
	case "sync-repo":
		fs.StringVar(&req.Repo, "repo", "", "")
	case "spawn":
		fs.StringVar(&req.Repo, "repo", "", "")
		fs.IntVar(&req.Task, "task", 0, "")
		fs.StringVar(&req.Feature, "feature", "", "")
		fs.StringVar(&req.Mode, "mode", "", "")
		fs.StringVar(&req.Tier, "tier", "", "")
		fs.StringVar(&req.Model, "model", "", "")
		fs.StringVar(&req.Effort, "effort", "", "")
		fs.StringVar(&req.RunID, "run-id", "", "")
		fs.BoolVar(&req.Heavy, "heavy", false, "")
	case "collect":
		fs.StringVar(&req.RunID, "run-id", "", "")
	case "release":
		fs.StringVar(&name, "name", "", "")
		fs.BoolVar(&failed, "failed", false, "")
	}
	if err := fs.Parse(args); err != nil {
		return nil, refuse("engine host %s: %v", op, err)
	}
	if fs.NArg() > 0 {
		return nil, refuse("engine host %s: unexpected %s", op, strings.Join(fs.Args(), " "))
	}
	return run()
}

// writeReply prints reply as one JSON object, with the envelope's fields added.
func writeReply(out io.Writer, env Envelope, reply any) error {
	fields := map[string]json.RawMessage{}
	for _, part := range []any{reply, env} {
		data, err := json.Marshal(part)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = out.Write(append(data, '\n'))
	return err
}

// isRefusal reports whether err is a refusal.
func isRefusal(err error) bool {
	var r *RefusedError
	return errors.As(err, &r)
}
