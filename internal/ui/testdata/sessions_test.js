"use strict";
// Drives the real dashboard scripts through dom_shim.js against fixture
// responses and checks the Sessions tab, its links into the item panel, and
// the feedback shown in the Plans task summary.
const assert = require("node:assert/strict");
const path = require("node:path");
const vm = require("node:vm");
const fs = require("node:fs");
const { boot } = require("./dom_shim.js");

const STATIC = path.join(__dirname, "..", "static");
const AT = "2026-11-07T09:00:00Z";
const OLD = "2026-11-06T09:00:00Z";
const ref = (item, update, extra = {}) => Object.assign({ item, update, kind: "ask", author: "t28", at: AT, summary: "Approve the API?" }, extra);
const row = (item, extra) => Object.assign({
  item, kind: item[0] === "p" ? "plan" : "task", role: item[0] === "p" ? "planner" : "executor", title: "Title " + item,
  repo: "r1", repo_slug: "dinoderek/engine", plan: "", state: "running", attempt: "a1", host: "devbox", harness: "claude",
  started: OLD, observed: "working", last_seen: AT,
}, extra);

// The server's order: the oldest request first, then item number.
const ROWS = [
  row("t29", { plan: "p24", host: "mac", observed: "exited", state: "in-review", feedback: ref("p24", 12, { author: "p24" }), latest: ref("t29", 77, { kind: "say", summary: "<script>alert(1)</script> done" }) }),
  row("t28", { plan: "p24", feedback: ref("t28", 40), latest: ref("t28", 41, { kind: "reply", author: "operator", summary: "Yes" }) }),
  row("t5", { plan: "p3", observed: "waiting", harness: "codex", host: "mac" }),
  row("t9", { plan: "p24", observed: "unknown", last_seen: undefined }),
  row("t11", { plan: "p24", observed: "limited", last_seen: undefined, latest: undefined }),
  row("p24", { observed: "waiting", plan: "p24", state: "executing" }),
];
const DATA = (rows = ROWS) => ({ data: { sessions: rows, feedback: rows.filter((r) => r.feedback).length }, fetched: AT });
const PLAN = {
  data: {
    plan: { id: "p24", repo: "r1", slug: "dinoderek/engine", title: "Control server rollout", state: "executing" },
    counts: { done: 0, in_progress: 2, ready: 0, blocked: 0 },
    tasks: [
      { id: "t28", title: "API", state: "running", group: "in-progress", host: "devbox", waiting: [], feedback: ref("t28", 40) },
      { id: "t29", title: "Review", state: "in-review", group: "in-progress", host: "mac", waiting: [], feedback: ref("p24", 12) },
      { id: "t31", title: "Docs", state: "todo", group: "in-progress", waiting: [] },
    ],
  },
  fetched: AT,
};
const PLANS = { data: { repos: [{ id: "r1", slug: "dinoderek/engine" }], states: ["executing"], total: 1, plans: [PLAN.data.plan] }, fetched: AT };
const msg = (id, kind, extra = {}) => Object.assign({ id, author: "operator", at: AT, kind, body: "m" + id }, extra);

async function page(hash, fx, body) {
  fx.log = [];
  fx.sessions = fx.sessions || DATA();
  const fetchImpl = async (url) => {
    const u = new URL(url, "http://127.0.0.1");
    fx.log.push(u.pathname + u.search);
    const reply = (status, data) => ({ ok: status < 300, status, statusText: "x", json: async () => data });
    if (fx.down) throw new TypeError("fetch failed");
    if (u.pathname === "/api/sessions") return fx.sessions.error && !fx.sessions.data ? reply(502, fx.sessions) : reply(200, fx.sessions);
    if (u.pathname === "/api/plans") return reply(200, PLANS);
    if (u.pathname === "/api/plans/p24") return reply(200, PLAN);
    let m;
    if ((m = u.pathname.match(/^\/api\/items\/([pt]\d+)\/thread$/))) {
      const messages = [msg(12, "ask", { requires_reply: true, body: "Approve?" }), msg(40, "ask", { requires_reply: true }), msg(41, "reply", { reply_to: 40 })];
      return reply(200, { data: { item: m[1], messages, questions: [{ id: 12, state: "open" }, { id: 40, state: "open", replies: 1 }], next: 41, more: false }, fetched: AT });
    }
    if ((m = u.pathname.match(/^\/api\/items\/([pt]\d+)$/))) {
      return reply(200, { data: { id: m[1], kind: m[1][0] === "p" ? "plan" : "task", repo: "r1", title: "Title " + m[1], state: "running", description_html: "" }, fetched: AT });
    }
    return reply(404, { error: "not found" });
  };
  const p = boot(STATIC, hash, fetchImpl);
  const $ = (id) => p.doc.getElementById(id);
  const t = {
    p, $, fx,
    flush: async () => { for (let i = 0; i < 8; i++) await new Promise((r) => setImmediate(r)); },
    active: () => p.doc.activeElement,
    key: (k) => p.doc.querySelector('[data-key="' + k + '"]'),
    rows: () => $("sessions").querySelectorAll("tr").filter((r) => r.id.startsWith("session-")),
    cell: (item, i) => $("session-" + item).children[i],
    refresh: async () => { p.doc.fire("visibilitychange"); await t.flush(); },
  };
  await t.flush();
  await body(t);
}

const scenarios = {
  async "the fmt helpers name observations and ages"() {
    const sandbox = { window: {}, document: {} };
    vm.createContext(sandbox);
    vm.runInContext(fs.readFileSync(path.join(STATIC, "sessions.js"), "utf8"), sandbox);
    const { observed, ago, feedbackLabel } = sandbox.window.engine.fmt;
    assert.deepEqual(["working", "waiting", "limited", "live", "exited", "unknown", "?", undefined, "constructor"].map(observed),
      ["Working", "Waiting", "Limited", "Live", "Exited", "Unknown", "Unknown", "Unknown", "Unknown"]);
    const now = Date.parse(AT);
    const at = (s) => new Date(now - s * 1000).toISOString();
    assert.deepEqual([at(5), at(120), at(3600 * 3), at(86400 * 3), at(-60), "", "garbage"].map((x) => ago(x, now)),
      ["just now", "2 min ago", "3 h ago", "3 d ago", "just now", "", ""]);
    assert.equal(feedbackLabel({ item: "t1" }, "t1"), "Needs feedback");
    assert.equal(feedbackLabel({ item: "p3" }, "t1"), "Needs feedback · plan p3");
    assert.equal(feedbackLabel({ item: "t2" }, "t1"), "Needs feedback · task t2");
  },

  async "rows show host, harness, observed state, last seen and the latest message"() {
    await page("#/sessions", {}, async (t) => {
      assert.equal(t.$("sessions").hidden, false);
      assert.equal(t.$("list").hidden, true);
      assert.equal(t.$("pane").hidden, true);
      assert.deepEqual(t.rows().map((r) => r.id), ["session-t29", "session-t28", "session-t5", "session-t9", "session-t11", "session-p24"]);
      assert.equal(t.$("tab-sessions").textContent, "Sessions · 6");
      assert.equal(t.$("tab-sessions").getAttribute("aria-current"), "page");
      assert.equal(t.$("tab-plans").getAttribute("aria-current"), null);
      assert.match(t.$("sessions").querySelector("h1").textContent, /Active sessions · 6 2 need feedback/);
      const c = t.cell("t28", 0);
      assert.equal(c.querySelector("a").textContent, "Title t28");
      assert.ok(c.textContent.includes("t28 · dinoderek/engine · Executor"));
      assert.ok(t.cell("p24", 0).textContent.includes("Planner"));
      assert.ok(t.cell("t28", 1).textContent.includes("Working") && t.cell("t28", 1).textContent.includes("Workflow: running"));
      assert.equal(t.cell("t28", 2).textContent, "devbox");
      assert.equal(t.cell("t5", 3).textContent, "codex");
      assert.equal(t.cell("t28", 4).querySelector("time").getAttribute("datetime"), AT);
      assert.match(t.cell("t28", 4).textContent, /ago$/);
      assert.ok(t.cell("t28", 5).textContent.startsWith("Yes"));
      assert.ok(t.cell("t28", 5).textContent.includes("operator"));
    });
  },

  async "waiting, unknown, limited and exited are shown apart from feedback"() {
    await page("#/sessions", {}, async (t) => {
      assert.ok(t.cell("t5", 1).textContent.startsWith("Waiting"));
      assert.equal(t.cell("t5", 1).querySelector(".feedback"), null, "an idle harness is not a request");
      assert.equal(t.$("session-t5").className, "");
      assert.ok(t.cell("t9", 1).textContent.startsWith("Unknown"));
      assert.ok(t.cell("t11", 1).textContent.startsWith("Limited"));
      assert.ok(t.cell("t29", 1).textContent.startsWith("Exited"));
      assert.ok(t.cell("t29", 1).querySelector(".feedback"), "a lost host can still have a question");
      assert.equal(t.cell("t9", 4).textContent, "—");
      assert.equal(t.cell("t11", 5).textContent, "No messages yet.");
      assert.equal(t.$("session-t29").className, "needs-feedback");
    });
  },

  async "feedback and latest links name the right item and sequence"() {
    await page("#/sessions", {}, async (t) => {
      assert.equal(t.key("s-fb-t28").getAttribute("href"), "#/sessions?panel=t28&tab=communications&update=40");
      assert.equal(t.key("s-fb-t28").textContent, "Needs feedback");
      assert.equal(t.key("s-fb-t29").getAttribute("href"), "#/sessions?panel=p24&tab=communications&update=12");
      assert.equal(t.key("s-fb-t29").textContent, "Needs feedback · plan p24");
      assert.equal(t.key("s-latest-t29").getAttribute("href"), "#/sessions?panel=t29&tab=communications&update=77");
      assert.equal(t.key("s-open-t29").getAttribute("href"), "#/sessions?panel=t29");
      assert.equal(t.key("s-plan-t29").getAttribute("href"), "#/plans/p24/t29");
      assert.equal(t.key("s-plan-t5").getAttribute("href"), "#/plans/p3/t5");
      assert.equal(t.key("s-plan-p24"), null);
    });
  },

  async "a message from the server stays text"() {
    await page("#/sessions", {}, async (t) => {
      assert.equal(t.cell("t29", 5).querySelector("script"), null);
      assert.ok(t.cell("t29", 5).textContent.includes("<script>alert(1)</script> done"));
    });
  },

  async "opening an inherited question keeps Sessions underneath and returns focus"() {
    await page("#/sessions", {}, async (t) => {
      const link = t.key("s-fb-t29");
      link.focus();
      link.click();
      await t.flush();
      assert.equal(t.$("panel").hidden, false);
      assert.equal(t.$("sessions").hidden, false);
      assert.equal(t.$("workspace").classList.contains("on-sessions"), true);
      assert.match(t.p.win.location.hash, /^#\/sessions\?panel=p24&tab=communications&update=12/);
      assert.equal(t.$("panel-title").textContent, "Title p24");
      assert.equal(t.$("update-12").classList.contains("anchored"), true);
      assert.ok(t.fx.log.some((u) => u === "/api/items/p24/thread?after=0&anchor=12"));
      assert.equal(t.key("s-fb-t29").getAttribute("aria-current"), "true");
      assert.equal(t.key("s-fb-t28").getAttribute("aria-current"), null);
      t.$("panel-close").click();
      assert.equal(t.$("panel").hidden, true);
      assert.equal(t.active().dataset.key, "s-fb-t29");
      assert.equal(t.p.win.location.hash, "#/sessions");
      assert.equal(t.key("s-fb-t29").getAttribute("aria-current"), null, "the closed panel leaves no selected message");
    });
  },

  async "a resolved question drops the feedback request on refresh"() {
    await page("#/sessions", {}, async (t) => {
      assert.equal(t.rows().length, 6);
      assert.ok(t.key("s-fb-t28"));
      t.fx.sessions = DATA(ROWS.map((r) => (r.item === "t28" ? Object.assign({}, r, { feedback: undefined }) : r)));
      await t.refresh();
      assert.equal(t.key("s-fb-t28"), null);
      assert.equal(t.$("session-t28").className, "");
      assert.match(t.$("sessions").querySelector("h1").textContent, /1 needs feedback/);
    });
  },

  async "the latest message opens the session's own item, not its plan"() {
    await page("#/sessions", {}, async (t) => {
      t.key("s-latest-t29").click();
      await t.flush();
      assert.equal(t.$("panel-title").textContent, "Title t29");
      assert.match(t.p.win.location.hash, /panel=t29&tab=communications&update=77/);
    });
  },

  async "a task's title opens its own panel, over Sessions"() {
    await page("#/sessions", {}, async (t) => {
      t.key("s-open-t5").click();
      await t.flush();
      assert.equal(t.$("panel").hidden, false);
      assert.equal(t.$("panel-title").textContent, "Title t5");
      assert.equal(t.$("sessions").hidden, false);
    });
  },

  async "a refresh keeps focus, the open panel and the selected message"() {
    await page("#/sessions?panel=t28&tab=communications&update=40", {}, async (t) => {
      assert.equal(t.$("update-40").classList.contains("anchored"), true);
      assert.equal(t.key("s-fb-t28").getAttribute("aria-current"), "true");
      t.key("s-open-t5").focus();
      const before = t.rows().length;
      await t.refresh();
      assert.equal(t.rows().length, before);
      assert.equal(t.active().dataset.key, "s-open-t5");
      assert.equal(t.$("panel").hidden, false);
      assert.equal(t.$("update-40").classList.contains("anchored"), true);
      assert.equal(t.key("s-fb-t28").getAttribute("aria-current"), "true");
      assert.equal(t.p.win.location.hash, "#/sessions?panel=t28&tab=communications&update=40");
    });
  },

  async "a refresh shows new data in place"() {
    await page("#/sessions", {}, async (t) => {
      t.fx.sessions = DATA(ROWS.slice(2, 3));
      await t.refresh();
      assert.deepEqual(t.rows().map((r) => r.id), ["session-t5"]);
      assert.equal(t.$("tab-sessions").textContent, "Sessions · 1");
      assert.match(t.$("sessions").querySelector("h1").textContent, /0 need feedback/);
    });
  },

  async "loading, empty and first-load failure each say so"() {
    await page("#/sessions", { sessions: DATA([]) }, async (t) => {
      assert.ok(t.$("sessions").textContent.includes("No active sessions."));
      assert.equal(t.$("tab-sessions").textContent, "Sessions · 0");
    });
    await page("#/sessions", { down: true }, async (t) => {
      assert.ok(t.$("sessions").textContent.includes("fetch failed"));
      assert.equal(t.$("tab-sessions").textContent, "Sessions");
    });
    const slow = boot(STATIC, "#/sessions", () => new Promise(() => {}));
    assert.ok(slow.doc.getElementById("sessions").textContent.includes("Loading sessions…"));
  },

  async "an outage keeps the last data, marked stale, and recovery clears it"() {
    await page("#/sessions", {}, async (t) => {
      t.fx.down = true;
      await t.refresh();
      assert.equal(t.rows().length, 6);
      assert.ok(t.$("sessions").textContent.includes("Control server unavailable"));
      assert.match(t.$("status").textContent, /^Stale since .*fetch failed/);
      assert.equal(t.$("status").className, "status stale");
      t.fx.sessions = Object.assign({}, DATA(), { stale: true, error: "control server unreachable" });
      t.fx.down = false;
      await t.refresh();
      assert.match(t.$("status").textContent, /control server unreachable/);
      t.fx.sessions = DATA();
      await t.refresh();
      assert.equal(t.$("sessions").textContent.includes("unavailable"), false);
      assert.match(t.$("status").textContent, /^Last refresh/);
    });
  },

  async "switching tabs keeps the Plans filters and selection"() {
    await page("#/plans/p24/t28?repo=r1&q=roll", {}, async (t) => {
      assert.equal(t.$("tab-sessions").getAttribute("href"), "#/sessions");
      assert.equal(t.$("tab-plans").getAttribute("href"), "#/plans/p24/t28?repo=r1&q=roll");
      t.$("tab-sessions").click();
      await t.flush();
      assert.equal(t.$("sessions").hidden, false);
      assert.equal(t.$("search").value, "roll");
      assert.equal(t.$("tab-plans").getAttribute("href"), "#/plans/p24/t28?repo=r1&q=roll");
      t.$("tab-plans").click();
      await t.flush();
      assert.equal(t.$("sessions").hidden, true);
      assert.equal(t.$("pane").hidden, false);
      assert.equal(t.$("search").value, "roll");
      assert.ok(t.$("task-t28").classList.contains("selected"));
    });
  },

  async "the plan task summary links the same feedback"() {
    await page("#/plans/p24", {}, async (t) => {
      assert.equal(t.key("fb-t28").getAttribute("href"), "#/plans/p24?panel=t28&tab=communications&update=40");
      assert.equal(t.key("fb-t28").textContent, "Needs feedback");
      assert.equal(t.key("fb-t29").getAttribute("href"), "#/plans/p24?panel=p24&tab=communications&update=12");
      assert.equal(t.key("fb-t29").textContent, "Needs feedback · plan p24");
      assert.equal(t.key("fb-t31"), null);
      t.key("fb-t29").focus();
      t.key("fb-t29").click();
      await t.flush();
      assert.equal(t.$("panel-title").textContent, "Title p24");
      assert.equal(t.$("pane").hidden, false, "the plan stays beneath the panel");
      assert.equal(t.$("update-12").classList.contains("anchored"), true);
      t.$("panel-close").click();
      assert.equal(t.active().dataset.key, "fb-t29");
    });
  },

  async "a panel opened from the address on Sessions names its repository"() {
    await page("#/sessions?panel=t5", {}, async (t) => assert.equal(t.$("panel-ref").textContent, "t5 · dinoderek/engine"));
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
