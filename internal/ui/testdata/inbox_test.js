"use strict";
// Drives the real dashboard scripts through dom_shim.js against fixture
// responses and checks the Inbox tab: its cards, the answers it sends, drafts
// that survive a reload, and a conflict that keeps the typed text.
const assert = require("node:assert/strict");
const path = require("node:path");
const { boot, Event } = require("./dom_shim.js");

const STATIC = path.join(__dirname, "..", "static");
const AT = "2026-11-07T09:00:00Z";
const card = (update, kind, extra = {}) => Object.assign({
  update, kind, item: "t8", title: "API", repo: "r1", repo_slug: "dinoderek/engine", state: "in-review", author: "t8", at: AT,
  body: kind === "question" ? "Merge it?" : "Build is green",
}, extra);
const QUESTION = card(5, "question", {
  class: "review", choices: ["Hold", "Merge", "Close"], recommended: 1, pr: "https://example.test/o/r/change/8",
  latest: { item: "t8", update: 9, author: "t8", kind: "say", at: AT, summary: "Pushing the fix" },
});
const PLAIN = card(7, "question", { item: "p3", title: "Rollout", task: "t8", body: "<b>Open?</b>", class: "product" });
const MESSAGE = card(3, "message", { item: "t9", title: "Docs" });
const inboxOf = (cards) => ({ data: { cards, questions: cards.filter((c) => c.kind === "question").length, messages: cards.filter((c) => c.kind === "message").length }, fetched: AT });

// memory is a localStorage; reloading a page hands it the same one.
function memory() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k),
    key: (i) => [...m.keys()][i] ?? null, get length() { return m.size; }, keys: () => [...m.keys()],
  };
}

async function page(hash, fx, body) {
  fx.posts = [];
  fx.loads = 0;
  fx.storage = fx.storage || memory();
  fx.badge = [];
  const reply = (status, data) => ({ ok: status < 300, status, statusText: "x", json: async () => data });
  const fetchImpl = async (url, init) => {
    const u = new URL(url, "http://127.0.0.1");
    if (init && init.method === "POST") {
      fx.posts.push({ path: u.pathname, body: JSON.parse(init.body) });
      const r = fx.answer(u.pathname, JSON.parse(init.body));
      if (r instanceof Error) throw r;
      return reply(r.status, r.body);
    }
    if (u.pathname === "/api/inbox") {
      fx.loads++;
      if (fx.down) return reply(502, { error: "control server unreachable" });
      return reply(200, fx.inbox);
    }
    if (u.pathname === "/api/sessions") return reply(200, { data: { sessions: [], feedback: 0 }, fetched: AT });
    if (u.pathname === "/api/plans") return reply(200, { data: { repos: [], states: [], total: 0, plans: [] }, fetched: AT });
    return reply(404, { error: "not found" });
  };
  const navigator = { setAppBadge: async (n) => { fx.badge.push(n); }, clearAppBadge: async () => { fx.badge.push(0); } };
  const p = boot(STATIC, hash, fetchImpl, { localStorage: fx.storage, navigator });
  const $ = (id) => p.doc.getElementById(id);
  const t = {
    p, $, fx,
    flush: async () => { for (let i = 0; i < 8; i++) await new Promise((r) => setImmediate(r)); },
    cards: () => $("cards").querySelectorAll("li"),
    card: (update) => $("card-" + update),
    button: (update, text) => t.card(update).querySelectorAll("button").find((b) => b.textContent.startsWith(text)),
    note: (update) => t.card(update).querySelector("textarea"),
    type: (update, text) => { const n = t.note(update); n.value = text; n.dispatchEvent(new Event("input", { bubbles: true })); },
    tap: async (update, text) => { t.button(update, text).click(); await t.flush(); },
    refresh: async () => { p.doc.fire("visibilitychange"); await t.flush(); },
  };
  await t.flush();
  await body(t);
}

const ok = () => ({ status: 200, body: { data: { update: 20 } } });

const scenarios = {
  async "the first tab lists questions then messages, each with what the phone needs"() {
    await page("", { inbox: inboxOf([QUESTION, PLAIN, MESSAGE]), answer: ok }, async (t) => {
      assert.equal(t.$("inbox").hidden, false);
      assert.equal(t.$("list").hidden, true);
      assert.equal(t.$("tab-inbox").getAttribute("aria-current"), "page");
      assert.deepEqual(t.cards().map((c) => c.id), ["card-5", "card-7", "card-3"]);
      assert.equal(t.$("tab-inbox").textContent, "Inbox · 3");
      assert.equal(t.$("inbox-head").textContent, "Inbox · 2 questions · 1 message");
      const q = t.card(5);
      assert.ok(q.textContent.includes("API") && q.textContent.includes("t8 · dinoderek/engine") && q.textContent.includes("review") && q.textContent.includes("Merge it?"));
      assert.equal(q.querySelector(".card-title").getAttribute("href"), "#/inbox?panel=t8&tab=communications&update=5");
      assert.deepEqual(q.querySelectorAll(".choice").map((b) => b.textContent), ["Hold", "Merge ★ recommended", "Close"]);
      assert.equal(q.querySelectorAll(".recommended").length, 1);
      assert.equal(q.querySelector(".card-pr").getAttribute("href"), "https://example.test/o/r/change/8");
      assert.ok(q.querySelector(".card-latest").textContent.includes("Pushing the fix"));
      assert.ok(q.querySelector("textarea"), "a note box");
      assert.ok(t.card(7).textContent.includes("asked by t8"));
      assert.equal(t.card(7).querySelector(".card-body").textContent, "<b>Open?</b>", "bodies are text");
      assert.equal(t.card(7).querySelectorAll("b").length, 0);
      assert.equal(t.card(7).querySelector(".card-pr"), null);
      assert.equal(t.button(3, "Acknowledge") !== undefined, true);
      assert.equal(t.button(5, "Acknowledge"), undefined, "a question is answered, not acknowledged");
      assert.deepEqual(t.fx.badge.slice(-1), [2], "the badge counts questions only");
    });
  },

  async "an empty inbox says so and clears the badge"() {
    await page("#/inbox", { inbox: inboxOf([]), answer: ok }, async (t) => {
      assert.equal(t.$("inbox-empty").hidden, false);
      assert.equal(t.$("inbox-empty").textContent, "Nothing needs you.");
      assert.equal(t.$("tab-inbox").textContent, "Inbox");
      assert.deepEqual(t.fx.badge.slice(-1), [0]);
    });
  },

  async "a tap on a choice resolves with its index, the key and the note, and the card goes"() {
    const fx = { inbox: inboxOf([QUESTION, MESSAGE]), answer: ok };
    await page("#/inbox", fx, async (t) => {
      t.type(5, "  ship it  ");
      fx.inbox = inboxOf([MESSAGE]);
      await t.tap(5, "Merge");
      assert.equal(fx.posts.length, 1);
      assert.equal(fx.posts[0].path, "/api/updates/5/resolve");
      assert.deepEqual(Object.keys(fx.posts[0].body).sort(), ["choice", "key", "note"]);
      assert.equal(fx.posts[0].body.choice, 1);
      assert.equal(fx.posts[0].body.note, "ship it");
      assert.equal(t.card(5), null);
      assert.deepEqual(t.cards().map((c) => c.id), ["card-3"]);
      assert.equal(fx.storage.getItem("engine.draft.5"), null);
      assert.deepEqual(fx.badge.slice(-1), [0]);
    });
  },

  async "an answer without a choice needs a note, and sends only the note"() {
    const fx = { inbox: inboxOf([PLAIN]), answer: ok };
    await page("#/inbox", fx, async (t) => {
      assert.equal(t.button(7, "Answer").hasAttribute("disabled"), true);
      assert.equal(t.button(7, "Reply").hasAttribute("disabled"), true);
      t.type(7, "Yes");
      assert.equal(t.button(7, "Answer").hasAttribute("disabled"), false);
      await t.tap(7, "Answer");
      assert.deepEqual(fx.posts[0], { path: "/api/updates/7/resolve", body: { key: fx.posts[0].body.key, note: "Yes" } });
    });
  },

  async "reply, say and acknowledge use their routes"() {
    const fx = { inbox: inboxOf([QUESTION, MESSAGE]), answer: ok };
    await page("#/inbox", fx, async (t) => {
      t.type(3, "Thanks");
      await t.tap(3, "Reply");
      assert.deepEqual([fx.posts[0].path, fx.posts[0].body.body], ["/api/updates/3/reply", "Thanks"]);
      assert.equal(t.note(3).value, "", "a sent note is cleared");
      assert.equal(t.card(3).querySelector(".card-result").textContent, "Reply sent.");
      t.type(3, "Use the new flag");
      await t.tap(3, "Say");
      assert.deepEqual([fx.posts[1].path, fx.posts[1].body.body], ["/api/items/t9/say", "Use the new flag"]);
      await t.tap(3, "Acknowledge");
      assert.deepEqual([fx.posts[2].path, Object.keys(fx.posts[2].body)], ["/api/updates/3/ack", ["key"]]);
      assert.notEqual(fx.posts[0].body.key, fx.posts[1].body.key);
    });
  },

  async "acknowledge all acknowledges up to the newest message shown, then refreshes"() {
    const LATER = card(9, "message", { item: "t9", title: "Docs", body: "Second notice" });
    const fx = { inbox: inboxOf([QUESTION, MESSAGE, LATER]), answer: ok };
    await page("#/inbox", fx, async (t) => {
      const b = t.$("inbox-ack-all");
      assert.equal(b.hidden, false);
      fx.inbox = inboxOf([QUESTION]);
      const loads = fx.loads;
      b.click();
      await t.flush();
      assert.equal(fx.posts.length, 1);
      assert.deepEqual([fx.posts[0].path, Object.keys(fx.posts[0].body)], ["/api/updates/9/ack-all", ["key"]]);
      assert.equal(t.$("inbox-ack-result").textContent, "Acknowledged every message.");
      assert.ok(fx.loads > loads, "the inbox reloads");
      assert.equal(b.hidden, true, "no messages left to acknowledge");
      assert.deepEqual(t.cards().map((c) => c.id), ["card-5"], "the question stays");
    });
  },

  async "acknowledge all is hidden without messages, and a resend after no answer reuses its key"() {
    await page("#/inbox", { inbox: inboxOf([QUESTION]), answer: ok }, async (t) => {
      assert.equal(t.$("inbox-ack-all").hidden, true);
    });
    let calls = 0;
    const fx = { inbox: inboxOf([MESSAGE]), answer: () => (++calls === 1 ? new Error("offline") : calls === 2 ? { status: 502, body: { error: "control server unreachable" } } : { status: 409, body: { error: "request key was already used" } }) };
    await page("#/inbox", fx, async (t) => {
      const b = t.$("inbox-ack-all");
      b.click();
      await t.flush();
      assert.ok(t.$("inbox-ack-result").textContent.includes("Tap again to retry."));
      b.click();
      await t.flush();
      assert.equal(fx.posts[0].body.key, fx.posts[1].body.key, "a resend reuses the key");
      b.click();
      await t.flush();
      assert.equal(fx.posts[1].body.key, fx.posts[2].body.key);
      assert.equal(t.$("inbox-ack-result").textContent, "request key was already used");
      assert.equal(t.$("inbox-ack-result").className, "bulk-result bad");
    });
  },

  async "a draft survives a reload and goes once it is sent"() {
    const fx = { inbox: inboxOf([QUESTION, MESSAGE]), answer: ok };
    await page("#/inbox", fx, async (t) => {
      t.type(5, "half a thought");
      t.type(3, "another");
      assert.equal(fx.storage.getItem("engine.draft.5"), "half a thought");
    });
    await page("#/inbox", fx, async (t) => {
      assert.equal(t.note(5).value, "half a thought");
      assert.equal(t.note(3).value, "another");
      t.type(3, "");
      assert.equal(fx.storage.getItem("engine.draft.3"), null, "an emptied note is no draft");
      await t.tap(5, "Hold");
      assert.equal(fx.storage.getItem("engine.draft.5"), null);
    });
    fx.storage.setItem("engine.draft.99", "of a card that is gone");
    fx.storage.setItem("unrelated", "kept");
    await page("#/inbox", fx, async () => {
      assert.equal(fx.storage.getItem("engine.draft.99"), null, "a draft of a card the server no longer lists is dropped");
      assert.equal(fx.storage.getItem("unrelated"), "kept");
    });
  },

  async "a 409 keeps the typed text, shows the server's message and refreshes the card"() {
    const fx = { inbox: inboxOf([QUESTION, MESSAGE]), answer: () => ({ status: 409, body: { error: "question already resolved by update 12", reason: "already-resolved" } }) };
    await page("#/inbox", fx, async (t) => {
      t.type(5, "my careful reasoning");
      fx.inbox = inboxOf([MESSAGE]); // it was settled elsewhere
      const loads = fx.loads;
      await t.tap(5, "Close");
      assert.ok(fx.loads > loads, "the card is refreshed");
      const c = t.card(5);
      assert.ok(c, "the card stays so the text can be read");
      assert.equal(t.note(5).value, "my careful reasoning");
      assert.equal(fx.storage.getItem("engine.draft.5"), "my careful reasoning");
      assert.equal(c.querySelector(".card-result").textContent, "question already resolved by update 12");
      assert.ok(c.textContent.includes("no longer in the inbox"));
      assert.equal(t.button(5, "Hold").hasAttribute("disabled"), true);
      assert.equal(t.button(5, "Dismiss").hidden, false);
      await t.tap(5, "Dismiss");
      assert.equal(t.card(5), null);
      assert.equal(fx.storage.getItem("engine.draft.5"), null);
    });
  },

  async "a 409 on a card the server still lists keeps it live"() {
    const fx = { inbox: inboxOf([QUESTION]), answer: () => ({ status: 409, body: { error: "stale" } }) };
    await page("#/inbox", fx, async (t) => {
      t.type(5, "text");
      await t.tap(5, "Hold");
      assert.equal(t.note(5).value, "text");
      assert.equal(t.button(5, "Hold").hasAttribute("disabled"), false);
      assert.equal(t.button(5, "Dismiss").hidden, true);
      assert.equal(t.card(5).querySelector(".card-result").textContent, "stale");
    });
  },

  async "a resend after no answer or a server error reuses the key; another body gets another"() {
    const replies = [new Error("offline"), { status: 502, body: { error: "control server unreachable" } }, { status: 200, body: { data: {} } }];
    const fx = { inbox: inboxOf([QUESTION]), answer: () => replies.shift() };
    await page("#/inbox", fx, async (t) => {
      await t.tap(5, "Hold");
      assert.ok(t.card(5).querySelector(".card-result").textContent.includes("Tap again"));
      await t.tap(5, "Hold");
      assert.deepEqual(fx.posts.map((p) => p.body.key), Array(2).fill(fx.posts[0].body.key));
      t.type(5, "changed my mind");
      await t.tap(5, "Hold");
      assert.notEqual(fx.posts[2].body.key, fx.posts[0].body.key);
      assert.equal(fx.posts[2].body.note, "changed my mind");
    });
  },

  async "a refresh keeps a note being typed and the focus"() {
    const fx = { inbox: inboxOf([QUESTION]), answer: ok };
    await page("#/inbox", fx, async (t) => {
      t.type(5, "typing");
      const area = t.note(5);
      area.focus();
      fx.inbox = inboxOf([Object.assign({}, QUESTION, { latest: { item: "t8", update: 11, author: "t8", summary: "Newer line", at: AT } }), MESSAGE]);
      await t.refresh();
      assert.equal(t.note(5), area, "the same node");
      assert.equal(t.p.doc.activeElement, area);
      assert.equal(area.value, "typing");
      assert.ok(t.card(5).querySelector(".card-latest").textContent.includes("Newer line"), "the card is brought up to date");
      assert.deepEqual(t.cards().map((c) => c.id), ["card-5", "card-3"]);
    });
  },

  async "a new card ahead of the one being typed in leaves the focus alone"() {
    const fx = { inbox: inboxOf([MESSAGE]), answer: ok };
    await page("#/inbox", fx, async (t) => {
      t.type(3, "typing in the message");
      const area = t.note(3);
      area.focus();
      fx.inbox = inboxOf([QUESTION, PLAIN, MESSAGE]); // questions lead, so both arrive ahead of it
      await t.refresh();
      assert.deepEqual(t.cards().map((c) => c.id), ["card-5", "card-7", "card-3"]);
      assert.equal(t.note(3), area);
      assert.equal(t.p.doc.activeElement, area, "focus stays");
      assert.equal(area.value, "typing in the message");
    });
  },

  async "a refused card that moves to the end keeps the focus in its note"() {
    const fx = { inbox: inboxOf([QUESTION, MESSAGE]), answer: () => ({ status: 409, body: { error: "already resolved" } }) };
    await page("#/inbox", fx, async (t) => {
      t.type(5, "my answer");
      fx.inbox = inboxOf([MESSAGE]);
      await t.tap(5, "Hold");
      assert.deepEqual(t.cards().map((c) => c.id), ["card-3", "card-5"], "the card moved behind the live ones");
      t.note(5).focus();
      fx.inbox = inboxOf([card(8, "message", { item: "t9" }), MESSAGE]);
      await t.refresh();
      assert.equal(t.p.doc.activeElement, t.note(5));
      assert.equal(t.note(5).value, "my answer");
    });
  },

  async "an answered card cannot be answered again while it waits to go"() {
    const fx = { inbox: inboxOf([QUESTION]), answer: ok };
    await page("#/inbox", fx, async (t) => {
      fx.inbox = inboxOf([QUESTION]); // the refresh has not dropped it yet
      await t.tap(5, "Hold");
      assert.equal(t.button(5, "Merge").hasAttribute("disabled"), true);
      await t.tap(5, "Merge");
      assert.equal(fx.posts.length, 1, "a second tap sends nothing");
    });
  },

  async "an outage keeps the last inbox, marked stale"() {
    const fx = { inbox: inboxOf([QUESTION]), answer: ok };
    await page("#/inbox", fx, async (t) => {
      fx.down = true;
      await t.refresh();
      assert.equal(t.$("inbox-banner").hidden, false);
      assert.equal(t.$("status").className, "status stale");
      assert.deepEqual(t.cards().map((c) => c.id), ["card-5"]);
      fx.down = false;
      await t.refresh();
      assert.equal(t.$("inbox-banner").hidden, true);
    });
  },

  async "a browser without storage or badges still works"() {
    const fx = { inbox: inboxOf([QUESTION]), answer: ok, storage: { getItem() { throw new Error("denied"); }, setItem() { throw new Error("denied"); }, removeItem() { throw new Error("denied"); }, length: 0, key: () => null } };
    await page("#/inbox", fx, async (t) => {
      t.type(5, "no storage");
      assert.equal(t.note(5).value, "no storage");
      await t.tap(5, "Hold");
      assert.equal(fx.posts.length, 1);
    });
  },

  async "a card opens its item's thread over the inbox"() {
    await page("#/inbox", { inbox: inboxOf([QUESTION]), answer: ok }, async (t) => {
      t.card(5).querySelector(".card-title").click();
      await t.flush();
      assert.equal(t.p.win.location.hash, "#/inbox?panel=t8&tab=communications&update=5");
      assert.equal(t.$("panel").hidden, false);
      assert.equal(t.$("inbox").hidden, false);
    });
  },
};

(async () => {
  let failed = 0;
  for (const [name, fn] of Object.entries(scenarios)) {
    try {
      let timer;
      const limit = new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("timed out")), 5000); });
      await Promise.race([fn(), limit]);
      clearTimeout(timer);
      console.log("ok   " + name);
    } catch (err) {
      failed++;
      console.log("FAIL " + name + "\n" + (err && err.stack || err));
    }
  }
  process.exit(failed ? 1 : 0);
})();
