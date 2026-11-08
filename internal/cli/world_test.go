package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/hostops"
	"github.com/dinoderek/engine/internal/session"
)

// rolloutBody is a plan description with every supported Markdown feature,
// raw HTML and a link the page must not make active.
const rolloutBody = "## Outcome\n\nShip the **dashboard** to operators.\n\n" +
	"- Plans view\n- Sessions view\n\n" +
	"Read the [control server doc](https://example.test/control) first.\n\n" +
	"```sh\nengine ui\n```\n\n" +
	"<script>alert(1)</script> and a [bad link](javascript:alert(1)).\n"

// progressSays is how many progress messages the Sessions UI task posts:
// more than one thread page of 50, so the thread paginates.
const progressSays = 55

// world is the fixture: two repositories, three plans and their tasks, with
// active planner and executor attempts on two hosts.
type world struct {
	alpha, beta                control.Item
	rollout, hardening, backup control.Item
	// Rollout's tasks.
	schema, sessionsUI, plansView, links, accept, docs control.Item
	// Backup's and hardening's tasks.
	audit, rotate, keys control.Item
	// Attempts by item ID, with each one's tmux window.
	attempts map[string]control.Attempt
	windows  map[string]string
	// Updates the tests open by sequence.
	linksQuestion, linksReply, hardeningQuestion, lastProgress, plansViewSay int
}

type seeder struct {
	t  *testing.T
	s  *controlStack
	op control.Client
	w  *world
	// workers holds each active attempt's execution client.
	workers map[string]control.Client
}

func (sd *seeder) call(c control.Client, r control.Request) control.Response {
	sd.t.Helper()
	out, err := c.Call(context.Background(), r)
	if err != nil {
		sd.t.Fatalf("%s %s: %v", r.Op, r.ID, err)
	}
	return out
}

func (sd *seeder) create(item control.Item) control.Item {
	sd.t.Helper()
	return *sd.call(sd.op, control.Request{Op: "create", Item: item}).Item
}

func (sd *seeder) repo(title, slug string) control.Item {
	return sd.create(control.Item{Kind: "repo", Title: title, Slug: slug, Remote: "ssh://example.test/" + slug})
}

func (sd *seeder) plan(repo control.Item, title, body string) control.Item {
	return sd.create(control.Item{Kind: "plan", Repo: repo.ID, Title: title, Body: body})
}

// task creates a task of plan, in plan's repository, with its prerequisites.
func (sd *seeder) task(plan control.Item, title, body string, depends ...string) control.Item {
	return sd.create(control.Item{Kind: "task", Repo: plan.Repo, Plan: plan.ID, Title: title, Body: body, Depends: depends})
}

func (sd *seeder) state(c control.Client, id, state string) {
	sd.t.Helper()
	got := sd.call(sd.op, control.Request{Op: "get", ID: id}).Item
	sd.call(c, control.Request{Op: "state", ID: id, Version: got.Version, State: state})
}

// run claims an item for host, as a dispatch does, and records its window
// the way a launch does.
func (sd *seeder) run(item control.Item, host, window string) {
	sd.t.Helper()
	out := sd.call(sd.op, control.Request{Op: "claim", ID: item.ID, Body: host, State: "claude"})
	if err := sd.s.service.SetAttempt(out.Attempt.ID, "running", window); err != nil {
		sd.t.Fatal(err)
	}
	sd.w.attempts[item.ID] = *out.Attempt
	sd.w.windows[item.ID] = window
	sd.workers[item.ID] = sd.s.as(out.Credential)
}

func (sd *seeder) post(id, op, body string, replyTo int) int {
	sd.t.Helper()
	if replyTo > 0 {
		return sd.call(sd.op, control.Request{Op: op, Body: body, ReplyTo: replyTo}).Updates[0].ID
	}
	return sd.call(sd.workers[id], control.Request{Op: op, ID: id, Body: body}).Updates[0].ID
}

// seedWorld builds the fixture through the control API.
func seedWorld(t *testing.T, s *controlStack) *world {
	t.Helper()
	sd := &seeder{t: t, s: s, op: s.operator(), w: &world{attempts: map[string]control.Attempt{}, windows: map[string]string{}}, workers: map[string]control.Client{}}
	w := sd.w
	w.alpha = sd.repo("Alpha", "owner/alpha")
	w.beta = sd.repo("Beta", "owner/beta")
	w.rollout = sd.plan(w.alpha, "Dashboard rollout", rolloutBody)
	w.hardening = sd.plan(w.beta, "Control server hardening", "## Outcome\n\nHarden the server.\n")
	w.backup = sd.plan(w.alpha, "Backup restore drill", "Draft.")
	card := "## Outcome\n\nWorks.\n"
	w.schema = sd.task(w.rollout, "Store schema", card)
	w.audit = sd.task(w.backup, "Lock audit", card)
	w.sessionsUI = sd.task(w.rollout, "Sessions UI", "## Outcome\n\n- Feedback first\n- Latest message\n", w.schema.ID)
	w.plansView = sd.task(w.rollout, "Plans view", card)
	w.links = sd.task(w.rollout, "Feedback links", card)
	w.accept = sd.task(w.rollout, "Acceptance", card, w.sessionsUI.ID, w.audit.ID)
	w.docs = sd.task(w.rollout, "Docs pass", card)
	w.rotate = sd.task(w.hardening, "Token rotation", card)
	w.keys = sd.task(w.hardening, "Key storage", card)
	for _, plan := range []control.Item{w.rollout, w.hardening} {
		sd.call(sd.op, control.Request{Op: "approve", ID: plan.ID, Version: plan.Version})
		sd.state(sd.op, plan.ID, "planned")
		sd.state(sd.op, plan.ID, "executing")
	}

	sd.run(w.schema, "devbox", "@9")
	sd.state(sd.workers[w.schema.ID], w.schema.ID, "done")
	if err := s.service.SetAttempt(w.attempts[w.schema.ID].ID, "ended", ""); err != nil {
		t.Fatal(err)
	}
	delete(w.attempts, w.schema.ID)

	sd.run(w.rollout, "devbox", "@1")
	sd.run(w.plansView, "devbox", "@2")
	sd.run(w.links, "devbox", "@3")
	sd.run(w.sessionsUI, "mac", "@1")
	sd.run(w.hardening, "mac", "@2")
	sd.run(w.rotate, "mac", "@3")

	for i := 1; i <= progressSays; i++ {
		w.lastProgress = sd.post(w.sessionsUI.ID, "say", fmt.Sprintf("Progress note %d", i), 0)
	}
	w.plansViewSay = sd.post(w.plansView.ID, "say", "Waiting for the harness to finish a long build", 0)
	w.linksQuestion = sd.post(w.links.ID, "ask", "Should the task table link the same feedback?", 0)
	sd.post(w.links.ID, "ack", "", w.linksQuestion)
	w.linksReply = sd.post(w.links.ID, "reply", "Looking at the mock now", w.linksQuestion)
	w.hardeningQuestion = sd.post(w.hardening.ID, "ask", "May the lock audit slip a week?", 0)
	return w
}

// windowsFor is what each fake host reports for the world's attempts: the
// rollout planner works, Plans view waits for its harness without any
// feedback request, Feedback links waits at its question, and the beta
// sessions are working, waiting and live.
func (w *world) windowsFor(host string) []hostops.Window {
	states := map[string]string{
		w.rollout.ID: session.StateWorking, w.plansView.ID: session.StateWaiting, w.links.ID: session.StateWaiting,
		w.sessionsUI.ID: session.StateWorking, w.hardening.ID: session.StateWaiting, w.rotate.ID: "",
	}
	slugs := map[string]string{w.alpha.ID: "alpha", w.beta.ID: "beta"}
	var out []hostops.Window
	for _, item := range []control.Item{w.rollout, w.plansView, w.links, w.sessionsUI, w.hardening, w.rotate} {
		if w.attempts[item.ID].Host != host {
			continue
		}
		name := fmt.Sprintf("%s-%d", slugs[item.Repo], control.Number(item.ID))
		out = append(out, hostops.Window{Name: name, ID: w.windows[item.ID], Alive: true, State: states[item.ID]})
	}
	return out
}
