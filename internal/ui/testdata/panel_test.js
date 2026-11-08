"use strict";
// Drives the real dashboard scripts through dom_shim.js against fixture
// responses and checks the item panel's keyboard and focus behaviour.
const assert = require("node:assert/strict");
const path = require("node:path");
const { boot, Event } = require("./dom_shim.js");

const STATIC = path.join(__dirname, "..", "static");
const AT = "2026-11-07T09:00:00Z";
// desc names an element briefly, so a failed comparison prints a line, not a tree.
const desc = (e) => (e && e.localName ? "<" + e.localName + (e.id ? "#" + e.id : "") + (e.dataset.key ? " " + e.dataset.key : "") + ">" : String(e));
const is = (got, want, why) => assert.ok(got === want, (why || "element") + ": got " + desc(got) + ", want " + desc(want));
const run = (txt) => [{ text: txt }];

const PLAN = {
  data: {
    plan: { id: "p24", repo: "r1", slug: "dinoderek/engine", title: "Control server rollout", state: "executing" },
    counts: { done: 0, in_progress: 1, ready: 1, blocked: 0 },
    tasks: [
      { id: "t30", title: "Acceptance", state: "running", group: "in-progress", host: "mac", waiting: [] },
      { id: "t31", title: "Docs", state: "todo", group: "ready", waiting: [] },
    ],
  },
  fetched: AT,
};
const PLANS = {
  data: {
    repos: [{ id: "r1", slug: "dinoderek/engine" }], states: ["executing"], total: 1,
    plans: [{ id: "p24", repo: "r1", slug: "dinoderek/engine", title: "Control server rollout", state: "executing" }],
  },
  fetched: AT,
};
const DESCRIPTION = [
  { type: "heading", level: 2, inline: run("Outcome") },
  { type: "paragraph", inline: [{ text: "<script>alert(1)</script> " }, { text: "bad" }, { text: " " }, { text: "docs", href: "https://example.com/d" }] },
  { type: "list", items: [run("one"), [{ text: "run " }, { text: "./dev test", code: true }]] },
  { type: "code", lang: "go", text: "func main() {}" },
];
const item = (id, kind, description) => ({ data: { id, kind, repo: "r1", title: id === "p24" ? "Control server rollout" : "Task " + id, state: "running", description }, fetched: AT });

// message builds a thread entry.
const msg = (id, kind, extra = {}) => Object.assign({ id, author: "operator", at: AT, kind, body: "m" + id }, extra);
const BASE = [
  msg(2, "ask", { body: "Approve the API?", recipient: "operator", requires_reply: true }),
  msg(3, "acknowledged", { body: "", reply_to: 2 }),
  msg(4, "reply", { reply_to: 2 }),
];

// page runs a scenario against fixtures it can change as it goes.
async function page(hash, fx, body, opts) {
  fx.log = [];
  fx.items = fx.items || { p24: item("p24", "plan", DESCRIPTION), t30: item("t30", "task", []) };
  fx.questions = fx.questions || [{ id: 2, state: "open", acknowledged: true, replies: 1 }];
  fx.thread = fx.thread || ((id, after) => ({ item: id, messages: BASE.filter((m) => m.id > after), questions: fx.questions, next: 4, more: false }));
  const fetchImpl = async (url) => {
    const u = new URL(url, "http://127.0.0.1");
    fx.log.push(u.pathname + u.search);
    const reply = (status, data) => ({ ok: status < 300, status, statusText: "x", json: async () => data });
    if (fx.down) throw new TypeError("fetch failed");
    let m;
    if (u.pathname === "/api/plans") return reply(200, PLANS);
    if (u.pathname === "/api/plans/p24") return reply(200, PLAN);
    if ((m = u.pathname.match(/^\/api\/items\/([pt]\d+)\/thread$/))) {
      return reply(200, { data: fx.thread(m[1], Number(u.searchParams.get("after")), Number(u.searchParams.get("anchor"))), fetched: AT });
    }
    if ((m = u.pathname.match(/^\/api\/items\/([pt]\d+)$/))) {
      return fx.items[m[1]] ? reply(200, fx.items[m[1]]) : reply(404, { error: "unknown item" });
    }
    return reply(404, { error: "not found" });
  };
  const p = boot(STATIC, hash, fetchImpl, opts);
  const $ = (id) => p.doc.getElementById(id);
  const t = {
    p, $, fx,
    flush: async () => { for (let i = 0; i < 8; i++) await new Promise((r) => setImmediate(r)); },
    active: () => p.doc.activeElement,
    key: (el, key) => el.dispatchEvent(new Event("keydown", { bubbles: true, key })),
    opener: (id) => p.doc.querySelector('[data-key="open-' + id + '"]'),
    text: (id) => $(id).textContent,
    refresh: async () => { p.doc.fire("visibilitychange"); await t.flush(); },
  };
  await t.flush();
  await body(t);
}

const scenarios = {
  async "the page loads the scripts index.html names, in order"() {
    await page("#/plans/p24", {}, async (t) => assert.deepEqual(t.p.scripts, ["/dom.js", "/panel.js", "/sessions.js", "/app.js"]));
  },

  async "opening from a link focuses the panel and closing returns focus to the link"() {
    await page("#/plans/p24", {}, async (t) => {
      assert.equal(t.$("panel").hidden, true);
      const link = t.opener("p24");
      assert.match(link.getAttribute("href"), /panel=p24/);
      link.focus();
      link.click();
      await t.flush();
      assert.equal(t.$("panel").hidden, false);
      is(t.active(), t.$("panel-title"));
      assert.equal(t.$("workspace").classList.contains("with-panel"), true);
      assert.equal(t.text("panel-title"), "Control server rollout");
      assert.equal(t.text("panel-ref"), "p24 · dinoderek/engine");
      t.key(t.$("panel-close"), "Escape");
      assert.equal(t.$("panel").hidden, true);
      assert.equal(t.active().dataset.key, "open-p24");
      assert.doesNotMatch(t.p.win.location.hash, /panel=/);
    });
  },

  async "the close button closes the panel and restores focus to the task link"() {
    await page("#/plans/p24", {}, async (t) => {
      const link = t.opener("t30");
      link.focus();
      link.click();
      await t.flush();
      assert.equal(t.text("panel-title"), "Task t30");
      t.$("panel-close").focus();
      t.$("panel-close").click();
      assert.equal(t.$("panel").hidden, true);
      assert.equal(t.active().dataset.key, "open-t30");
    });
  },

  async "closing a panel opened from the address falls back to the task pane"() {
    await page("#/plans/p24?panel=t30", {}, async (t) => {
      assert.equal(t.$("panel").hidden, false);
      t.key(t.$("panel-title"), "Escape");
      is(t.active(), t.$("pane"));
    });
  },

  async "descriptions render the Markdown subset and keep HTML and unsafe links inert"() {
    await page("#/plans/p24?panel=p24", {}, async (t) => {
      const box = t.$("view-description");
      assert.equal(box.querySelector("h4").textContent, "Outcome");
      assert.equal(box.querySelectorAll("li").length, 2);
      assert.equal(box.querySelector("pre").textContent, "func main() {}");
      assert.equal(box.querySelector("code").textContent, "./dev test");
      assert.equal(box.querySelector("script"), null);
      assert.ok(box.textContent.includes("<script>alert(1)</script> bad docs"));
      const links = box.querySelectorAll("a");
      assert.equal(links.length, 1);
      assert.equal(links[0].getAttribute("href"), "https://example.com/d");
      assert.equal(links[0].getAttribute("rel"), "noopener noreferrer");
    });
  },

  async "the page refuses an unsafe link even if the server sent one"() {
    const bad = [{ type: "paragraph", inline: [{ text: "x", href: "javascript:alert(1)" }, { text: "y", href: " https://example.com" }, { text: "z", href: "data:text/html,x" }] }];
    await page("#/plans/p24?panel=p24", { items: { p24: item("p24", "plan", bad) } }, async (t) => {
      assert.equal(t.$("view-description").querySelectorAll("a").length, 0);
      assert.equal(t.text("view-description"), "xyz");
    });
  },

  async "a refresh leaves an unchanged description alone"() {
    await page("#/plans/p24?panel=p24", {}, async (t) => {
      const first = t.$("view-description").children[0];
      await t.refresh();
      is(t.$("view-description").children[0], first, "description nodes");
    });
  },

  async "a missing description and an unknown item say so"() {
    await page("#/plans/p24?panel=t30", {}, async (t) => assert.equal(t.text("view-description"), "No description."));
    await page("#/plans/p24?panel=t99", {}, async (t) => {
      assert.equal(t.text("view-description"), "unknown item");
      assert.equal(t.$("panel-banner").hidden, false);
      assert.equal(t.text("panel-title"), "Unavailable");
    });
  },

  async "tabs follow the arrow, home and end keys and keep one tab in the order"() {
    await page("#/plans/p24?panel=p24", {}, async (t) => {
      const [d, c] = [t.$("tab-description"), t.$("tab-communications")];
      d.focus();
      assert.equal([d, c].filter((x) => x.getAttribute("tabindex") === "0").length, 1);
      t.key(d, "ArrowRight");
      await t.flush();
      is(t.active(), c);
      assert.equal(c.getAttribute("aria-selected"), "true");
      assert.equal(d.getAttribute("tabindex"), "-1");
      assert.equal(t.$("view-description").hidden, true);
      assert.equal(t.$("view-communications").hidden, false);
      assert.match(t.p.win.location.hash, /tab=communications/);
      t.key(c, "ArrowRight");
      is(t.active(), d);
      t.key(d, "End");
      is(t.active(), c);
      t.key(c, "Home");
      is(t.active(), d);
      t.key(d, "ArrowLeft");
      is(t.active(), c);
      t.key(c, "x");
      is(t.active(), c);
    });
  },

  async "the thread shows authors, times, kinds and question status"() {
    await page("#/plans/p24?panel=p24&tab=communications", {}, async (t) => {
      const list = t.$("messages");
      assert.equal(list.children.length, 3);
      const q = t.$("update-2");
      assert.ok(q.textContent.includes("operator") && q.textContent.includes("Question to operator") && q.textContent.includes("Approve the API?"));
      assert.equal(q.querySelector("time").getAttribute("datetime"), AT);
      assert.equal(q.querySelector(".question").textContent, "Open · acknowledged · 1 reply");
      assert.equal(t.$("update-3").textContent.includes("Acknowledged"), true);
      assert.equal(t.$("update-4").querySelector("a").getAttribute("href"), "#/plans/p24?panel=p24&tab=communications&update=2");
      assert.equal(t.$("thread-empty").hidden, true);
      assert.equal(t.$("more").hidden, true);
    });
  },

  async "message text is inert"() {
    const fx = { thread: (id) => ({ item: id, messages: [msg(2, "say", { body: "<img src=x onerror=alert(1)>\nline two" })], questions: [], next: 2, more: false }) };
    await page("#/plans/p24?panel=p24&tab=communications", fx, async (t) => {
      const body = t.$("update-2").querySelector(".message-body");
      assert.equal(body.textContent, "<img src=x onerror=alert(1)>\nline two");
      assert.equal(body.children.length, 0);
    });
  },

  async "an empty thread says so"() {
    const fx = { thread: (id) => ({ item: id, messages: [], questions: [], next: 0, more: false }) };
    await page("#/plans/p24?panel=p24&tab=communications", fx, async (t) => {
      assert.equal(t.$("thread-empty").hidden, false);
      assert.equal(t.$("messages").children.length, 0);
    });
  },

  async "a refresh adds new messages and updates questions without disturbing focus"() {
    const fx = {};
    await page("#/plans/p24?panel=p24&tab=communications", fx, async (t) => {
      const before = t.$("update-3");
      t.$("update-3").focus();
      fx.questions = [{ id: 2, state: "resolved", acknowledged: true, replies: 1, resolution: 5 }];
      fx.thread = (id, after) => ({ item: id, messages: [...BASE, msg(5, "resolved", { reply_to: 2, body: "Approved" })].filter((m) => m.id > after), questions: fx.questions, next: 5, more: false });
      await t.refresh();
      is(t.$("update-3"), before, "existing messages keep their nodes");
      is(t.active(), before, "focus stays on the message");
      assert.equal(t.$("messages").children.length, 4);
      assert.equal(t.$("update-2").querySelector(".question").textContent, "Resolved");
      assert.ok(t.$("update-5").textContent.includes("Decision"));
      assert.ok(fx.log.some((u) => u === "/api/items/p24/thread?after=4"), "the refresh asks after the cursor");
      assert.equal(t.$("panel").hidden, false);
    });
  },

  async "pages load on demand, move focus to the first new message and open at an update"() {
    const all = Array.from({ length: 120 }, (_, i) => msg(i + 1, "say"));
    const thread = (id, after, anchor) => {
      const rest = all.filter((m) => m.id > after);
      let n = 50;
      if (anchor > after) n = Math.max(50, anchor - after);
      return { item: id, messages: rest.slice(0, n), questions: [], next: rest.slice(0, n).pop().id, more: rest.length > n };
    };
    await page("#/plans/p24?panel=p24&tab=communications", { thread }, async (t) => {
      assert.equal(t.$("messages").children.length, 50);
      assert.equal(t.$("more").hidden, false);
      t.$("more").focus();
      t.$("more").click();
      await t.flush();
      assert.equal(t.$("messages").children.length, 100);
      is(t.active(), t.$("update-51"));
      await t.refresh();
      assert.equal(t.$("messages").children.length, 100, "a refresh does not load further pages");
      t.$("more").click();
      t.p.doc.fire("visibilitychange"); // a refresh lands while the page is loading
      await t.flush();
      assert.equal(t.$("more").hidden, true);
      assert.equal(t.$("messages").children.length, 120);
    });
    await page("#/plans/p24?panel=p24&tab=communications&update=75", { thread }, async (t) => {
      assert.equal(t.$("messages").children.length, 75);
      is(t.active(), t.$("update-75"));
      assert.equal(t.$("update-75").classList.contains("anchored"), true);
      t.p.win.navigate("#/plans/p24?panel=p24&tab=communications&update=110");
      await t.flush();
      is(t.active(), t.$("update-110"));
      assert.equal(t.$("update-75").classList.contains("anchored"), false);
    });
  },

  async "an update that is not a message is reported"() {
    await page("#/plans/p24?panel=p24&tab=communications&update=999", {}, async (t) => {
      assert.equal(t.text("thread-note"), "Update 999 is not a message in this thread.");
    });
  },

  async "a control server outage keeps the panel and its data and marks it stale"() {
    const fx = {};
    await page("#/plans/p24?panel=p24&tab=communications", fx, async (t) => {
      t.$("update-2").focus();
      fx.down = true;
      await t.refresh();
      assert.equal(t.$("panel-banner").hidden, false);
      assert.equal(t.$("messages").children.length, 3);
      assert.equal(t.$("view-description").children.length > 0, true);
      is(t.active(), t.$("update-2"));
      assert.match(t.text("status"), /^Stale since/);
      fx.down = false;
      await t.refresh();
      assert.equal(t.$("panel-banner").hidden, true);
      assert.match(t.text("status"), /^Last refresh/);
    });
  },

  async "a thread that cannot load says why"() {
    await page("#/plans/p24?panel=p24&tab=communications", { down: true }, async (t) => {
      assert.equal(t.$("messages").children.length, 0);
      assert.equal(t.text("thread-note"), "fetch failed");
    });
  },

  async "on a narrow screen the panel makes the rest of the page inert until it closes"() {
    await page("#/plans/p24", {}, async (t) => {
      t.opener("p24").click();
      await t.flush();
      assert.equal(t.$("bar").inert && t.$("list").inert && t.$("pane").inert, true);
      is(t.active(), t.$("panel-title"));
      t.key(t.$("panel-title"), "Escape");
      assert.equal(t.$("bar").inert || t.$("list").inert || t.$("pane").inert, false);
      assert.equal(t.active().dataset.key, "open-p24", "focus returns to the link that was inert while the panel was open");
    }, { narrow: true });
  },

  async "the browser's back button closes the panel"() {
    await page("#/plans/p24", {}, async (t) => {
      const link = t.opener("p24");
      link.focus();
      link.click();
      await t.flush();
      t.p.win.navigate("#/plans/p24");
      await t.flush();
      assert.equal(t.$("panel").hidden, true);
      assert.equal(t.active().dataset.key, "open-p24");
    });
  },

  async "switching plans keeps the panel open"() {
    await page("#/plans/p24?panel=p24", {}, async (t) => {
      const link = t.p.doc.querySelector('[data-key="plan-p24"]');
      assert.match(link.getAttribute("href"), /panel=p24/);
      assert.equal(t.$("panel").hidden, false);
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
