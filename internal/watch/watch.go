// Package watch is `engine watch`: a watcher that runs outside the control
// server, calls it every minute, and tells the operator on Telegram when it
// stops answering and when it answers again. It only sends: polling the bot
// would take answers meant for the server's own questions.
package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Interval is the time between calls; ProbeTimeout bounds one call; Strikes
// is how many failures in a row make the server down.
const (
	Interval     = time.Minute
	ProbeTimeout = 30 * time.Second
	Strikes      = 2
)

// StateFile is where the watcher keeps its streak, relative to the home, so
// a restart of the watcher neither forgets a failure nor repeats an alert.
const StateFile = ".local/share/engine/watch.json"

// State is the watcher's streak: failures in a row, when the first of them
// happened, and whether the down message was sent.
type State struct {
	Failures int       `json:"failures"`
	Since    time.Time `json:"since,omitzero"`
	Alerted  bool      `json:"alerted"`
}

// Watcher is one server's watcher. Tests replace Probe, Send and Now.
type Watcher struct {
	// Host names the server's host in the messages.
	Host string
	// Probe makes one authenticated call to the server; an error is a
	// failure.
	Probe func(context.Context) error
	// Send sends one Telegram message to the operator's chat.
	Send func(context.Context, string) error
	Now  func() time.Time
	// StatePath is the state file.
	StatePath string
	// Logf records what went wrong, without the token; nil logs nothing.
	Logf func(string, ...any)
}

// DownMessage is the alert after Strikes failures in a row.
func DownMessage(host string, since time.Time) string {
	return fmt.Sprintf("Engine server on %s is not answering since %s.\n"+
		"Diagnose it: engine server status\nRestart it: engine server restart", host, stamp(since))
}

// UpMessage is the one message when the server answers again.
func UpMessage(host string, since time.Time) string {
	return fmt.Sprintf("Engine server on %s answers again; it was not answering since %s.", host, stamp(since))
}

func stamp(t time.Time) string { return t.Format("2006-01-02 15:04 MST") }

// Pass calls the server once and sends what the streak calls for. A message
// that fails to send is retried on the next pass. The error is the first
// thing that went wrong besides the server itself.
func (w *Watcher) Pass(ctx context.Context) error {
	st, loadErr := w.load()
	probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	down := w.Probe(probeCtx)
	cancel()
	var sendErr error
	switch {
	case down == nil && st.Alerted:
		if sendErr = w.Send(ctx, UpMessage(w.Host, st.Since)); sendErr != nil {
			st.Failures = 0
			break
		}
		st = State{}
	case down == nil:
		st = State{}
	default:
		if w.Logf != nil {
			w.Logf("engine watch: the server did not answer: %v", down)
		}
		// Since stays the first outage's while its recovery is unsent.
		if st.Failures == 0 && !st.Alerted {
			st.Since = w.Now()
		}
		st.Failures++
		if st.Failures >= Strikes && !st.Alerted {
			sendErr = w.Send(ctx, DownMessage(w.Host, st.Since))
			st.Alerted = sendErr == nil
		}
	}
	if sendErr != nil {
		sendErr = fmt.Errorf("send the Telegram message: %w", sendErr)
	}
	return errors.Join(loadErr, sendErr, w.save(st))
}

// Run calls Pass every Interval until ctx ends, logging each error. Wait
// paces it; nil waits on the clock.
func (w *Watcher) Run(ctx context.Context, wait func(context.Context, time.Duration) bool) {
	if wait == nil {
		wait = sleep
	}
	for {
		if err := w.Pass(ctx); err != nil && w.Logf != nil {
			w.Logf("engine watch: %v", err)
		}
		if !wait(ctx, Interval) {
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// load reads the state file. None is a fresh streak; one that does not read
// is too, with the error.
func (w *Watcher) load() (State, error) {
	data, err := os.ReadFile(w.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	var st State
	if err == nil {
		err = json.Unmarshal(data, &st)
	}
	if err != nil {
		return State{}, fmt.Errorf("read the watcher's state, starting afresh: %w", err)
	}
	return st, nil
}

// save writes the state file whole, through a rename.
func (w *Watcher) save(st State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(w.StatePath), 0o700); err != nil {
		return err
	}
	tmp := w.StatePath + ".tmp"
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.StatePath)
}
