package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/dinoderek/engine/internal/control"
)

// writeLimit caps the JSON body of one write.
const writeLimit = 64 << 10

// writeOps is every control operation a dashboard write may forward. The
// reads in readOps are separate: a write never widens them.
var writeOps = []string{"resolve", "reply", "ack", "say", "approve", "run", "stop", "task-stop", "task-resume", "state", "complete-external"}

// questionRead is the one read a write makes: the updates from a question
// on, to turn a resolve's choice index into the choice's text. Unlike the
// inbox it still lists a question once resolved, so a resend of an applied
// resolve reaches the server's replay.
const questionRead = "updates"

// writeBody is every field a write may carry; each route accepts its own
// subset (writeRoute.fields).
type writeBody struct {
	Key     string `json:"key"`
	Choice  *int   `json:"choice"`
	Note    string `json:"note"`
	Body    string `json:"body"`
	Version int    `json:"version"`
	PR      string `json:"pr"`
}

// writeRoute is one write: its route, what its path names, the fields its
// body takes (true when required) and the control request it becomes.
type writeRoute struct {
	pattern string
	param   string
	noun    string
	valid   func(string) bool
	fields  map[string]bool
	build   func(subject string, b writeBody, choice string) control.Request
}

func isPlan(id string) bool { return itemID(id, 'p') }
func isTask(id string) bool { return itemID(id, 't') }

// updateNumber accepts a positive update sequence written in digits.
func updateNumber(s string) bool {
	n, ok := count(s)
	return ok && n > 0 && strings.Trim(s, "0123456789") == ""
}

// sequence reads an update number that updateNumber accepted.
func sequence(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func keyOnly() map[string]bool { return map[string]bool{"key": true} }

func versioned(more ...string) map[string]bool {
	f := map[string]bool{"key": true, "version": true}
	for _, m := range more {
		f[m] = true
	}
	return f
}

func messaging() map[string]bool { return map[string]bool{"key": true, "body": true} }

// simple is a route whose request is its op on the path's item.
func simple(op string) func(string, writeBody, string) control.Request {
	return func(id string, b writeBody, _ string) control.Request {
		return control.Request{Op: op, ID: id, Key: b.Key}
	}
}

// reaction is a route whose request answers the update on its path.
func reaction(op string) func(string, writeBody, string) control.Request {
	return func(n string, b writeBody, _ string) control.Request {
		return control.Request{Op: op, ReplyTo: control.Number("u" + n), Body: b.Body, Key: b.Key}
	}
}

// writeRoutes is the whole set of writes the dashboard forwards.
var writeRoutes = []writeRoute{
	{"/api/updates/{n}/resolve", "n", "update", updateNumber,
		map[string]bool{"key": true, "choice": false, "note": false},
		func(n string, b writeBody, choice string) control.Request {
			r := reaction("resolve")(n, b, "")
			r.Body = strings.Join(nonEmpty(choice, b.Note), "\n")
			return r
		}},
	{"/api/updates/{n}/reply", "n", "update", updateNumber, messaging(), reaction("reply")},
	{"/api/updates/{n}/ack", "n", "update", updateNumber, keyOnly(), reaction("ack")},
	{"/api/items/{id}/say", "id", "item", panelItem, messaging(),
		func(id string, b writeBody, _ string) control.Request {
			return control.Request{Op: "say", ID: id, Body: b.Body, Key: b.Key}
		}},
	{"/api/plans/{id}/approve", "id", "plan", isPlan, versioned(),
		func(id string, b writeBody, _ string) control.Request {
			return control.Request{Op: "approve", ID: id, Version: b.Version, Key: b.Key}
		}},
	{"/api/plans/{id}/run", "id", "plan", isPlan, keyOnly(), simple("run")},
	{"/api/plans/{id}/stop", "id", "plan", isPlan, keyOnly(), simple("stop")},
	{"/api/tasks/{id}/stop", "id", "task", isTask, keyOnly(), simple("task-stop")},
	{"/api/tasks/{id}/resume", "id", "task", isTask, keyOnly(), simple("task-resume")},
	{"/api/tasks/{id}/retry", "id", "task", isTask, versioned(),
		func(id string, b writeBody, _ string) control.Request {
			return control.Request{Op: "state", State: "todo", ID: id, Version: b.Version, Key: b.Key}
		}},
	{"/api/tasks/{id}/complete-external", "id", "task", isTask, versioned("pr"),
		func(id string, b writeBody, _ string) control.Request {
			return control.Request{Op: "complete-external", ID: id, Version: b.Version, Key: b.Key,
				Item: control.Item{Kind: "task", PR: b.PR}}
		}},
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func registerWrites(mux *http.ServeMux, up *upstream) {
	for _, rt := range writeRoutes {
		mux.HandleFunc("POST "+rt.pattern, rt.serve(up))
	}
}

func (rt writeRoute) serve(up *upstream) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subject := r.PathValue(rt.param)
		if !rt.valid(subject) {
			reply(w, 404, Envelope{Error: "unknown " + rt.noun})
			return
		}
		b, status, msg := readWrite(w, r, rt.fields)
		if msg != "" {
			reply(w, status, Envelope{Error: msg})
			return
		}
		env, status := up.apply(r.Context(), rt, subject, b)
		reply(w, status, env)
	}
}

// refuseWrite checks what every POST must satisfy before any route sees it:
// a JSON body, from the dashboard's own origin. It returns the status and
// reason of a refusal, or zero.
func refuseWrite(r *http.Request) (int, string) {
	if !sameOrigin(r) {
		return http.StatusForbidden, "writes must come from the dashboard's own origin"
	}
	if kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || kind != "application/json" {
		return http.StatusUnsupportedMediaType, "a write is a JSON body"
	}
	return 0, ""
}

// sameOrigin reports whether the request's Origin names the host the request
// was sent to, over http or https: the dashboard's own origin, whichever name
// reached it. A missing, opaque or malformed Origin is another origin.
func sameOrigin(r *http.Request) bool {
	o, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || (o.Scheme != "http" && o.Scheme != "https") || o.User != nil || o.Path != "" || o.RawQuery != "" || o.Fragment != "" {
		return false
	}
	return o.Host != "" && strings.EqualFold(o.Host, r.Host)
}

// readWrite reads and checks a write's body: at most writeLimit bytes, one
// JSON object whose fields are all this route's, with the key and the
// route's required fields present. It returns a status and message on a refusal.
func readWrite(w http.ResponseWriter, r *http.Request, fields map[string]bool) (writeBody, int, string) {
	var b writeBody
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, writeLimit))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return b, http.StatusRequestEntityTooLarge, "a write is at most 64 KiB"
	}
	var present map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &present) != nil || present == nil {
		return b, 400, "a write is one JSON object"
	}
	for name := range present {
		if _, ok := fields[name]; !ok {
			return b, 400, "unknown field " + name
		}
	}
	for name, required := range fields {
		if _, ok := present[name]; required && !ok {
			return b, 400, "field " + name + " is required"
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&b); err != nil {
		return b, 400, "a field has the wrong type"
	}
	if _, ok := fields["version"]; ok && b.Version < 1 {
		return b, 400, "version is the version the page read, from 1"
	}
	if n := len(b.Key); n < 1 || n > 200 {
		return b, 400, "key is 1–200 characters"
	}
	return b, 0, ""
}

// Applied is what a successful write returns: the item's version afterwards
// and the first update it recorded, each when there is one.
type Applied struct {
	Version int `json:"version,omitempty"`
	Update  int `json:"update,omitempty"`
}

// apply turns one checked write into its control request and sends it. The
// control server's status and reason pass through for a refusal.
func (u *upstream) apply(ctx context.Context, rt writeRoute, subject string, b writeBody) (Envelope, int) {
	choice := ""
	if b.Choice != nil {
		var err error
		if choice, err = u.choiceText(ctx, sequence(subject), *b.Choice); err != nil {
			return u.writeFailure(err)
		}
	}
	r := rt.build(subject, b, choice)
	if r.Op == "resolve" && r.Body == "" {
		return Envelope{Error: "a resolution needs a choice or a note"}, 400
	}
	resp, err := u.forward(ctx, r)
	if err != nil {
		return u.writeFailure(err)
	}
	out := Applied{}
	if resp.Item != nil {
		out.Version = resp.Item.Version
	}
	if len(resp.Updates) > 0 {
		out.Update = resp.Updates[0].ID
	}
	return Envelope{Data: out, Fetched: u.now().UTC()}, 200
}

// forward sends a request on the write allowlist, or the one question read,
// and nothing else. A state change is only a retry, to todo.
func (u *upstream) forward(ctx context.Context, r control.Request) (control.Response, error) {
	if !slices.Contains(writeOps, r.Op) && r.Op != questionRead || r.Op == "state" && r.State != "todo" {
		return control.Response{}, errors.New("operation " + r.Op + " is not a dashboard write")
	}
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()
	return u.client.Call(ctx, r)
}

// choiceText is the text of choice i of question n, read from the updates
// that start at it. A question already resolved is the server's to refuse.
func (u *upstream) choiceText(ctx context.Context, n, i int) (string, error) {
	got, err := u.forward(ctx, control.Request{Op: questionRead, After: n - 1})
	if err != nil {
		return "", err
	}
	for _, up := range got.Updates {
		if up.ID != n {
			continue
		}
		if i < 0 || i >= len(up.Choices) {
			return "", &control.Problem{Status: 400, Message: "that question has no such choice"}
		}
		return up.Choices[i], nil
	}
	return "", &control.Problem{Status: 404, Message: "no question with that number"}
}

// writeFailure is failure, with the control server's reason when it gave one.
func (u *upstream) writeFailure(err error) (Envelope, int) {
	status, message := u.failure(err)
	env := Envelope{Error: message}
	var p *control.Problem
	if errors.As(err, &p) && p.Status < 500 {
		env.Reason = p.Reason
	}
	return env, status
}
