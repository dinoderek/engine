"use strict";
// Plan and task control: the buttons for the writes an item's state allows
// (the server lists them in `actions`), the confirmation sheet that names the
// item, its state and the effect before a destructive one is sent, and what
// the page says when the server refuses. The server still decides every
// write; this file only offers and sends them. Values from the server are
// inserted as text.
window.engine = window.engine || {};
window.engine.control = function (ctx) {
  const { el, label } = window.engine.dom;
  const $ = ctx.$;

  // Every action: its button text, its route, whether it sends the version the
  // page read, whether it asks first, and what it does, in a sentence.
  const OPS = {
    approve: { text: "Approve", path: (id) => "/api/plans/" + id + "/approve", versioned: true, confirm: true, done: "Approved.",
      effect: "Marks this version of the plan approved. Nothing starts until you run it, and an edit to the plan withdraws the approval." },
    run: { text: "Run", path: (id) => "/api/plans/" + id + "/run", done: "Coordination started." },
    stop: { text: "Stop", path: (id) => "/api/plans/" + id + "/stop", confirm: true, danger: true, done: "Coordination stopped.",
      effect: "Stops dispatching this plan's tasks. Sessions already running carry on, and no task fails." },
    "task-stop": { text: "Stop", path: (id) => "/api/tasks/" + id + "/stop", confirm: true, danger: true, done: "Stopped; the task is held.",
      effect: "Ends the running session without failing the task. Its window closes, its branch and worktree stay, and the task is held until you resume it." },
    resume: { text: "Resume", path: (id) => "/api/tasks/" + id + "/resume", done: "Resumed; the task is dispatched again." },
    retry: { text: "Retry", path: (id) => "/api/tasks/" + id + "/retry", versioned: true, confirm: true, done: "Moved back to todo.",
      effect: "Moves the failed task back to todo, so the coordinator dispatches it again." },
    "complete-external": { text: "Complete externally", path: (id) => "/api/tasks/" + id + "/complete-external", versioned: true, confirm: true, pr: true, done: "Marked done.",
      effect: "Marks the task done with the pull request below, for work finished outside engine. Nothing checks that it merged, and tasks waiting on it may start." },
  };

  const forms = new Map(); // item and action -> the form holding its request key
  const notes = new Map(); // item -> the last result, kept across redraws
  let sheet = null; // the open confirmation: what it is for and its nodes

  // A form is kept per item, action and version the page read: a lost reply
  // is resent with its key, but once the item has changed, a retained key
  // would only replay an old result, so the next try makes a new one.
  const slot = (f, op) => f.id + " " + op + " " + f.version;
  const formFor = (f, op) => {
    const k = slot(f, op);
    if (!forms.has(k)) forms.set(k, window.engine.writes.form());
    return forms.get(k);
  };

  // The facts every offer is made from, as each view holds them.
  function fromPlan(d) {
    return { id: d.plan.id, kind: "plan", title: d.plan.title, state: label(d.plan.state), version: d.version, actions: d.actions || [] };
  }

  function fromTask(row) {
    return { id: row.id, kind: "task", title: row.title, state: label(row.state) + (row.held ? " (held)" : ""), version: row.version, actions: row.actions || [] };
  }

  function fromItem(d) {
    return { id: d.id, kind: d.kind, title: d.title, state: label(d.state) + (d.held ? " (held)" : ""), version: d.version, actions: d.actions || [] };
  }

  // changed says what moved under a refused write, from the facts the page
  // acted on and the ones it has now.
  function changed(before, now) {
    if (!now) return "";
    if (now.state !== before.state) return before.id + " is now " + now.state + "; the page showed " + before.state + ".";
    if (now.version !== before.version) return before.id + " was changed by someone else (version " + before.version + " to " + now.version + ").";
    return "";
  }

  // note keeps an item's last result. A refusal also keeps the facts the
  // write was made on, so the bar can say what has changed since.
  function note(id, text, bad, before) {
    notes.set(id, { text, bad, before });
  }

  // bar is an item's buttons and its last result. scope keeps the keys of
  // the same item's bars in two places apart, so focus returns to the right one.
  function bar(f, scope) {
    const n = notes.get(f.id);
    if (!f.actions.length && !n) return "";
    const row = el("div", { class: "actions", role: "group", tabindex: "-1", "data-key": "acts-" + scope + "-" + f.id, "aria-label": "Actions for " + f.id });
    for (const op of f.actions) {
      const spec = OPS[op];
      if (!spec) continue;
      const key = "act-" + scope + "-" + f.id + "-" + op;
      const b = el("button", { type: "button", class: "act" + (spec.danger ? " danger" : ""), "data-key": key }, spec.text);
      b.addEventListener("click", () => (spec.confirm ? open(f, op, key, scope) : direct(f, op, b, scope)));
      row.append(b);
    }
    if (n) {
      const what = n.before ? changed(n.before, f) : "";
      row.append(el("p", { class: "action-note" + (n.bad ? " bad" : ""), role: "status" }, (n.text + " " + what).trim()));
    }
    return row;
  }

  // settle records a write's outcome. A definite answer ends the key; a 409
  // refreshes the view and says what changed. It reports whether it applied.
  async function settle(f, op, r) {
    const spec = OPS[op];
    if (r.status === 0 || r.status >= 500) return { done: false, retry: true, text: (r.error || "No answer.") + " Try again; the same request is resent." };
    forms.delete(slot(f, op));
    if (r.ok) {
      note(f.id, spec.done, false);
      await ctx.refresh();
      return { done: true };
    }
    if (r.status === 409) {
      note(f.id, "Not applied: " + (r.error || "the server refused it."), true, f);
      await ctx.refresh();
      ctx.render();
      return { done: true, conflict: true };
    }
    return { done: false, text: r.error || "The dashboard refused the write." };
  }

  async function direct(f, op, button, scope) {
    const was = button.dataset.key;
    button.setAttribute("disabled", "");
    const r = await formFor(f, op).send(OPS[op].path(f.id), {});
    const out = await settle(f, op, r);
    if (!out.done) {
      note(f.id, out.text, true);
      ctx.render();
    }
    button.removeAttribute("disabled");
    if (document.activeElement === document.body) refocus([was], scope, f.id);
  }

  function fieldsFor(f, op, input) {
    const fields = {};
    if (OPS[op].versioned) fields.version = f.version;
    if (OPS[op].pr) fields.pr = input.value.trim();
    return fields;
  }

  function say(text, bad) {
    sheet.result.textContent = text;
    sheet.result.className = bad ? "sheet-result bad" : "sheet-result";
  }

  async function confirm() {
    const s = sheet;
    if (!s || s.busy) return;
    if (OPS[s.op].pr && !s.input.value.trim()) {
      say("Enter the pull request URL.", true);
      s.input.focus();
      return;
    }
    s.busy = true;
    s.ok.setAttribute("disabled", "");
    s.cancel.setAttribute("disabled", "");
    // The text is fixed while the write is out: a resend must match it.
    if (s.input) s.input.setAttribute("readonly", "");
    const r = await formFor(s.f, s.op).send(OPS[s.op].path(s.f.id), fieldsFor(s.f, s.op, s.input));
    const out = await settle(s.f, s.op, r);
    s.busy = false;
    s.ok.removeAttribute("disabled");
    s.cancel.removeAttribute("disabled");
    if (out.done) {
      close(true);
      return;
    }
    // A lost reply keeps the text fixed for the resend; a refusal frees it.
    if (s.input && !out.retry) s.input.removeAttribute("readonly");
    say(out.text, true);
    // Disabling the button dropped focus; put it back inside the sheet.
    (s.input && !out.retry ? s.input : s.ok).focus();
  }

  // refocus puts focus on the first of the keys that exists, else on the
  // item's buttons, else on its link: an action's button is often gone once
  // it is done, because the item no longer offers it.
  function refocus(keys, scope, id) {
    for (const key of [...keys, "acts-" + scope + "-" + id, "open-" + id]) {
      const back = document.querySelector('[data-key="' + CSS.escape(key) + '"]');
      if (back) {
        back.focus();
        if (document.activeElement === back) return;
      }
    }
  }

  // close ends the sheet; while a confirm is in flight it stays, so the
  // answer, or the lack of one, is shown where it was asked.
  function close(applied) {
    const s = sheet;
    if (!s || (s.busy && !applied)) return;
    sheet = null;
    if (!applied) forms.delete(slot(s.f, s.op));
    $("sheet-back").hidden = true;
    $("sheet").replaceChildren();
    // The button may be gone: a stopped task offers Resume instead. Focus
    // then goes to the item's buttons, or to its link.
    refocus([s.opener], s.scope, s.f.id);
  }

  // open asks before sending: it names the item, its state and the effect.
  function open(f, op, opener, scope) {
    const spec = OPS[op];
    const what = spec.text === "Stop" ? "Stop " + f.id : spec.text + " " + f.id;
    const input = spec.pr ? el("input", { type: "url", id: "sheet-pr", class: "sheet-input", placeholder: "https://github.com/owner/repo/pull/…", autocomplete: "off", "aria-label": "Pull request URL" }) : null;
    const cancel = el("button", { type: "button", class: "act", id: "sheet-cancel" }, "Cancel");
    const ok = el("button", { type: "button", class: "act primary" + (spec.danger ? " danger" : ""), id: "sheet-ok" }, spec.text);
    const result = el("p", { class: "sheet-result", role: "status" });
    cancel.addEventListener("click", () => close(false));
    ok.addEventListener("click", confirm);
    sheet = { f, op, opener, scope, input, ok, cancel, result, busy: false };
    $("sheet").replaceChildren(
      el("h2", { id: "sheet-title" }, what + "?"),
      el("p", { class: "sheet-item" }, el("b", {}, f.id), " · " + f.title),
      el("p", { class: "sheet-state" }, "State: " + f.state),
      el("p", { id: "sheet-effect", class: "sheet-effect" }, spec.effect),
      input ? el("label", { class: "sheet-field", for: "sheet-pr" }, "Pull request URL", input) : "",
      result,
      el("div", { class: "sheet-buttons" }, cancel, ok));
    $("sheet-back").hidden = false;
    (input || cancel).focus();
  }

  // trap keeps Tab inside the sheet and lets Escape or a tap outside cancel.
  function keys(e) {
    if (!sheet) return;
    if (e.key === "Escape") {
      e.preventDefault();
      close(false);
      return;
    }
    if (e.key !== "Tab") return;
    const stops = [sheet.input, $("sheet-cancel"), sheet.ok].filter(Boolean);
    const at = stops.indexOf(document.activeElement);
    const next = e.shiftKey ? (at <= 0 ? stops.length - 1 : at - 1) : (at < 0 || at === stops.length - 1 ? 0 : at + 1);
    e.preventDefault();
    stops[next].focus();
  }

  // Clicking text in the sheet leaves focus on the sheet, not the page.
  $("sheet").setAttribute("tabindex", "-1");
  $("sheet-back").addEventListener("keydown", keys);
  $("sheet-back").addEventListener("click", (e) => {
    if (e.target === $("sheet-back")) close(false);
  });

  return { bar, fromPlan, fromTask, fromItem };
};
