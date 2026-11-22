"use strict";
// The Inbox tab: what waits for the operator, as cards that answer, reply,
// acknowledge and say through the write routes. The server orders the cards;
// this file builds DOM nodes from them, always as text. Cards are kept across
// refreshes, so a note being typed and the focus stay where they were. A note
// is also a draft in localStorage, one per update, until it is sent.
window.engine = window.engine || {};
window.engine.inbox = function (ctx) {
  const { el, keep } = window.engine.dom;
  const { ago } = window.engine.fmt;
  const $ = ctx.$;
  const DRAFT = "engine.draft.";

  const entries = new Map(); // update -> its card's nodes and state
  const forms = new Map(); // a request's JSON -> the form holding its key
  const conflicts = new Map(); // update -> the card a 409 left, kept while the server no longer lists it
  let last = null;
  let bulk = null; // acknowledge all: its route and the form holding its key

  // newestMessage is the highest update among the message cards, or 0.
  function newestMessage(d) {
    return d.cards.reduce((n, c) => (c.kind === "message" ? Math.max(n, c.update) : n), 0);
  }

  // ackAll acknowledges every message up to the newest one shown, never a
  // question. A resend after no answer or a server error reuses the key.
  async function ackAll() {
    const d = last && last.data;
    const newest = d ? newestMessage(d) : 0;
    if (!newest) return;
    const path = "/api/updates/" + newest + "/ack-all";
    if (!bulk || bulk.path !== path) bulk = { path, form: window.engine.writes.form() };
    const b = $("inbox-ack-all");
    const result = $("inbox-ack-result");
    b.setAttribute("disabled", "");
    const r = await bulk.form.send(path, {});
    b.removeAttribute("disabled");
    if (r.status === 0 || r.status >= 500) {
      result.textContent = (r.error || "No answer") + " Tap again to retry.";
      result.className = "bulk-result bad";
      return;
    }
    bulk = null;
    result.textContent = r.ok ? "Acknowledged every message." : r.error || "The dashboard refused the write.";
    result.className = r.ok ? "bulk-result" : "bulk-result bad";
    ctx.refresh();
  }
  $("inbox-ack-all").addEventListener("click", ackAll);

  function storage() {
    try {
      return window.localStorage || null;
    } catch (_) {
      return null;
    }
  }

  function draftOf(update) {
    try {
      const s = storage();
      return (s && s.getItem(DRAFT + update)) || "";
    } catch (_) {
      return "";
    }
  }

  function keepDraft(update, text) {
    try {
      const s = storage();
      if (!s) return;
      if (text) s.setItem(DRAFT + update, text);
      else s.removeItem(DRAFT + update);
    } catch (_) {
      // Without storage a draft lasts as long as the page.
    }
  }

  // pruneDrafts forgets the drafts of cards the server no longer lists.
  function pruneDrafts(live) {
    try {
      const s = storage();
      if (!s) return;
      const stale = [];
      for (let i = 0; i < s.length; i++) {
        const k = s.key(i);
        if (k && k.startsWith(DRAFT) && !live.has(Number(k.slice(DRAFT.length)))) stale.push(k);
      }
      for (const k of stale) s.removeItem(k);
    } catch (_) {
      // Nothing to prune.
    }
  }

  // badge puts the open-question count on the installed app's icon.
  function badge(count) {
    const nav = window.navigator;
    if (!nav) return;
    try {
      const done = count > 0 ? nav.setAppBadge && nav.setAppBadge(count) : nav.clearAppBadge && nav.clearAppBadge();
      if (done && done.catch) done.catch(() => {});
    } catch (_) {
      // A browser without badges shows none.
    }
  }

  function statics(c) {
    const question = c.kind === "question";
    const key = "i-open-" + c.update;
    const open = el("a", { href: ctx.panelHref(c.item, "communications", c.update), "data-key": key, class: "card-title" }, c.title || c.item);
    open.addEventListener("click", () => ctx.noteOpener(key));
    const meta = [c.item + " · " + (c.repo_slug || c.repo || "")];
    if (c.task) meta.push("asked by " + c.task);
    const when = ago(c.at, Date.now());
    meta.push(c.author + (when ? " · " + when : ""));
    const out = [
      el("div", { class: "card-head" },
        el("span", { class: "badge " + c.kind }, question ? "Question" : "Message"),
        c.class ? el("span", { class: "badge class" }, c.class) : null),
      open,
      el("div", { class: "muted" }, meta.join(" · ")),
      el("p", { class: "card-body" }, c.body),
    ];
    const links = [];
    if (c.pr) links.push(el("a", { href: c.pr, target: "_blank", rel: "noopener noreferrer", class: "card-pr" }, "Pull request"));
    if (links.length) out.push(el("div", { class: "card-links" }, ...links));
    if (c.latest && c.latest.update !== c.update && c.latest.summary) {
      out.push(el("div", { class: "card-latest muted" }, "Session: ", c.latest.author + " · " + c.latest.summary));
    }
    return out;
  }

  function button(text, cls, onTap) {
    const b = el("button", { type: "button", class: cls }, text);
    b.addEventListener("click", onTap);
    return b;
  }

  // sync turns a card's buttons on or off for what it can do now.
  function sync(e) {
    const empty = !e.note.value.trim();
    const off = e.busy || e.gone || e.done;
    const set = (b, disabled) => {
      if (disabled) b.setAttribute("disabled", "");
      else b.removeAttribute("disabled");
    };
    e.choices.forEach((b) => set(b, off));
    for (const name of ["answer", "reply", "say"]) if (e[name]) set(e[name], off || empty);
    if (e.ack) set(e.ack, off);
    e.dismiss.hidden = !e.gone;
  }

  function say(e, text, bad) {
    e.result.textContent = text;
    e.result.className = bad ? "card-result bad" : "card-result";
  }

  // send posts one action. A resend of the same request, after no answer or a
  // server error, reuses its key; a definite answer ends the attempt.
  async function send(e, path, fields, sent) {
    const id = JSON.stringify([path, fields]);
    if (!forms.has(id)) forms.set(id, window.engine.writes.form());
    e.busy = true;
    sync(e);
    const r = await forms.get(id).send(path, fields);
    e.busy = false;
    if (r.status === 0 || r.status >= 500) {
      say(e, (r.error || "No answer") + " Tap again to retry.", true);
    } else {
      forms.delete(id);
      settle(e, r, sent);
    }
    sync(e);
  }

  function settle(e, r, sent) {
    if (r.ok) {
      say(e, sent.message, false);
      // An answered or acknowledged card is spent until the refresh drops it.
      if (sent.done) e.done = true;
      if (sent.clear) {
        e.note.value = "";
        keepDraft(e.card.update, "");
      }
      ctx.refresh();
      return;
    }
    // The typed text stays. A conflict means the card is out of date: show
    // the server's message and reload, keeping the card if it is gone.
    say(e, r.error || "The dashboard refused the write.", true);
    if (r.status === 409) {
      conflicts.set(e.card.update, e.card);
      ctx.refresh();
    }
  }

  function actions(e) {
    const c = e.card;
    const note = () => e.note.value.trim();
    const answer = (extra) => send(e, "/api/updates/" + c.update + "/resolve", Object.assign(note() ? { note: note() } : {}, extra), { message: "Answered.", clear: true, done: true });
    const row = el("div", { class: "card-buttons" });
    if (c.kind === "question") {
      (c.choices || []).forEach((text, i) => {
        const rec = c.recommended === i;
        const b = button(text, "choice" + (rec ? " recommended" : ""), () => answer({ choice: i }));
        if (rec) b.append(el("span", { class: "rec" }, " ★ recommended"));
        e.choices.push(b);
        row.append(b);
      });
      e.answer = button("Answer", "act", () => answer({}));
    }
    e.reply = button("Reply", "act", () => send(e, "/api/updates/" + c.update + "/reply", { body: note() }, { message: "Reply sent.", clear: true }));
    if (c.item) e.say = button("Say", "act", () => send(e, "/api/items/" + c.item + "/say", { body: note() }, { message: "Sent.", clear: true }));
    if (c.kind === "message") e.ack = button("Acknowledge", "act", () => send(e, "/api/updates/" + c.update + "/ack", {}, { message: "Acknowledged.", clear: false, done: true }));
    e.dismiss = button("Dismiss", "act", () => dismiss(c.update));
    row.append(...[e.answer, e.reply, e.say, e.ack, e.dismiss].filter(Boolean));
    return row;
  }

  function dismiss(update) {
    keepDraft(update, "");
    conflicts.delete(update);
    if (last) render(last);
  }

  function create(c) {
    const e = { card: c, json: "", busy: false, gone: false, choices: [] };
    e.statics = el("div", { class: "card-static" });
    e.note = el("textarea", { rows: "3", class: "note", "aria-label": "Note for " + c.item, placeholder: c.kind === "question" ? "Note, or an answer" : "Note" });
    e.note.value = draftOf(c.update);
    e.note.addEventListener("input", () => {
      keepDraft(c.update, e.note.value);
      sync(e);
    });
    e.result = el("p", { class: "card-result", role: "status" });
    e.node = el("li", { class: "card " + c.kind, id: "card-" + c.update }, e.statics, e.note, actions(e), e.result);
    return e;
  }

  // show brings a card up to date without touching what the operator is typing.
  function show(e, c, gone) {
    const json = JSON.stringify([c, gone]);
    e.card = c;
    e.gone = gone;
    if (json !== e.json) {
      e.json = json;
      const banner = gone ? el("p", { class: "banner" }, "This is no longer in the inbox; it may have been settled elsewhere.") : "";
      keep(() => e.statics.replaceChildren(...statics(c), banner));
    }
    sync(e);
  }

  // reorder puts the cards in order by inserting only the ones out of place,
  // because moving a card would take the focus from a note being typed.
  function reorder(list, wanted) {
    wanted.forEach((node, i) => {
      if (list.children[i] !== node) list.insertBefore(node, list.children[i] || null);
    });
  }

  function counts(d) {
    const q = d.questions + (d.questions === 1 ? " question" : " questions");
    const m = d.messages + (d.messages === 1 ? " message" : " messages");
    return q + " · " + m;
  }

  // render draws the tab from an envelope. The last good one stays on
  // screen, marked stale, while the control server is down.
  function render(env) {
    last = env;
    const d = env && env.data;
    $("tab-inbox").textContent = d ? "Inbox" + (d.cards.length ? " · " + d.cards.length : "") : "Inbox";
    $("inbox-head").textContent = d ? "Inbox · " + counts(d) : "Inbox";
    $("inbox-banner").hidden = !(env && env.stale);
    $("inbox-ack-all").hidden = !d || !d.messages || !!env.stale;
    $("inbox-banner").textContent = "Control server unavailable; showing the last successful view.";
    const empty = $("inbox-empty");
    if (!d) {
      empty.hidden = false;
      empty.textContent = env ? env.error : "Loading inbox…";
      return;
    }
    badge(d.questions);
    const live = new Set(d.cards.map((c) => c.update));
    for (const update of conflicts.keys()) if (live.has(update)) conflicts.delete(update);
    const wanted = d.cards.map((c) => [c, false]).concat([...conflicts.values()].map((c) => [c, true]));
    for (const [update, e] of entries) {
      if (!wanted.some(([c]) => c.update === update)) {
        entries.delete(update);
        e.node.remove();
      }
    }
    for (const [c, gone] of wanted) {
      if (!entries.has(c.update)) entries.set(c.update, create(c));
      show(entries.get(c.update), c, gone);
    }
    reorder($("cards"), wanted.map(([c]) => entries.get(c.update).node));
    empty.hidden = wanted.length > 0;
    empty.textContent = "Nothing needs you.";
    if (!env.stale && !env.failed) pruneDrafts(new Set(wanted.map(([c]) => c.update)));
  }

  return { render };
};
