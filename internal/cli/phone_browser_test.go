package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
)

// The browser half of TestOperateEngineFromAPhone (phone_acceptance_test.go):
// the inbox, every view and plan and task control at a phone's size, over the
// dashboard's tailnet name.

// tapButton taps the button inside scope whose text starts with label.
func tapButton(b *browser, scope, label string) {
	b.t.Helper()
	var found bool
	b.value(fmt.Sprintf(`(() => { document.querySelectorAll('[data-tap]').forEach((n) => n.removeAttribute('data-tap'));
		const n = [...document.querySelectorAll(%q)].find((n) => n.innerText.trim().startsWith(%q));
		if (n) n.dataset.tap = '1'; return Boolean(n); })()`, scope+" button", label), &found)
	if !found {
		b.t.Fatalf("no %q button in %s", label, scope)
	}
	tap(b, `[data-tap="1"]`)
}

// fitsAPhone fails the test if the view shown scrolls sideways or has a
// control under 44px.
func fitsAPhone(t *testing.T, b *browser, view string) {
	t.Helper()
	if r := tapTargets(b); r.Wide > r.Inner || len(r.Small) > 0 {
		t.Errorf("%s at 390x844: page %dpx wide in %dpx; controls under 44px: %s", view, r.Wide, r.Inner, strings.Join(r.Small, "; "))
	}
}

func (p *phone) inboxOnAPhone() {
	t, b := p.t, p.b
	progress, err := p.worker.Call(context.Background(), control.Request{Op: "say", ID: p.task.ID, Body: "Rewriting the setup section"})
	if err != nil {
		t.Fatal(err)
	}
	card, msg := fmt.Sprintf("#card-%d", p.second), fmt.Sprintf("#card-%d", p.message)
	b.do("Runtime.evaluate", map[string]any{"expression": "document.dispatchEvent(new Event('visibilitychange'))"})
	b.waitFor("the session's latest line on the card", fmt.Sprintf("(document.querySelector('%s .card-latest') || {}).innerText === 'Session: %s · Rewriting the setup section'",
		card, progress.Updates[0].Author))
	var order []string
	b.value("[...document.querySelectorAll('#cards li')].map((n) => n.id)", &order)
	check(t, strings.Join(order, " ") == fmt.Sprintf("card-%d card-%d", p.second, p.message), "the inbox lists the question, then the message: %v", order)
	got := text(b, card)
	for _, want := range []string{"Question", "product", "Docs pass", "Is the wording right?", "Keep it ★ recommended", "Shorten it"} {
		check(t, strings.Contains(got, want), "the question's card lacks %q: %q", want, got)
	}
	check(t, text(b, card+" .choice.recommended") == "Keep it ★ recommended", "the marked choice: %q", text(b, card+" .choice.recommended"))
	check(t, text(b, msg+" .card-body") == "Docs are green and waiting.", "the message's card: %q", text(b, msg))
	var badge int
	b.value("window.badge", &badge)
	check(t, badge == 1, "the app badge shows %d, want the one open question", badge)
	fitsAPhone(t, b, "the inbox")
	b.screenshot(p.shots, "phone-acceptance-inbox")

	typeInto(b, card+" textarea", "Use the short form")
	tapButton(b, card, "Shorten it")
	b.waitFor("the answered card to go", "document.querySelector('"+card+"') === null")
	got2 := p.answers("resolved", p.second)
	check(t, len(got2) == 1 && got2[0] == "Shorten it\nUse the short form", "the inbox resolved %q", got2)

	tapButton(b, msg, "Acknowledge")
	b.waitFor("the acknowledged message to go", "document.querySelectorAll('#cards li').length === 0")
	check(t, len(p.answers("acknowledged", p.message)) == 1, "the message was not acknowledged on the server")
	b.waitFor("the badge cleared", "window.badge === 0")
}

func (p *phone) telegramFollows() {
	t := p.t
	eventually(t, "the answered question's message edited", func() bool {
		e := p.tg.edits(p.secondQ)
		return len(e) > 0 && strings.HasSuffix(e[len(e)-1], "\n\nAnswered: Shorten it\nUse the short form")
	})
	eventually(t, "the digest marked acknowledged", func() bool {
		e := p.tg.edits(p.digest)
		return len(e) > 0 && strings.Contains(e[len(e)-1], "Docs are green and waiting.\nAcknowledged")
	})
	// A tap on a question answered elsewhere says so and answers nothing.
	p.tg.tap("tap-late", operatorChat, p.secondQ, fmt.Sprintf("q:%d:0", p.second))
	eventually(t, "the late tap's answer", func() bool { return len(p.tg.sent("answerCallbackQuery")) >= 3 })
	late := p.tg.sent("answerCallbackQuery")[2]
	check(t, late.Body["callback_query_id"] == "tap-late" && late.Body["text"] == "Already answered", "the late tap: %v", late.Body)
	e := p.tg.edits(p.secondQ)
	check(t, strings.HasSuffix(e[len(e)-1], "\n\nAlready answered"), "the late tap's edit: %q", e)
	got := p.answers("resolved", p.second)
	check(t, len(got) == 1 && got[0] == "Shorten it\nUse the short form", "the late tap resolved again: %q", got)
}

func (p *phone) viewsOnAPhone() {
	b, plan, task := p.b, p.plan.ID, p.task.ID
	for _, v := range []struct{ name, hash, ready string }{
		{"the inbox", "#/inbox", "document.getElementById('inbox-head') !== null && !document.getElementById('inbox-head').hidden"},
		{"the plans list", "#/plans", "document.querySelectorAll('#plans a').length === 1 && !document.getElementById('list').hidden"},
		{"the plan view", "#/plans/" + plan, "document.querySelectorAll('#pane tbody tr').length === 1"},
		{"the item panel", "#/plans/" + plan + "?panel=" + task, "!document.getElementById('panel').hidden && document.getElementById('panel-title').innerText === 'Docs pass'"},
		{"the thread", "#/plans/" + plan + "?panel=" + task + "&tab=communications", "document.querySelectorAll('#messages li').length > 4"},
		{"sessions", "#/sessions", "document.querySelectorAll('#sessions tbody tr').length === 1"},
	} {
		b.do("Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("location.hash = %q", v.hash)})
		b.waitFor(v.name, v.ready)
		fitsAPhone(p.t, b, v.name)
		b.screenshot(p.shots, "phone-acceptance-"+strings.ReplaceAll(strings.TrimPrefix(v.name, "the "), " ", "-"))
	}
}

// confirm opens an action's confirmation sheet and checks it names the item,
// its state and the effect, inside the screen, before anything is sent.
func (p *phone) confirm(action string, says ...string) {
	t, b := p.t, p.b
	b.waitFor("the "+action+" button", "document.querySelector('"+action+"') !== null")
	tap(b, action)
	b.waitFor("the confirmation sheet", "!document.getElementById('sheet-back').hidden && document.activeElement.id === 'sheet-cancel'")
	sheet := text(b, "#sheet")
	for _, want := range says {
		check(t, strings.Contains(sheet, want), "the sheet lacks %q: %q", want, sheet)
	}
	var fit sheetFit
	b.value(`(() => { const r = document.getElementById('sheet').getBoundingClientRect();
		return { Left: r.left, Right: r.right, Top: r.top, Bottom: r.bottom, Width: window.innerWidth, Height: window.innerHeight,
			Wide: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) }; })()`, &fit)
	check(t, fit.Left >= 0 && fit.Right <= fit.Width && fit.Top >= 0 && fit.Bottom <= fit.Height && fit.Wide <= int(fit.Width), "the sheet does not fit the phone: %+v", fit)
	fitsAPhone(t, b, "the sheet")
}

func (p *phone) controlFromAPhone() {
	t, b, plan, task := p.t, p.b, p.plan.ID, p.task.ID
	check(t, readTask(t, p.s, task).State == "running", "the answered task is %s, want running again", readTask(t, p.s, task).State)
	b.do("Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("location.hash = '#/plans/%s'", plan)})

	// Stop the task, after a confirmation; then resume it, at once.
	p.confirm(fmt.Sprintf(`[data-key="act-pane-%s-task-stop"]`, task), "Stop "+task+"?", "Docs pass", "State: Running", "held until you resume it")
	got := readTask(t, p.s, task)
	check(t, got.State == "running" && !got.Held, "opening the sheet changed the task: %+v", got)
	b.screenshot(p.shots, "phone-acceptance-confirm")
	tap(b, "#sheet-ok")
	resume := fmt.Sprintf(`[data-key="act-pane-%s-resume"]`, task)
	b.waitFor("the task held", "document.querySelector('"+resume+"') !== null && document.getElementById('sheet-back').hidden")
	got = readTask(t, p.s, task)
	check(t, got.State == "todo" && got.Held, "the confirmed stop left the task %+v, want todo and held", got)
	tap(b, resume)
	b.waitFor("the resume to land", "document.querySelector('"+resume+"') === null")
	got = readTask(t, p.s, task)
	check(t, got.State == "todo" && !got.Held, "the resume left the task %+v, want todo and not held", got)

	// Stop the plan: a cancelled sheet sends nothing; a confirmed one stops it.
	stop := fmt.Sprintf(`[data-key="act-pane-%s-stop"]`, plan)
	p.confirm(stop, "Stop "+plan+"?", "Phone rollout", "Stops dispatching this plan's tasks")
	tap(b, "#sheet-cancel")
	b.waitFor("the sheet closed", "document.getElementById('sheet-back').hidden")
	check(t, readTask(t, p.s, plan).Coordinating, "cancelling stopped the plan")
	p.confirm(stop, "Stop "+plan+"?")
	tap(b, "#sheet-ok")
	b.waitFor("the plan stopped", fmt.Sprintf("document.querySelector('[data-key=\"act-pane-%s-run\"]') !== null", plan))
	check(t, !readTask(t, p.s, plan).Coordinating, "the confirmed stop left the plan coordinating")
	b.screenshot(p.shots, "phone-acceptance-stopped")
}
