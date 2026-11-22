package watch

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hosts"
	"github.com/dinoderek/engine/internal/telegram"
)

// Usage is the command's usage line.
const Usage = "usage: engine watch --chat <id>\n"

// Caller is the operator's control client.
type Caller interface {
	Call(context.Context, control.Request) (control.Response, error)
}

// Env is the command's boundary. Tests replace each part.
type Env struct {
	Home string
	Err  io.Writer
	// Connect is the operator's control client, as every server command
	// builds it.
	Connect func() (Caller, error)
	// TelegramURL and TelegramHTTP replace the Bot API's origin and
	// transport; empty and nil are the real ones.
	TelegramURL  string
	TelegramHTTP *http.Client
	Now          func() time.Time
	// Context ends the watch; nil ends it on an interrupt or SIGTERM.
	Context context.Context
	// Wait paces the passes; nil waits on the clock.
	Wait func(context.Context, time.Duration) bool
}

// probe is the call each pass makes: an authenticated read that every
// server version answers. A listener that answers but whose commands hang
// fails it, as a plain connect would not.
var probe = control.Request{Op: "list", State: "repo"}

// Main runs `engine watch --chat <id>` until its context ends. It exits 2 on
// bad flags and 1 when it cannot start.
func Main(args []string, env Env) int {
	fs := flag.NewFlagSet("engine watch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	chatFlag := fs.String("chat", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprint(env.Err, Usage)
		return 2
	}
	chat, err := strconv.ParseInt(*chatFlag, 10, 64)
	if err != nil || chat == 0 {
		fmt.Fprintf(env.Err, "engine watch: --chat must be the Telegram chat ID, not %q\n%s", *chatFlag, Usage)
		return 2
	}
	w, err := build(env, chat)
	if err != nil {
		fmt.Fprintln(env.Err, "engine watch:", err)
		return 1
	}
	ctx := env.Context
	if ctx == nil {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	fmt.Fprintf(env.Err, "engine watch: calling the server on %s every %s\n", w.Host, Interval)
	w.Run(ctx, env.Wait)
	return 0
}

// build is the watcher for the server's host in hosts.toml, sending to chat
// with the token copy setup wrote.
func build(env Env, chat int64) (*Watcher, error) {
	dir := hosts.ConfigDir(env.Home)
	cfg, err := hosts.Load(filepath.Join(dir, "hosts.toml"))
	if err != nil {
		return nil, err
	}
	server, err := cfg.ServerHost()
	if err != nil {
		return nil, err
	}
	tokenPath := filepath.Join(dir, hosts.WatchTokenFile)
	token, err := control.ReadCredential(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("the bot token copy %s: %w; rerun engine setup", tokenPath, err)
	}
	client, err := env.Connect()
	if err != nil {
		return nil, err
	}
	bot := telegram.Bot{Token: token, BaseURL: env.TelegramURL, HTTP: env.TelegramHTTP}
	now := env.Now
	if now == nil {
		now = time.Now
	}
	return &Watcher{
		Host: server.Name,
		Probe: func(ctx context.Context) error {
			_, err := client.Call(ctx, probe)
			return err
		},
		Send:      func(ctx context.Context, text string) error { return bot.Send(ctx, chat, text) },
		Now:       now,
		StatePath: filepath.Join(env.Home, StateFile),
		Logf:      func(format string, args ...any) { fmt.Fprintf(env.Err, format+"\n", args...) },
	}, nil
}
