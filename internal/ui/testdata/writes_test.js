"use strict";
// Runs the page's write helper against a scripted fetch: one key per form,
// reused on a resend after no answer or a server error, renewed after a
// definite 2xx or 4xx.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

function boot(replies) {
  const sent = [];
  let n = 0;
  const sandbox = {
    window: {},
    crypto: { randomUUID: () => "key-" + ++n },
    fetch: async (url, init) => {
      sent.push({ url, init, body: JSON.parse(init.body) });
      const r = replies.shift();
      if (r instanceof Error) throw r;
      return { ok: r.status < 300, status: r.status, statusText: "text", json: async () => { if (r.raw) throw new Error("no json"); return r.body; } };
    },
  };
  vm.createContext(sandbox);
  vm.runInContext(fs.readFileSync(path.join(__dirname, "..", "static", "writes.js"), "utf8"), sandbox);
  return { writes: sandbox.window.engine.writes, sent };
}

async function scenario(name, fn) {
  try {
    await fn();
    console.log("ok   " + name);
  } catch (e) {
    console.log("FAIL " + name + ": " + e.message);
    process.exitCode = 1;
  }
}

(async () => {
  await scenario("a send posts JSON with the form's key", async () => {
    const { writes, sent } = boot([{ status: 200, body: { data: { version: 4 } } }]);
    const r = await writes.form().send("/api/tasks/t5/stop", { version: 3 });
    assert.deepEqual([r.ok, r.status, r.data.version, r.error, r.reason], [true, 200, 4, undefined, undefined]);
    assert.equal(sent[0].url, "/api/tasks/t5/stop");
    assert.equal(sent[0].init.method, "POST");
    assert.equal(sent[0].init.headers["Content-Type"], "application/json");
    assert.deepEqual(sent[0].body, { key: "key-1", version: 3 });
  });
  await scenario("no answer and a server error resend the same key", async () => {
    const { writes, sent } = boot([new Error("offline"), { status: 502, body: { error: "control server unreachable" } }, { status: 200, body: { data: {} } }]);
    const f = writes.form();
    const lost = await f.send("/api/updates/4/ack", {});
    assert.equal(lost.status, 0);
    assert.equal(lost.ok, false);
    const bad = await f.send("/api/updates/4/ack", {});
    assert.equal(bad.status, 502);
    assert.equal(bad.error, "control server unreachable");
    assert.equal((await f.send("/api/updates/4/ack", {})).ok, true);
    assert.deepEqual(sent.map((s) => s.body.key), ["key-1", "key-1", "key-1"]);
  });
  await scenario("a definite answer renews the key, and a refusal passes its reason on", async () => {
    const { writes, sent } = boot([{ status: 409, body: { error: "version is 9", reason: "stale" } }, { status: 200, body: { data: {} } }]);
    const f = writes.form();
    const r = await f.send("/api/tasks/t5/retry", { version: 8 });
    assert.deepEqual([r.ok, r.status, r.error, r.reason], [false, 409, "version is 9", "stale"]);
    await f.send("/api/tasks/t5/retry", { version: 9 });
    assert.deepEqual(sent.map((s) => s.body.key), ["key-1", "key-2"]);
  });
  await scenario("an unreadable reply is an error, not a throw", async () => {
    const { writes } = boot([{ status: 200, raw: true }]);
    const r = await writes.form().send("/api/tasks/t5/stop", {});
    assert.equal(r.status, 200);
    assert.equal(r.error, "text");
  });
  await scenario("each form has its own key", async () => {
    const { writes, sent } = boot([{ status: 500, body: {} }, { status: 500, body: {} }]);
    await writes.form().send("/a", {});
    await writes.form().send("/a", {});
    assert.deepEqual(sent.map((s) => s.body.key), ["key-1", "key-2"]);
  });
})();
