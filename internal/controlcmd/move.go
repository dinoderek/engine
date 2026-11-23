package controlcmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dinoderek/engine/internal/control"
)

// moveFlags turns task move's --host, --harness and --yes, and host move's
// --harness and --yes, into the request's move; no other command takes --host
// or --yes, and host move no --host.
func moveFlags(r *control.Request, move control.MoveRequest) error {
	switch {
	case r.Op == "host-move" && move.Host != "":
		return fmt.Errorf("--host is only for task move: host move moves off the host it names")
	case r.Op == "host-pause" || r.Op == "host-resume":
		if move.Host != "" || move.Confirm || r.Item.Harness != "" {
			return fmt.Errorf("%s takes no --host, --harness or --yes", strings.Replace(r.Op, "-", " ", 1))
		}
		return nil
	case r.Op != "task-move" && r.Op != "host-move":
		if move.Host != "" || move.Confirm {
			return fmt.Errorf("--host and --yes are only for task move and host move")
		}
		return nil
	}
	move.Harness, r.Item.Harness = r.Item.Harness, ""
	r.Move = &move
	return nil
}

// confirmMove handles a move the server refused because it could not read
// the host: it prints the warning and, at a terminal, asks. A yes has send
// deliver the move again, confirmed, under the same request key; anything
// else, or no terminal, leaves the task, or the host, as it was.
func confirmMove(ctx context.Context, e Env, send func(context.Context, control.Request) (control.Response, error), req control.Request, err error) (control.Response, error) {
	var p *control.Problem
	if req.Move == nil || !errors.As(err, &p) || p.Reason != control.ReasonMoveUnconfirmed {
		return control.Response{}, err
	}
	fmt.Fprintln(e.Err, "engine: warning:", p.Message)
	left := "the task was not moved"
	if req.Op == "host-move" {
		left = "the host was not paused and its tasks were not moved"
	}
	if !e.terminal() {
		return control.Response{}, fmt.Errorf("%s; rerun with --yes to move it anyway", left)
	}
	fmt.Fprint(e.Err, "Move it anyway? [y/N] ")
	line, _ := bufio.NewReader(e.In).ReadString('\n')
	if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
		return control.Response{}, fmt.Errorf("%s", left)
	}
	confirmed := *req.Move
	confirmed.Confirm = true
	req.Move = &confirmed
	return send(ctx, req)
}

// terminal reports whether the operator can answer at e's input.
func (e Env) terminal() bool {
	if e.Terminal != nil {
		return e.Terminal()
	}
	f, ok := e.In.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
