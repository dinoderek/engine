// Package controlcmd is the server CLI, shared by operators and execution sessions.
package controlcmd

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
)

// Env is the CLI boundary; tests replace streams, credentials and HTTP.
type Env struct {
	In       io.Reader
	Out, Err io.Writer
	Home     string
	Getenv   func(string) string
	Client   *control.Client
	Context  context.Context
	Attach   func([]string) error
	// Server starts the control server's process; nil is the real tmux,
	// ssh, binary and clock.
	Server *ServerDeps
	// Retry is the clock and sleep of a command's retry of an unreachable
	// server; nil is the real ones.
	Retry *Retry
	// Terminal reports whether In is a terminal an operator can answer a
	// confirmation at; nil checks In.
	Terminal func() bool
}

const Usage = "engine serve\n" +
	"engine serve --foreground [--db <path>] [--token-file <path>] [--listen 127.0.0.1:7331] [--advertise <origin>]\n" +
	"engine serve --backup <path> [--db <path>] [--token-file <path>]\n" +
	"engine repo add --title <name> --slug <owner/name> --remote <git-url> [--requires <cap>,…]\n" +
	"engine repo edit <id> --version <n> --requires <cap>,…   (replaces them)\n" +
	"engine plan create --repo <id> --title <text> --body-file <path|->\n" +
	"engine task create --repo <id> --plan <id> --title <text> --body-file <path|->\n" +
	"engine plan show|list|edit|approve|state|stop <id> [--version <n>] [--state <state>]\n" +
	"engine task show|list|edit|state <id> [--version <n>] [--state <state>]\n" +
	"engine lint <plan-id>\n" +
	"engine task complete-external <task-id> --version <n> --pr <url>\n" +
	"engine task merge-ready <task-id> --pr <url> --head <sha> --method squash|merge|rebase --verdict APPROVED\n" +
	"engine task cleaned <task-id> [--left <text>]\n" +
	"engine task stop|resume <task-id>\n" +
	"engine task move <task-id> [--host <host>] [--harness <harness>] [--yes]\n" +
	"engine ps\nengine run <plan-id>\nengine updates <id> [--after <sequence>]\n" +
	"engine tell <id> <text> [--operator]\n" +
	"engine ask <id> <text> [--choice <text>]... [--recommend <index from 0>] [--class <hard-stop class>]\n" +
	"engine inbox [--operator]\n" +
	"engine reply|ack|resolve <sequence> [<text>]\n" +
	"engine ack --all [--repo <id>] [--before <sequence>]\n" +
	"engine merge-queue <repo-id>\n" +
	"engine host status --all [--json]\n" +
	"engine host pause|resume <host> [--json]\n" +
	"engine host move <host> [--harness <harness>] [--yes] [--json]\n" +
	"engine attempt list [<task-id>]\n" +
	"engine attempt reconcile <attempt-id> <evidence> | --body <evidence>\n" +
	"All writes accept --request-key <key>; edits require --version. Output is JSON.\n"

// Main executes a server command. Serve owns its own signal lifecycle.
func Main(args []string, e Env) int {
	if e.Getenv == nil {
		e.Getenv = func(string) string { return "" }
	}
	if len(args) == 0 {
		fmt.Fprint(e.Err, Usage)
		return 2
	}
	if args[0] == "serve" {
		return serveMain(args[1:], e)
	}
	if args[0] == "lint" {
		return lintMain(args[1:], e)
	}
	if args[0] == "attach" {
		return attachMain(args[1:], e)
	}
	if args[0] == "host" && (len(args) < 2 || !slices.Contains([]string{"pause", "resume", "move"}, args[1])) {
		return hostMain(args[1:], e)
	}
	req, err := Parse(args, e)
	if err != nil {
		fmt.Fprintf(e.Err, "engine: %v\n%s", err, Usage)
		return 2
	}
	call, err := newCaller(e)
	if err != nil {
		fmt.Fprintf(e.Err, "engine: %v\n", err)
		return 2
	}
	client, err := makeClient(e)
	if err != nil {
		fmt.Fprintln(e.Err, "engine:", err)
		return 1
	}
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := call(ctx, client, req)
	if err != nil {
		send := func(ctx context.Context, r control.Request) (control.Response, error) { return call(ctx, client, r) }
		out, err = confirmMove(ctx, e, send, req, err)
	}
	if err != nil {
		fmt.Fprintln(e.Err, "engine:", err)
		return 1
	}
	enc := json.NewEncoder(e.Out)
	enc.SetIndent("", "  ")
	if err = enc.Encode(out); err != nil {
		fmt.Fprintln(e.Err, "engine:", err)
		return 1
	}
	return 0
}

// Connect builds the operator's control client from the CLI configuration:
// the server origin and credential every server command uses.
func Connect(e Env) (control.Client, error) { return makeClient(e) }

func makeClient(e Env) (control.Client, error) {
	if e.Client != nil {
		return *e.Client, nil
	}
	dir := hosts.ConfigDir(e.Home)
	url := e.Getenv("ENGINE_SERVER_URL")
	if url == "" {
		var err error
		url, err = hosts.ServerOrigin(dir)
		if err != nil {
			return control.Client{}, err
		}
	}
	if url == "" {
		url = "http://devbox:7331"
	}
	if _, err := control.Endpoint(url); err != nil {
		return control.Client{}, err
	}
	path := e.Getenv("ENGINE_SERVER_TOKEN_FILE")
	if path == "" {
		path = filepath.Join(dir, ServerTokenFile)
	}
	token, err := control.ReadCredential(path)
	return control.Client{URL: url, Token: token}, err
}

// Parse converts CLI flags into explicit domain operations.
func Parse(args []string, e Env) (control.Request, error) {
	r, rest, err := operation(args)
	if err != nil {
		return r, err
	}
	fs := flag.NewFlagSet("engine", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bodyFile := ""
	fs.StringVar(&r.Key, "request-key", "", "")
	fs.IntVar(&r.Version, "version", 0, "")
	fs.StringVar(&r.Item.Repo, "repo", "", "")
	fs.StringVar(&r.Item.Plan, "plan", "", "")
	fs.StringVar(&r.Item.Title, "title", "", "")
	fs.StringVar(&r.Item.Slug, "slug", "", "")
	fs.StringVar(&r.Item.Remote, "remote", "", "")
	fs.StringVar(&r.Item.Harness, "harness", "", "")
	fs.StringVar(&r.Item.Tier, "tier", "", "")
	fs.StringVar(&r.Item.PR, "pr", "", "")
	fs.StringVar(&r.State, "state", r.State, "")
	fs.StringVar(&r.Body, "body", "", "")
	fs.StringVar(&bodyFile, "body-file", "", "")
	fs.StringVar(&r.Attempt, "attempt", e.Getenv("ENGINE_ATTEMPT_ID"), "")
	fs.IntVar(&r.After, "after", 0, "")
	toOperator := fs.Bool("operator", false, "")
	fs.Bool("json", true, "")
	fs.Bool("server", true, "")
	var merge control.MergeRequest
	fs.StringVar(&merge.Head, "head", "", "")
	fs.StringVar(&merge.Method, "method", "", "")
	fs.StringVar(&merge.Verdict, "verdict", "", "")
	left := ""
	fs.StringVar(&left, "left", "", "")
	depends, requires := "", ""
	fs.StringVar(&depends, "depends", "", "")
	fs.StringVar(&requires, "requires", "", "")
	questionFlags(fs, &r)
	all := fs.Bool("all", false, "")
	fs.IntVar(&r.Before, "before", 0, "")
	var move control.MoveRequest
	fs.StringVar(&move.Host, "host", "", "")
	fs.BoolVar(&move.Confirm, "yes", false, "")
	// Go flag parsing stops at a positional; allow IDs and text before flags.
	flags, position := splitFlags(rest)
	if err = fs.Parse(flags); err != nil {
		return r, err
	}
	if r.Op != "ask" && (r.Choices != nil || r.Recommended != nil || r.Class != "") {
		return r, fmt.Errorf("--choice, --recommend and --class are only for ask")
	}
	if err = ackAll(&r, *all); err != nil {
		return r, err
	}
	if *toOperator {
		r.Recipient = "operator"
	}
	if r.Op == "merge-request" {
		merge.PR, r.Item.PR = r.Item.PR, ""
		r.Merge = &merge
	}
	if err = moveFlags(&r, move); err != nil {
		return r, err
	}
	if left != "" && r.Op != "cleaned" {
		return r, fmt.Errorf("--left is only for task cleaned")
	}
	r.Body = cmp.Or(left, r.Body)
	r.Item.Depends = words(depends)
	r.Item.Requires = words(requires)
	if bodyFile != "" {
		body, readErr := readText(bodyFile, e.In)
		if readErr != nil {
			return r, readErr
		}
		r.Item.Body = body
	}
	if r.Op == "create" || r.Op == "edit" {
		if r.Item.Body == "" {
			r.Item.Body = r.Body
		}
		r.Body = ""
	}
	if err = positionals(&r, position); err != nil {
		return r, err
	}
	if r.Body == "-" {
		r.Body, err = readText("-", e.In)
		if err != nil {
			return r, err
		}
	}
	if r.Key == "" {
		r.Key = control.Secret()
	}
	return r, nil
}

// taskOps are the operations only a task has: the CLI's name for each, and
// the request's.
var taskOps = map[string]string{"complete-external": "complete-external", "merge-ready": "merge-request", "cleaned": "cleaned"}

// lifecycleOps are the request ops of the operator's approve, stop and
// resume, by what they act on.
var lifecycleOps = map[string]string{"plan approve": "approve", "plan stop": "stop", "task stop": "task-stop", "task resume": "task-resume", "task move": "task-move"}

func operation(args []string) (control.Request, []string, error) {
	r := control.Request{}
	head, rest := args[0], args[1:]
	switch head {
	case "repo", "plan", "task", "attempt":
		if len(rest) == 0 {
			return r, nil, fmt.Errorf("%s requires an operation", head)
		}
		op := rest[0]
		rest = rest[1:]
		if head == "attempt" {
			if op != "list" && op != "reconcile" {
				return r, nil, fmt.Errorf("attempt supports list or reconcile")
			}
			r.Op = "attempts"
			if op == "reconcile" {
				r.Op = "attempt-reconcile"
			}
			return r, rest, nil
		}
		r.Item.Kind = head
		switch op {
		case "add", "create":
			r.Op = "create"
		case "show":
			r.Op = "get"
		case "list":
			r.Op = "list"
			r.State = head
		case "complete-external", "merge-ready", "cleaned":
			if head != "task" {
				return r, nil, fmt.Errorf("%s requires a task", op)
			}
			r.Op = taskOps[op]
		case "approve", "stop", "resume", "move":
			if r.Op = lifecycleOps[head+" "+op]; r.Op == "" {
				return r, nil, fmt.Errorf("%s requires %s", op, map[string]string{"approve": "a plan", "stop": "a plan or a task", "resume": "a task", "move": "a task"}[op])
			}
		case "edit", "state":
			r.Op = op
		default:
			return r, nil, fmt.Errorf("unknown %s operation %s", head, op)
		}
	case "host":
		r.Op, rest = "host-"+rest[0], rest[1:]
	case "tell":
		r.Op = "say"
	case "ps":
		r.Op = "list"
		r.State = "task"
	case "run", "updates", "ask", "inbox", "reply", "ack", "resolve", "merge-queue":
		r.Op = head
	default:
		return r, nil, fmt.Errorf("unknown server command %s", head)
	}
	return r, rest, nil
}

// questionFlags reads an ask's choices, recommendation and class. Each
// --choice is one choice, taken verbatim: commas never split it.
func questionFlags(fs *flag.FlagSet, r *control.Request) {
	fs.Func("choice", "", func(v string) error {
		r.Choices = append(r.Choices, v)
		return nil
	})
	fs.Func("recommend", "", func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("--recommend takes the recommended choice's index, from 0")
		}
		r.Recommended = &n
		return nil
	})
	fs.StringVar(&r.Class, "class", "", "")
}

// ackAll turns ack --all into the bulk acknowledgement; only it takes
// --before.
func ackAll(r *control.Request, all bool) error {
	if all {
		if r.Op != "ack" {
			return fmt.Errorf("--all is only for ack")
		}
		r.Op = "ack-all"
	}
	if r.Before != 0 && r.Op != "ack-all" {
		return fmt.Errorf("--before is only for ack --all")
	}
	if r.Before < 0 {
		return fmt.Errorf("--before takes an update sequence")
	}
	return nil
}

func splitFlags(args []string) ([]string, []string) {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "--") {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		if strings.Contains(a, "=") || a == "--operator" || a == "--all" || a == "--json" || a == "--server" || a == "--yes" {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, pos
}

func positionals(r *control.Request, pos []string) error {
	if r.Op == "reply" || r.Op == "ack" || r.Op == "resolve" {
		if len(pos) < 1 {
			return fmt.Errorf("update sequence is required")
		}
		n, err := strconv.Atoi(pos[0])
		if err != nil {
			return err
		}
		r.ReplyTo = n
		pos = pos[1:]
	} else if r.Op != "create" && r.Op != "inbox" && r.Op != "ack-all" {
		if len(pos) > 0 {
			r.ID = pos[0]
			pos = pos[1:]
		}
		if r.ID == "" && strings.HasPrefix(r.Op, "host-") {
			return fmt.Errorf("%s requires a host", strings.TrimPrefix(r.Op, "host-"))
		}
		if r.ID == "" && r.Op != "list" && r.Op != "attempts" {
			return fmt.Errorf("item ID is required")
		}
	}
	if len(pos) > 0 {
		if r.Op != "say" && r.Op != "ask" && r.Op != "reply" && r.Op != "resolve" && r.Op != "attempt-reconcile" {
			return fmt.Errorf("unexpected arguments")
		}
		if r.Op == "attempt-reconcile" && r.Body != "" {
			return fmt.Errorf("give the evidence once, as an argument or with --body")
		}
		r.Body = strings.Join(pos, " ")
	}
	return nil
}

func readText(path string, in io.Reader) (string, error) {
	if path == "-" {
		b, err := io.ReadAll(io.LimitReader(in, 2<<20))
		return string(b), err
	}
	b, err := os.ReadFile(path)
	return string(b), err
}
func words(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, v := range strings.Split(s, ",") {
		out = append(out, strings.TrimSpace(v))
	}
	return out
}
