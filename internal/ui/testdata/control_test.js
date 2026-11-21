"use strict";
// Drives the real dashboard scripts through dom_shim.js and checks plan and
// task control: which buttons an item's actions become, that a confirmation
// sends nothing until it is confirmed, what each write sends, and what the page
// says when the server refuses or does not answer.
const assert = require("node:assert/strict");
const path = require("node:path");
const { boot, Event } = require("./dom_shim.js");

const STATIC = path.join(__dirname, "..", "static");
const AT = "2026-11-07T09:00:00Z";
const task = (id, state, actions, extra = {}) => Object.assign({ id, title: "Task " + id, state, group: "in-progress", waiting: [], version: 3, actions }, extra);
const planOf = (state, version, actions, tasks) => ({
  data: {
    plan: { id: "p24", repo: "r1", slug: "dinoderek/engine", title: "Control server rollout", state }, version, actions,
    counts: { done: 0, in_progress: 1, ready: 0, blocked: 0 }, tasks,
  },
  fetched: AT,
});
const itemOf = (id, kind, state, version, actions, extra = {}) => ({ data: Object.assign({ id, kind, repo: "r1", title: kind === "plan" ? "Control server rollout" : "Task " + id, state, version, actions, description_html: "" }, extra), fetched: AT });

// page runs a scenario against fixtures it can change as it goes. fx.plan and
// fx.items are what the server reads return; fx.answer decides each write.
async function page(hash, fx, body) {
  fx.posts = [];
  fx.items = fx.items || {};
  const reply = (status, data) => ({ ok: status < 300, status, statusText: "x", json: async () => data });
  const fetchImpl = async (url, init) => {
    const u = new URL(url, "http://127.0.0.1");
    if (init && init.method === "POST") {
      const posted = { path: u.pathname, body: JSON.parse(init.body) };
      fx.posts.push(posted);
      const r = await fx.answer(posted);
      if (r instanceof Error) throw r;
      return reply(r.status, r.body);
    }
    let m;
    if (u.pathname === "/api/plans") return reply(200, { data: { repos: [], states: [], total: 0, plans: [] }, fetched: AT });
    if (u.pathname === "/api/plans/p24") return reply(200, fx.plan);
    if (u.pathname === "/api/inbox") return reply(200, { data: { cards: [], questions: 0, messages: 0 }, fetched: AT });
    if (u.pathname === "/api/sessions") return reply(200, { data: { sessions: [], feedback: 0 }, fetched: AT });
    if ((m = u.pathname.match(/^\/api\/items\/([pt]\d+)\/thread$/))) return reply(200, { data: { item: m[1], messages: [], questions: [], next: 0, more: false }, fetched: AT });
    if ((m = u.pathname.match(/^\/api\/items\/([pt]\d+)$/))) return fx.items[m[1]] ? reply(200, fx.items[m[1]]) : reply(404, { error: "unknown item" });
    return reply(404, { error: "not found" });
  };
  const p = boot(STATIC, hash, fetchImpl);
  const $ = (id) => p.doc.getElementById(id);
  const t = {
    p, $, fx,
    flush: async () => { for (let i = 0; i < 12; i++) await new Promise((r) => setImmediate(r)); },
    act: (scope, id, op) => p.doc.querySelector('[data-key="act-' + scope + "-" + id + "-" + op + '"]'),
    click: async (el) => { el.click(); await t.flush(); },
    key: (el, key) => el.dispatchEvent(new Event("keydown", { bubbles: true, key })),
    sheetOpen: () => !$("sheet-back").hidden,
    sheetText: () => $("sheet").textContent,
    refresh: async () => { p.doc.fire("visibilitychange"); await t.flush(); },
  };
  await t.flush();
  await body(t);
}

const ok = () => ({ status: 200, body: { data: {} } });
const texts = (nodes) => nodes.map((n) => n.textContent.trim());
const names = (t, scope, id) => t.p.doc.documentElement.querySelectorAll("button").filter((b) => (b.dataset.key || "").startsWith("act-" + scope + "-" + id + "-")).map((b) => b.textContent);

const scenarios = {
  async "the plan pane and the panel offer the actions the server lists, and no others"() {
    const fx = {
      plan: planOf("prepare", 4, ["approve"], [
        task("t30", "running", ["task-stop"]), task("t31", "todo", ["resume", "complete-external"], { held: true }),
        task("t32", "failed", ["retry", "complete-external"]), task("t33", "done", []),
      ]),
      items: { t30: itemOf("t30", "task", "running", 3, ["task-stop"]), p24: itemOf("p24", "plan", "prepare", 4, ["approve"]) },
      answer: ok,
    };
    await page("#/plans/p24", fx, async (t) => {
      assert.deepEqual(names(t, "pane", "p24"), ["Approve"]);
      assert.deepEqual(names(t, "pane", "t30"), ["Stop"]);
      assert.deepEqual(names(t, "pane", "t31"), ["Resume", "Complete externally"]);
      assert.deepEqual(names(t, "pane", "t32"), ["Retry", "Complete externally"]);
      assert.deepEqual(names(t, "pane", "t33"), []);
      assert.equal(t.$("task-t33").querySelector('[data-label="Actions"]').textContent, "—");
      assert.equal(t.$("task-t31").querySelector('[data-label="State"]').textContent.includes("held"), true);
      assert.equal(t.fx.posts.length, 0);
      t.p.win.navigate("#/plans/p24?panel=t30");
      await t.flush();
      assert.deepEqual(names(t, "panel", "t30"), ["Stop"]);
      assert.equal(t.$("panel-state").textContent, "Running");
      t.p.win.navigate("#/plans/p24?panel=p24");
      await t.flush();
      assert.deepEqual(names(t, "panel", "p24"), ["Approve"]);
    });
  },

  async "a stop asks first, naming the item, its state and the effect, and sends nothing until confirmed"() {
    const fx = { plan: planOf("executing", 5, ["stop"], [task("t30", "running", ["task-stop"])]), answer: ok };
    await page("#/plans/p24", fx, async (t) => {
      const opener = t.act("pane", "t30", "task-stop");
      opener.focus();
      await t.click(opener);
      assert.equal(t.sheetOpen(), true);
      const s = t.sheetText();
      assert.ok(s.includes("Stop t30?") && s.includes("t30") && s.includes("Task t30") && s.includes("State: Running") && s.includes("held until you resume it"), s);
      assert.equal(t.fx.posts.length, 0, "nothing is sent by opening the sheet");
      assert.equal(t.p.doc.activeElement, t.$("sheet-cancel"));
      await t.click(t.$("sheet-cancel"));
      assert.equal(t.sheetOpen(), false);
      assert.equal(t.fx.posts.length, 0, "cancelling sends nothing");
      assert.equal(t.p.doc.activeElement.dataset.key, "act-pane-t30-task-stop", "focus returns to the button");

      await t.click(t.act("pane", "t30", "task-stop"));
      t.key(t.$("sheet-back"), "Escape");
      assert.equal(t.sheetOpen(), false);
      assert.equal(t.fx.posts.length, 0, "Escape sends nothing");

      await t.click(t.act("pane", "t30", "task-stop"));
      fx.plan = planOf("executing", 5, ["stop"], [task("t30", "todo", ["resume"], { held: true })]);
      await t.click(t.$("sheet-ok"));
      assert.equal(fx.posts.length, 1);
      assert.equal(fx.posts[0].path, "/api/tasks/t30/stop");
      assert.deepEqual(Object.keys(fx.posts[0].body), ["key"]);
      assert.equal(t.sheetOpen(), false);
      assert.deepEqual(names(t, "pane", "t30"), ["Resume"]);
      assert.ok(t.$("task-t30").textContent.includes("Stopped; the task is held."));
      assert.equal(t.p.doc.activeElement.dataset.key, "acts-pane-t30", "the button is gone, so focus goes to the item's buttons");
    });
  },

  async "a plan stop and approve confirm, and approve sends the version the page read"() {
    const fx = { plan: planOf("prepare", 7, ["approve"], []), answer: ok };
    await page("#/plans/p24", fx, async (t) => {
      await t.click(t.act("pane", "p24", "approve"));
      assert.ok(t.sheetText().includes("Approve p24?") && t.sheetText().includes("State: Prepare") && t.sheetText().includes("Nothing starts until you run it"));
      assert.equal(fx.posts.length, 0);
      // Someone else edits the plan while the sheet is open; the page's version still goes.
      fx.plan = planOf("prepare", 8, ["approve"], []);
      await t.click(t.$("sheet-ok"));
      assert.deepEqual(fx.posts, [{ path: "/api/plans/p24/approve", body: { key: fx.posts[0].body.key, version: 7 } }]);
    });
    const running = { plan: planOf("executing", 9, ["stop"], []), answer: ok };
    await page("#/plans/p24", running, async (t) => {
      await t.click(t.act("pane", "p24", "stop"));
      assert.ok(t.sheetText().includes("Stop p24?") && t.sheetText().includes("State: Executing") && t.sheetText().includes("carry on"));
      assert.equal(running.posts.length, 0);
      await t.click(t.$("sheet-ok"));
      assert.equal(running.posts[0].path, "/api/plans/p24/stop");
      assert.deepEqual(Object.keys(running.posts[0].body), ["key"]);
    });
  },

  async "run and resume go straight through, with no sheet"() {
    const fx = { plan: planOf("planned", 4, ["run"], [task("t31", "todo", ["resume"], { held: true })]), answer: ok };
    await page("#/plans/p24", fx, async (t) => {
      await t.click(t.act("pane", "p24", "run"));
      assert.equal(t.sheetOpen(), false);
      assert.equal(fx.posts[0].path, "/api/plans/p24/run");
      assert.deepEqual(Object.keys(fx.posts[0].body), ["key"]);
      await t.click(t.act("pane", "t31", "resume"));
      assert.equal(t.sheetOpen(), false);
      assert.equal(fx.posts[1].path, "/api/tasks/t31/resume");
      assert.ok(t.$("pane").textContent.includes("Coordination started."));
    });
  },

  async "focus stays with the item after run, and the URL is editable again after a refusal that follows a lost reply"() {
    const fx = { plan: planOf("planned", 4, ["run"], [task("t32", "failed", ["complete-external"], { version: 11 })]), answer: ok };
    await page("#/plans/p24", fx, async (t) => {
      const run = t.act("pane", "p24", "run");
      run.focus();
      fx.plan = planOf("executing", 5, ["stop"], [task("t32", "failed", ["complete-external"], { version: 11 })]);
      await t.click(run);
      assert.equal(t.p.doc.activeElement.dataset.key, "acts-pane-p24", "the Run button is gone, so focus goes to the plan's buttons");
    });
    let calls = 0;
    const lossy = {
      plan: planOf("blocked", 4, ["stop"], [task("t32", "failed", ["complete-external"], { version: 11 })]),
      answer: () => (++calls === 1 ? new TypeError("fetch failed") : { status: 400, body: { error: "a GitHub PR URL for this repository is required" } }),
    };
    await page("#/plans/p24", lossy, async (t) => {
      await t.click(t.act("pane", "t32", "complete-external"));
      t.$("sheet-pr").value = "nonsense";
      await t.click(t.$("sheet-ok"));
      assert.equal(t.$("sheet-pr").hasAttribute("readonly"), true, "locked while a resend must match");
      await t.click(t.$("sheet-ok"));
      assert.equal(t.$("sheet-pr").hasAttribute("readonly"), false, "a refusal frees the text");
    });
  },

  async "a lost Run reply is resent with its key, but not once the item has changed"() {
    let calls = 0;
    const fx = {
      plan: planOf("planned", 4, ["run"], []),
      answer: () => (++calls === 1 ? new TypeError("fetch failed") : ok()),
    };
    await page("#/plans/p24", fx, async (t) => {
      await t.click(t.act("pane", "p24", "run"));
      assert.ok(t.$("pane").textContent.includes("the dashboard did not answer"));
      await t.click(t.act("pane", "p24", "run"));
      assert.equal(fx.posts[1].body.key, fx.posts[0].body.key, "the same item, the same key");
      // The run landed, was stopped, and is offered again at a newer version.
      fx.plan = planOf("planned", 6, ["run"], []);
      await t.refresh();
      await t.click(t.act("pane", "p24", "run"));
      assert.equal(fx.posts.length, 3);
      assert.notEqual(fx.posts[2].body.key, fx.posts[0].body.key, "an old key would only replay the old result");
    });
  },

  async "retry confirms and sends the version"() {
    const fx = { plan: planOf("blocked", 4, ["stop"], [task("t32", "failed", ["retry", "complete-external"], { version: 11 })]), answer: ok };
    await page("#/plans/p24", fx, async (t) => {
      await t.click(t.act("pane", "t32", "retry"));
      assert.ok(t.sheetText().includes("Retry t32?") && t.sheetText().includes("State: Failed") && t.sheetText().includes("back to todo"));
      assert.equal(fx.posts.length, 0);
      await t.click(t.$("sheet-ok"));
      assert.deepEqual(fx.posts, [{ path: "/api/tasks/t32/retry", body: { key: fx.posts[0].body.key, version: 11 } }]);
    });
  },

  async "complete-external asks for the pull request, sends it with the version, and keeps the sheet on a refusal"() {
    const url = `https://github.com/dinoderek/engine/pull/${9}`;
    const fx = {
      plan: planOf("blocked", 4, ["stop"], [task("t32", "failed", ["retry", "complete-external"], { version: 11 })]),
      answer: (post) => (post.body.pr === "nonsense" ? { status: 400, body: { error: "a GitHub PR URL for this repository is required" } } : ok()),
    };
    await page("#/plans/p24", fx, async (t) => {
      await t.click(t.act("pane", "t32", "complete-external"));
      assert.ok(t.sheetText().includes("Complete externally t32?") && t.sheetText().includes("Nothing checks that it merged"));
      assert.equal(t.p.doc.activeElement, t.$("sheet-pr"));
      await t.click(t.$("sheet-ok"));
      assert.equal(fx.posts.length, 0, "no URL, no request");
      assert.ok(t.sheetText().includes("Enter the pull request URL."));
      t.$("sheet-pr").value = "nonsense";
      await t.click(t.$("sheet-ok"));
      assert.equal(fx.posts.length, 1);
      assert.equal(t.sheetOpen(), true, "a 400 keeps the sheet and the text");
      assert.equal(t.$("sheet-pr").value, "nonsense");
      assert.ok(t.sheetText().includes("a GitHub PR URL for this repository is required"));
      t.$("sheet-pr").value = "  " + url + " ";
      await t.click(t.$("sheet-ok"));
      assert.deepEqual(fx.posts[1], { path: "/api/tasks/t32/complete-external", body: { key: fx.posts[1].body.key, version: 11, pr: url } });
      assert.notEqual(fx.posts[1].body.key, fx.posts[0].body.key, "a definite answer made a new key");
      assert.equal(t.sheetOpen(), false);
    });
  },

  async "a 409 closes the sheet, refreshes the view and says what changed"() {
    const fx = {
      plan: planOf("executing", 5, ["stop"], [task("t30", "running", ["task-stop"])]),
      answer: () => ({ status: 409, body: { error: "task t30 has no running execution to stop" } }),
    };
    await page("#/plans/p24", fx, async (t) => {
      await t.click(t.act("pane", "t30", "task-stop"));
      // The task failed since the page loaded; the refresh after the 409 shows it.
      fx.plan = planOf("blocked", 5, ["stop"], [task("t30", "failed", ["retry", "complete-external"], { version: 4 })]);
      await t.click(t.$("sheet-ok"));
      assert.equal(t.sheetOpen(), false);
      const row = t.$("task-t30").textContent;
      assert.ok(row.includes("Not applied: task t30 has no running execution to stop"), row);
      assert.ok(row.includes("t30 is now Failed; the page showed Running."), row);
      assert.deepEqual(names(t, "pane", "t30"), ["Retry", "Complete externally"], "the view shows what is allowed now");
    });
    const stale = {
      plan: planOf("prepare", 4, ["approve"], []),
      answer: () => ({ status: 409, body: { error: "version is 6" } }),
    };
    await page("#/plans/p24", stale, async (t) => {
      await t.click(t.act("pane", "p24", "approve"));
      stale.plan = planOf("prepare", 6, ["approve"], []);
      await t.click(t.$("sheet-ok"));
      assert.ok(t.$("pane").textContent.includes("p24 was changed by someone else (version 4 to 6)."), t.$("pane").textContent);
    });
  },

  async "no answer keeps the sheet and resends the same key"() {
    let calls = 0;
    const fx = {
      plan: planOf("executing", 5, ["stop"], []),
      answer: () => (++calls === 1 ? new TypeError("fetch failed") : ok()),
    };
    await page("#/plans/p24", fx, async (t) => {
      await t.click(t.act("pane", "p24", "stop"));
      await t.click(t.$("sheet-ok"));
      assert.equal(t.sheetOpen(), true);
      assert.ok(t.sheetText().includes("the dashboard did not answer"));
      assert.equal(t.p.doc.activeElement, t.$("sheet-ok"), "focus stays in the sheet after a failure");
      await t.click(t.$("sheet-ok"));
      assert.equal(fx.posts.length, 2);
      assert.equal(fx.posts[0].body.key, fx.posts[1].body.key);
      assert.equal(t.sheetOpen(), false);
    });
  },

  async "Cancel and Escape wait for a confirmation in flight, so its outcome is shown"() {
    let release;
    const gate = new Promise((r) => { release = r; });
    const fx = { plan: planOf("executing", 5, ["stop"], []), answer: async () => { await gate; return ok(); } };
    await page("#/plans/p24", fx, async (t) => {
      await t.click(t.act("pane", "p24", "stop"));
      t.$("sheet-ok").click();
      await t.flush();
      assert.equal(t.$("sheet-cancel").hasAttribute("disabled"), true);
      t.key(t.$("sheet-back"), "Escape");
      t.$("sheet-back").click();
      assert.equal(t.sheetOpen(), true, "the sheet stays while the write is out");
      release();
      await t.flush();
      assert.equal(t.sheetOpen(), false);
      assert.ok(t.$("pane").textContent.includes("Coordination stopped."));
    });
  },

  async "the item panel acts too, and Tab stays inside the sheet"() {
    const fx = {
      plan: planOf("executing", 5, ["stop"], [task("t30", "running", ["task-stop"])]),
      items: { t30: itemOf("t30", "task", "running", 3, ["task-stop"]) },
      answer: ok,
    };
    await page("#/plans/p24?panel=t30", fx, async (t) => {
      const b = t.act("panel", "t30", "task-stop");
      assert.ok(b);
      await t.click(b);
      assert.equal(t.sheetOpen(), true);
      assert.equal(t.p.doc.activeElement, t.$("sheet-cancel"));
      t.key(t.$("sheet-back"), "Tab");
      assert.equal(t.p.doc.activeElement, t.$("sheet-ok"));
      t.key(t.$("sheet-back"), "Tab");
      assert.equal(t.p.doc.activeElement, t.$("sheet-cancel"), "Tab wraps");
      await t.click(t.$("sheet-ok"));
      assert.equal(fx.posts[0].path, "/api/tasks/t30/stop");
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
