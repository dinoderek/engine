package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dinoderek/engine/internal/control"
	"github.com/dinoderek/engine/internal/ui"
)

// acceptInbox reads the inbox through the real dashboard process: the two
// open questions the world holds, oldest first, joined with their items,
// repositories and sessions.
func acceptInbox(t *testing.T, d *dashboard, w *world) {
	var inbox ui.InboxList
	status, env := d.get(t, "/api/inbox", &inbox)
	check(t, status == 200 && !env.Stale && inbox.Questions == 2 && inbox.Messages == 0 && len(inbox.Cards) == 2, "inbox: %d %+v", status, inbox)
	if len(inbox.Cards) != 2 {
		return
	}
	links, hardening := inbox.Cards[0], inbox.Cards[1]
	check(t, links.Update == w.linksQuestion && links.Item == w.links.ID && links.Title == "Feedback links" && links.RepoSlug == "owner/alpha" &&
		links.Kind == "question" && links.Body == "Should the task table link the same feedback?", "first card: %+v", links)
	check(t, links.Latest != nil && links.Latest.Update == w.linksReply, "the session's latest line: %+v", links.Latest)
	check(t, hardening.Update == w.hardeningQuestion && hardening.Item == w.hardening.ID && hardening.Title == "Control server hardening" && hardening.RepoSlug == "owner/beta",
		"second card: %+v", hardening)
}

// phoneWorld adds what the phone checks need beyond the shared world: a
// session of the docs task that asked two questions with choices and sent the
// operator a message.
func phoneWorld(t *testing.T, s *controlStack, w *world) phoneQuestions {
	t.Helper()
	sd := &seeder{t: t, s: s, op: s.operator(), w: w, workers: map[string]control.Client{}}
	sd.run(w.docs, "devbox", "@4")
	worker := sd.workers[w.docs.ID]
	rec := 1
	choices := []string{"Hold", "Merge now", "Close the task"}
	ask := func(body string) int {
		return sd.call(worker, control.Request{Op: "ask", ID: w.docs.ID, Body: body, Choices: choices, Recommended: &rec, Class: "review"}).Updates[0].ID
	}
	q := phoneQuestions{first: ask("Merge the docs pass?"), second: ask("Is the wording right?")}
	sd.call(worker, control.Request{Op: "say", ID: w.docs.ID, Body: "Docs are green and waiting.", Recipient: "operator"})
	return q
}

// phoneQuestions are the update numbers of the questions phoneWorld asked.
type phoneQuestions struct{ first, second int }

// Selectors the phone checks share.
const (
	interactive = "a[href], button, input, select, textarea"
	cardsReady  = "document.querySelectorAll('#cards li').length >= 5"
)

// phoneReport is what one view looks like to a thumb: whether the page
// scrolls sideways, and every control smaller than 44px.
type phoneReport struct {
	Wide, Inner int
	Small       []string
}

// tapTargets measures the visible controls of the page being shown. A link
// inside a rendered description or message is as wide as its text, which is
// what a person can aim at in running text; its height is still 44px.
func tapTargets(b *browser) phoneReport {
	var r phoneReport
	b.value(`(() => {
		const visible = (n) => n.getClientRects().length > 0 && getComputedStyle(n).visibility !== 'hidden' && !n.closest('[inert]');
		const small = [];
		for (const n of document.querySelectorAll('`+interactive+`')) {
			if (!visible(n)) continue;
			const r = n.getBoundingClientRect();
			const inText = n.localName === 'a' && n.closest('.markdown') !== null;
			if (r.height < 43.5 || (!inText && r.width < 43.5)) {
				small.push(n.localName + '#' + n.id + '.' + n.className + ' "' + (n.innerText || n.getAttribute('aria-label') || '').trim().slice(0, 24) + '" ' + Math.round(r.width) + 'x' + Math.round(r.height));
			}
		}
		return { Wide: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth), Inner: window.innerWidth, Small: small };
	})()`, &r)
	return r
}

// phoneViews opens every dashboard view at the size of a phone and checks
// each for sideways scrolling and for controls too small to tap.
func phoneViews(t *testing.T, b *browser, w *world, shots string) {
	b.viewport(390, 844, true)
	for _, v := range []struct{ name, hash, ready string }{
		{"inbox", "#/inbox", cardsReady},
		{"plans", "#/plans", "document.querySelectorAll('#plans a').length === 3 && !document.getElementById('list').hidden"},
		{"plan", "#/plans/" + w.rollout.ID, "document.querySelectorAll('#pane tbody tr').length === 6"},
		{"item panel", "#/plans/" + w.rollout.ID + "?panel=" + w.rollout.ID, "document.querySelector('#view-description pre code') !== null && !document.getElementById('panel').hidden"},
		{"thread", "#/plans/" + w.rollout.ID + "?panel=" + w.links.ID + "&tab=communications", "document.querySelectorAll('#messages li').length === 3"},
		{"sessions", "#/sessions", "document.querySelectorAll('#sessions tbody tr').length === 7"},
		{"inbox with a thread", "#/inbox?panel=" + w.docs.ID + "&tab=communications", "document.querySelectorAll('#messages li').length > 0"},
	} {
		b.do("Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("location.hash = %q", v.hash)})
		b.waitFor("the "+v.name+" view", v.ready)
		r := tapTargets(b)
		if r.Wide > r.Inner || len(r.Small) > 0 {
			t.Errorf("%s at 390x844: page %dpx wide in %dpx; controls under 44px: %s", v.name, r.Wide, r.Inner, strings.Join(r.Small, "; "))
		}
		b.screenshot(shots, "phone-"+strings.ReplaceAll(v.name, " ", "-"))
	}
}

// tap presses an element as a finger would: at its centre, so something
// covering it would take the tap.
func tap(b *browser, selector string) {
	b.t.Helper()
	var at struct{ X, Y float64 }
	b.value(fmt.Sprintf(`(() => { const n = document.querySelector(%q); n.scrollIntoView({ block: 'center' });
		const r = n.getBoundingClientRect(); return { X: r.left + r.width / 2, Y: r.top + r.height / 2 }; })()`, selector), &at)
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		b.do("Input.dispatchMouseEvent", map[string]any{"type": kind, "x": at.X, "y": at.Y, "button": "left", "clickCount": 1})
	}
}

// typeInto types text into a field.
func typeInto(b *browser, selector, text string) {
	b.t.Helper()
	b.value(fmt.Sprintf("(document.querySelector(%q).focus(), true)", selector), new(bool))
	b.do("Input.insertText", map[string]any{"text": text})
}

// resolution is the body of the update that resolved question n on item,
// read from the control server, and whether there is one.
func resolution(t *testing.T, s *controlStack, item string, n int) (string, bool) {
	t.Helper()
	got, err := s.operator().Call(context.Background(), control.Request{Op: "updates", ID: item})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range got.Updates {
		if u.Kind == "resolved" && u.ReplyTo == n {
			return u.Body, true
		}
	}
	return "", false
}

// answerOnAPhone answers a question with a tap on a choice, keeps a draft
// across a reload, and keeps the typed text when another surface settled the
// question first.
func answerOnAPhone(b *browser, s *controlStack, w *world, q phoneQuestions, shots string) {
	t := b.t
	first, second := q.first, q.second
	b.do("Runtime.evaluate", map[string]any{"expression": "location.hash = '#/inbox'"})
	b.waitFor("the inbox", cardsReady)
	if got := text(b, "#tab-inbox"); got != "Inbox · 5" {
		t.Errorf("tab %q, want the four questions and the message", got)
	}
	if got := text(b, "#inbox-head"); got != "Inbox · 4 questions · 1 message" {
		t.Errorf("heading %q", got)
	}
	if got := text(b, "#card-"+fmt.Sprint(first)+" .recommended"); !strings.Contains(got, "Merge now") || !strings.Contains(got, "recommended") {
		t.Errorf("the recommended choice is marked: %q", got)
	}

	// A draft survives a reload.
	card := fmt.Sprintf("#card-%d", first)
	typeInto(b, card+" textarea", "Looks right to me")
	b.do("Page.reload", nil)
	b.waitFor("the reloaded inbox", cardsReady+" && document.querySelector('"+card+" textarea').value === 'Looks right to me'")

	// Another surface settles the second question while its card shows: the
	// tap is refused, the text stays, the server's message shows, and the card
	// is kept.
	other := fmt.Sprintf("#card-%d", second)
	typeInto(b, other+" textarea", "My own answer")
	if _, err := s.operator().Call(context.Background(), control.Request{Op: "resolve", ReplyTo: second, Body: "Answered in the pane"}); err != nil {
		t.Fatal(err)
	}
	tap(b, other+" .choice:nth-of-type(1)")
	b.waitFor("the refusal", "document.querySelector('"+other+" .card-result.bad') !== null")
	var kept struct{ Note, Result string }
	b.value(fmt.Sprintf("({ Note: document.querySelector('%s textarea').value, Result: document.querySelector('%s .card-result').innerText })", other, other), &kept)
	if kept.Note != "My own answer" || !strings.Contains(kept.Result, fmt.Sprintf("question %d is already resolved", second)) {
		t.Errorf("after the refusal: %+v", kept)
	}
	if body, ok := resolution(t, s, w.docs.ID, second); !ok || body != "Answered in the pane" {
		t.Errorf("the second question was resolved %q (%t), want the pane's answer only", body, ok)
	}

	// The tap on a choice answers with that choice and the note.
	tap(b, card+" .choice:nth-of-type(2)")
	b.waitFor("the answered card to go", "document.querySelector('"+card+"') === null")
	body, ok := resolution(t, s, w.docs.ID, first)
	if want := "Merge now\nLooks right to me"; !ok || body != want {
		t.Errorf("resolution %q (found %t), want %q", body, ok, want)
	}
	b.waitFor("the question count", "document.getElementById('inbox-head').innerText === 'Inbox · 2 questions · 1 message' && document.querySelector('"+other+"') !== null")
	b.screenshot(shots, "phone-inbox-refused")
}

// installable checks the page offers itself as an app: a manifest with an
// icon that loads, linked from the page.
func installable(t *testing.T, b *browser) {
	var app struct {
		Linked, Name, Display string
		Icons                 []string
		Loaded                []bool
	}
	b.value(`(async () => {
		const href = document.querySelector('link[rel=manifest]').getAttribute('href');
		const m = await (await fetch(href)).json();
		const loaded = await Promise.all(m.icons.map(async (i) => (await fetch(i.src)).ok));
		return { Linked: href, Name: m.name, Display: m.display, Icons: m.icons.map((i) => i.sizes), Loaded: loaded };
	})()`, &app)
	if app.Linked != "/manifest.webmanifest" || app.Name == "" || app.Display != "standalone" || len(app.Icons) < 2 || len(app.Loaded) != len(app.Icons) {
		t.Errorf("manifest: %+v", app)
	}
	for i, ok := range app.Loaded {
		if !ok {
			t.Errorf("icon %s did not load", app.Icons[i])
		}
	}
}
