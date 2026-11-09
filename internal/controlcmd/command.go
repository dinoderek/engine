// Package controlcmd is the server CLI, shared by operators and execution sessions.
package controlcmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
}

const Usage = "engine serve\n" +
	"engine serve --foreground [--db <path>] [--token-file <path>] [--listen 127.0.0.1:7331] [--advertise <origin>]\n" +
	"engine serve --backup <path> [--db <path>] [--token-file <path>]\n" +
	"engine repo add --title <name> --slug <owner/name> --remote <git-url>\n" +
	"engine plan|task create --repo <id> --title <text> --body-file <path|-> [--plan <id>]\n" +
	"engine plan|task show|list|edit|approve|state|stop <id> [--version <n>] [--state <state>]\n" +
	"engine lint <plan-id>\n" +
	"engine task complete-external <task-id> --version <n> --pr <url>\n" +
	"engine task merge-ready <task-id> --pr <url> --head <sha> --method squash|merge|rebase --verdict APPROVED\n" +
	"engine ps\nengine run <plan-or-task-id>\nengine updates <id> [--after <sequence>]\n" +
	"engine tell|ask <id> <text> [--operator]\nengine inbox [--operator]\n" +
	"engine reply|ack|resolve <sequence> [<text>]\n" +
	"engine lock acquire|check|release [--attempt <id>] [--lock-token-file <path>]\n" +
	"engine lock recover <repo-id> --attempt <id> --lock-token-file <path> --body <evidence>\n" +
	"engine attempt list [<task-id>]\n" +
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
	req, err := Parse(args, e)
	if err != nil {
		fmt.Fprintf(e.Err, "engine: %v\n%s", err, Usage)
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
	out, err := client.Call(ctx, req)
	if err != nil {
		fmt.Fprintln(e.Err, "engine:", err)
		return 1
	}
	if out.Lock != nil && (req.Op == "lock-acquire" || req.Op == "lock-check") {
		if dir := e.Getenv("ENGINE_RUN_DIR"); dir != "" {
			b, _ := json.Marshal(out.Lock)
			if err = os.WriteFile(filepath.Join(dir, "merge-lock.json"), b, 0o600); err != nil {
				fmt.Fprintln(e.Err, "engine: store merge lock:", err)
				return 1
			}
		}
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
	bodyFile, tokenFile := "", ""
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
	fs.StringVar(&tokenFile, "lock-token-file", "", "")
	fs.IntVar(&r.After, "after", 0, "")
	toOperator := fs.Bool("operator", false, "")
	fs.Bool("json", true, "")
	fs.Bool("server", true, "")
	var merge control.MergeRequest
	fs.StringVar(&merge.Head, "head", "", "")
	fs.StringVar(&merge.Method, "method", "", "")
	fs.StringVar(&merge.Verdict, "verdict", "", "")
	depends, requires := "", ""
	fs.StringVar(&depends, "depends", "", "")
	fs.StringVar(&requires, "requires", "", "")
	// Go flag parsing stops at a positional; allow IDs and text before flags.
	flags, position := splitFlags(rest)
	if err = fs.Parse(flags); err != nil {
		return r, err
	}
	if *toOperator {
		r.Recipient = "operator"
	}
	if r.Op == "merge-request" {
		merge.PR, r.Item.PR = r.Item.PR, ""
		r.Merge = &merge
	}
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
	if err = lockToken(&r, tokenFile, e); err != nil {
		return r, err
	}
	if r.Key == "" {
		r.Key = control.Secret()
	}
	return r, nil
}

func lockToken(r *control.Request, tokenFile string, e Env) error {
	if strings.HasPrefix(r.Op, "lock-") && r.Op != "lock-acquire" && r.Op != "lock-show" || r.Op == "merge-request" {
		if tokenFile != "" {
			token, readErr := control.ReadCredential(tokenFile)
			if readErr != nil {
				return readErr
			}
			r.Token = token
		} else if dir := e.Getenv("ENGINE_RUN_DIR"); dir != "" {
			b, readErr := os.ReadFile(filepath.Join(dir, "merge-lock.json"))
			if errors.Is(readErr, fs.ErrNotExist) {
				// No lock was taken here: the server says what the
				// request needs.
				return nil
			}
			if readErr != nil {
				return readErr
			}
			var lock control.Lock
			if err := json.Unmarshal(b, &lock); err != nil {
				return err
			}
			r.Token = lock.Token
		}
	}
	return nil
}

func operation(args []string) (control.Request, []string, error) {
	r := control.Request{}
	head, rest := args[0], args[1:]
	switch head {
	case "repo", "plan", "task", "lock", "attempt":
		if len(rest) == 0 {
			return r, nil, fmt.Errorf("%s requires an operation", head)
		}
		op := rest[0]
		rest = rest[1:]
		if head == "lock" {
			r.Op = "lock-" + op
			return r, rest, nil
		}
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
		case "complete-external":
			if head != "task" {
				return r, nil, fmt.Errorf("complete-external requires a task")
			}
			r.Op = op
		case "merge-ready":
			if head != "task" {
				return r, nil, fmt.Errorf("merge-ready requires a task")
			}
			r.Op = "merge-request"
		case "edit", "approve", "state", "stop":
			r.Op = op
		default:
			return r, nil, fmt.Errorf("unknown %s operation %s", head, op)
		}
	case "tell":
		r.Op = "say"
	case "ps":
		r.Op = "list"
		r.State = "task"
	case "run", "updates", "ask", "inbox", "reply", "ack", "resolve":
		r.Op = head
	default:
		return r, nil, fmt.Errorf("unknown server command %s", head)
	}
	return r, rest, nil
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
		if strings.Contains(a, "=") || a == "--operator" || a == "--json" || a == "--server" {
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
	} else if r.Op != "create" && r.Op != "inbox" && !strings.HasPrefix(r.Op, "lock-") {
		if len(pos) > 0 {
			r.ID = pos[0]
			pos = pos[1:]
		}
		if r.ID == "" && r.Op != "list" && r.Op != "attempts" {
			return fmt.Errorf("item ID is required")
		}
	} else if (r.Op == "lock-recover" || r.Op == "lock-show") && len(pos) > 0 {
		r.ID = pos[0]
		pos = pos[1:]
	}
	if len(pos) > 0 {
		if r.Op != "say" && r.Op != "ask" && r.Op != "reply" && r.Op != "resolve" {
			return fmt.Errorf("unexpected arguments")
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
