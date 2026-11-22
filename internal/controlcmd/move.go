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

// moveFlags turns task move's --host, --harness and --yes into the request's
// move; no other command takes --host or --yes.
func moveFlags(r *control.Request, move control.MoveRequest) error {
	if r.Op != "task-move" {
		if move.Host != "" || move.Confirm {
			return fmt.Errorf("--host and --yes are only for task move")
		}
		return nil
	}
	move.Harness, r.Item.Harness = r.Item.Harness, ""
	r.Move = &move
	return nil
}

// confirmMove handles a move the server refused because it could not read
// the task's host: it prints the warning and, at a terminal, asks. A yes has
// send deliver the move again, confirmed, under the same request key;
// anything else, or no terminal, leaves the task as it was.
func confirmMove(ctx context.Context, e Env, send func(context.Context, control.Request) (control.Response, error), req control.Request, err error) (control.Response, error) {
	var p *control.Problem
	if req.Move == nil || !errors.As(err, &p) || p.Reason != control.ReasonMoveUnconfirmed {
		return control.Response{}, err
	}
	fmt.Fprintln(e.Err, "engine: warning:", p.Message)
	if !e.terminal() {
		return control.Response{}, fmt.Errorf("the task was not moved; rerun with --yes to move it anyway")
	}
	fmt.Fprint(e.Err, "Move it anyway? [y/N] ")
	line, _ := bufio.NewReader(e.In).ReadString('\n')
	if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
		return control.Response{}, fmt.Errorf("the task was not moved")
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
