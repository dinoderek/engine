// Package pane records on the server what the operator types into the pane
// of an engine session, through the session's prompt hook. The hook tells
// the operator's prompts from engine's by the run's typed-text log; it
// records the operator's verbatim with the pane-input op, which resolves the
// open question that holds the session's item.
//
// It detects a fake, it does not prevent one: the hook signs each post with
// the run's pane key and says whether the text is among the prompts in the
// session's transcript, and the server raises a note for a post that lacks
// either. A session set on faking a post can read its own run directory.
package pane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/harness"
	"github.com/dinoderek/engine/internal/session"
)

// sessionFile and tokenFile are where a server run keeps its connection:
// the server's URL and the attempt, and the execution credential.
const (
	sessionFile = "server-session.json"
	tokenFile   = "server-token"
	// OutcomeLog records, one JSON line per post, what became of each.
	OutcomeLog = "pane.jsonl"
)

// Hook is the prompt hook of the run in Dir.
type Hook struct {
	Dir string
	// Detach starts Post for the prompt with seq in the background; when it
	// is nil or fails, the hook posts at once with Post, without waiting
	// for the transcript.
	Detach func(dir string, seq int) error
	Post   Poster
}

// pending is an operator prompt waiting for its post: the prompt, its
// transcript and when it was submitted, then, once worked out, what the
// post vouches, so every retry sends the same request.
type pending struct {
	Prompt     string             `json:"prompt"`
	Transcript string             `json:"transcript"`
	At         time.Time          `json:"at"`
	Vouch      *control.PaneInput `json:"vouch,omitempty"`
}

// Submitted handles a submitted prompt. Outside a server run, for an empty
// prompt, and for one engine typed or another session submitted, it records
// at most the prompt and posts nothing. The operator's it queues and posts
// in the background, since the harness may record the prompt in its
// transcript only after its hooks have run. Each prompt also starts again
// the post of any earlier one still queued, which a long outage left.
func (h Hook) Submitted(ev harness.Event) error {
	if ev.Kind != harness.PromptSubmitted || strings.TrimSpace(ev.Prompt) == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(h.Dir, sessionFile)); err != nil {
		return nil // not a server run: there is no server to post to
	}
	h.retryQueued()
	p, err := session.RecordPrompt(h.Dir, ev.SessionID, ev.Prompt, h.Post.Now())
	if err != nil || !p.Operator() {
		return err
	}
	data, err := json.Marshal(pending{Prompt: ev.Prompt, Transcript: ev.Transcript, At: p.At})
	if err != nil {
		return err
	}
	if err := os.WriteFile(pendingFile(h.Dir, p.Seq), data, 0o600); err != nil {
		return err
	}
	if h.Detach != nil {
		if err := h.Detach(h.Dir, p.Seq); err == nil {
			return nil
		}
	}
	now := h.Post
	now.Wait, now.RetryFor = 0, 0
	return now.Post(context.Background(), h.Dir, p.Seq)
}

// retryQueued starts in the background the post of each queued prompt. A
// post still running holds its prompt, so the new one leaves it alone.
func (h Hook) retryQueued() {
	if h.Detach == nil {
		return
	}
	queued, _ := filepath.Glob(filepath.Join(h.Dir, "pane-*.json"))
	for _, file := range queued {
		var seq int
		if _, err := fmt.Sscanf(filepath.Base(file), "pane-%d.json", &seq); err == nil {
			_ = h.Detach(h.Dir, seq) // the next prompt tries again
		}
	}
}

func pendingFile(dir string, seq int) string {
	return filepath.Join(dir, fmt.Sprintf("pane-%d.json", seq))
}

// Poster posts a queued operator prompt.
type Poster struct {
	Hooks harness.Hooks
	Now   func() time.Time
	// Sleep waits between reads of the transcript and between tries; nil
	// is time.Sleep.
	Sleep func(time.Duration)
	// Wait bounds how long Post waits for the prompt to reach the
	// transcript; past it, the post says the text is missing there.
	Wait time.Duration
	// RetryFor bounds how long Post retries a post the server did not
	// take for want of an answer or with a server error.
	RetryFor time.Duration
	HTTP     *http.Client
}

// DefaultWait is how long a background post waits for the transcript, and
// DefaultRetry how long it retries an unreachable or failing server.
const (
	DefaultWait  = time.Minute
	DefaultRetry = 15 * time.Minute
)

// pollEvery is how often a post rereads the transcript while it waits;
// firstRetry and lastRetry bound the waits between its tries.
const (
	pollEvery  = time.Second
	firstRetry = 5 * time.Second
	lastRetry  = 2 * time.Minute
)

// Post posts the queued prompt with seq of the run in dir, verbatim, signed
// with the run's pane key, saying whether the session's transcript holds it
// and when it was submitted, and records the outcome in the run's
// OutcomeLog. It retries an unreachable or failing server for RetryFor,
// under one request key, so a repeat is the same request. A posted prompt
// leaves the queue, and so does one the server refused; one still unposted
// stays for the next prompt to post. A post of a prompt another post holds
// does nothing.
func (p Poster) Post(ctx context.Context, dir string, seq int) error {
	file := pendingFile(dir, seq)
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil // another post of this prompt is under way
	}
	var q pending
	if err := json.NewDecoder(f).Decode(&q); err != nil {
		return err
	}
	conn, err := connection(dir)
	if err != nil {
		return p.record(dir, outcome{Seq: seq, Error: err.Error()}, err)
	}
	if q.Vouch == nil {
		q.Vouch = &control.PaneInput{Transcript: p.inTranscript(q), At: q.At}
		q.Vouch.Signature, _ = session.SignPane(dir, control.PaneMessage(conn.Attempt, q.Prompt)) // unsigned is flagged, not refused
		data, err := json.Marshal(q)
		if err != nil {
			return err
		}
		if err := os.WriteFile(file, data, 0o600); err != nil {
			return err
		}
	}
	client := control.Client{URL: conn.URL, Token: conn.token, HTTP: p.HTTP}
	out, err := p.call(ctx, client, control.Request{Op: "pane-input", Key: fmt.Sprintf("pane/%s/%d", conn.Attempt, seq), Body: q.Prompt, Pane: q.Vouch})
	o := outcome{Seq: seq, Signed: q.Vouch.Signature != "", Transcript: q.Vouch.Transcript}
	if err != nil {
		o.Error = err.Error()
		if !retryable(err) {
			_ = os.Remove(file) // the server refused it: posting it again changes nothing
		}
		return p.record(dir, o, err)
	}
	for _, u := range out.Updates {
		o.Updates = append(o.Updates, u.ID)
	}
	if err := p.record(dir, o, nil); err != nil {
		return err
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// call sends r, retrying a server that does not answer or fails, with
// growing waits, until RetryFor has passed.
func (p Poster) call(ctx context.Context, client control.Client, r control.Request) (control.Response, error) {
	deadline := p.Now().Add(p.RetryFor)
	delay := firstRetry
	for {
		out, err := client.Call(ctx, r)
		if err == nil || !retryable(err) || !p.Now().Before(deadline) {
			return out, err
		}
		p.sleep(delay)
		delay = min(2*delay, lastRetry)
	}
}

// retryable reports whether a failed post may succeed later: the server
// did not answer, or failed, rather than refusing it.
func retryable(err error) bool {
	var refused *control.Problem
	return !errors.As(err, &refused) || refused.Status >= 500
}

func (p Poster) sleep(d time.Duration) {
	if p.Sleep == nil {
		time.Sleep(d)
		return
	}
	p.Sleep(d)
}

// inTranscript reports whether the session's transcript holds q's prompt,
// rereading it until Wait has passed.
func (p Poster) inTranscript(q pending) bool {
	want := strings.TrimSpace(q.Prompt)
	deadline := p.Now().Add(p.Wait)
	for {
		if q.Transcript != "" {
			prompts, _ := p.Hooks.Prompts(q.Transcript) // unreadable is not found
			for _, got := range prompts {
				if strings.Contains(got, want) {
					return true
				}
			}
		}
		if !p.Now().Before(deadline) {
			return false
		}
		p.sleep(pollEvery)
	}
}

// outcome is one line of the run's OutcomeLog.
type outcome struct {
	Seq        int       `json:"seq"`
	At         time.Time `json:"at"`
	Updates    []int     `json:"updates,omitempty"`
	Signed     bool      `json:"signed,omitempty"`
	Transcript bool      `json:"transcript,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// record appends o to the run's OutcomeLog and returns cause, joined with
// any error writing it.
func (p Poster) record(dir string, o outcome, cause error) error {
	o.At = p.Now().UTC()
	line, err := json.Marshal(o)
	if err != nil {
		return errors.Join(cause, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, OutcomeLog), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.Join(cause, err)
	}
	_, err = f.Write(append(line, '\n'))
	return errors.Join(cause, err, f.Close())
}

// conn is a server run's connection to the server.
type conn struct {
	URL, Attempt string
	token        string
}

func connection(dir string) (conn, error) {
	var c conn
	data, err := os.ReadFile(filepath.Join(dir, sessionFile))
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil || c.URL == "" || c.Attempt == "" {
		return c, fmt.Errorf("%s in %s names no server and attempt", sessionFile, dir)
	}
	c.token, err = control.ReadCredential(filepath.Join(dir, tokenFile))
	return c, err
}
