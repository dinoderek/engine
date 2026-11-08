// Package ui is `engine ui`: a local, read-only dashboard over the control
// server. The browser talks only to this process, which holds the operator
// credential and forwards a fixed set of reads.
package ui

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// DefaultListen is the dashboard's loopback address; the control server uses 7331.
const DefaultListen = "127.0.0.1:7332"

// Usage is the command's synopsis.
const Usage = "usage: engine ui [--listen " + DefaultListen + "]\n"

// Env is the command boundary. Connect builds the control client from the
// CLI's server configuration and credential.
type Env struct {
	Out, Err io.Writer
	Context  context.Context
	Connect  func() (Caller, error)
	Now      func() time.Time
}

// Main runs the dashboard until the context ends or the process is signalled.
func Main(args []string, e Env) int {
	fs := flag.NewFlagSet("engine ui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", DefaultListen, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprint(e.Err, Usage)
		return 2
	}
	if !LoopbackHost(*listen) {
		fmt.Fprintf(e.Err, "engine ui: --listen must be a loopback address, such as %s\n", DefaultListen)
		return 2
	}
	client, err := e.Connect()
	if err != nil {
		fmt.Fprintln(e.Err, "engine ui:", err)
		return 1
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(e.Err, "engine ui:", err)
		return 1
	}
	ctx := e.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	now := e.Now
	if now == nil {
		now = time.Now
	}
	logf := func(format string, args ...any) { fmt.Fprintf(e.Err, "engine ui: "+format+"\n", args...) }
	fmt.Fprintf(e.Out, "engine ui: serving http://%s/\n", ln.Addr())
	if err = serve(ctx, ln, handler(newUpstream(client, now, logf))); err != nil {
		fmt.Fprintln(e.Err, "engine ui:", err)
		return 1
	}
	return 0
}

// serve runs HTTP on ln until ctx ends, then drains open requests.
func serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	server := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10}
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(ln) }()
	var err error
	select {
	case <-ctx.Done():
	case err = <-errs:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := server.Shutdown(shutdown); e != nil && err == nil {
		err = e
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
