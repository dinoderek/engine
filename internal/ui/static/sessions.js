"use strict";
// The Sessions tab. The server orders the rows, feedback requests first; this
// file builds DOM nodes from them, always as text, and keeps links stable so
// a refresh leaves keyboard focus where it was.
window.engine = window.engine || {};
window.engine.fmt = (function () {
  const OBSERVED = { working: "Working", waiting: "Waiting", limited: "Limited", live: "Live", exited: "Exited", unknown: "Unknown" };

  // observed names the harness state a host reported; anything else is unknown.
  function observed(state) {
    return Object.prototype.hasOwnProperty.call(OBSERVED, state) ? OBSERVED[state] : "Unknown";
  }

  // ago is a compact age such as "5 min ago"; empty when there is no time.
  function ago(iso, now) {
    const t = Date.parse(iso);
    if (!iso || Number.isNaN(t)) return "";
    const s = Math.max(0, Math.round((now - t) / 1000));
    if (s < 45) return "just now";
    const m = Math.round(s / 60);
    if (m < 60) return m + " min ago";
    const h = Math.round(m / 60);
    return h < 48 ? h + " h ago" : Math.round(h / 24) + " d ago";
  }

  // feedbackLabel says whose question is open: the item's own or its plan's.
  function feedbackLabel(fb, item) {
    if (fb.item === item) return "Needs feedback";
    return "Needs feedback · " + (fb.item[0] === "p" ? "plan " : "task ") + fb.item;
  }
  return { observed, ago, feedbackLabel };
})();

window.engine.sessions = function (ctx) {
  const { el, keep } = window.engine.dom;
  const { observed, ago, feedbackLabel } = window.engine.fmt;
  const $ = ctx.$;
  const view = ctx.view;

  function when(iso, now) {
    const text = ago(iso, now);
    return text ? el("time", { datetime: iso, title: new Date(iso).toLocaleString() }, text) : "—";
  }

  // threadLink opens an item's Communications at one update, over this view.
  function threadLink(ref, key, text) {
    const on = view.panel === ref.item && view.update === ref.update;
    const a = el("a", { href: ctx.panelHref(ref.item, "communications", ref.update), "data-key": key, "aria-current": on ? "true" : null }, text);
    a.addEventListener("click", () => ctx.noteOpener(key));
    return a;
  }

  function itemCell(r) {
    const key = "s-open-" + r.item;
    const open = el("a", { href: ctx.panelHref(r.item, "description"), "data-key": key }, r.title || r.item);
    open.addEventListener("click", () => ctx.noteOpener(key));
    const parts = [r.item + " · " + (r.repo_slug || r.repo) + " · " + (r.role === "planner" ? "Planner" : "Executor")];
    const meta = el("div", { class: "muted" }, parts.join(""));
    // A standalone task has no plan page to go to, so it only opens its panel.
    if (r.kind === "task" && r.plan) {
      meta.append(" · ", el("a", { href: ctx.planHref(r.plan, r.item), "data-key": "s-plan-" + r.item }, "in " + r.plan));
    }
    return el("td", { "data-label": "Plan or task" }, open, meta);
  }

  function stateCell(r) {
    const cell = el("td", { "data-label": "Session state" }, el("span", { class: "observed obs-" + (observed(r.observed).toLowerCase()) }, observed(r.observed)),
      el("div", { class: "muted" }, "Workflow: " + r.state));
    if (r.feedback) {
      cell.append(el("div", { class: "feedback" }, threadLink(r.feedback, "s-fb-" + r.item, feedbackLabel(r.feedback, r.item))));
    }
    return cell;
  }

  function latestCell(r, now) {
    const cell = el("td", { "data-label": "Latest attached update" });
    if (!r.latest) {
      cell.append(el("span", { class: "empty" }, "No messages yet."));
      return cell;
    }
    const l = r.latest;
    cell.append(threadLink(l, "s-latest-" + r.item, l.summary || "(no text)"),
      el("div", { class: "muted" }, l.author + " · ", when(l.at, now)));
    return cell;
  }

  function row(r, now) {
    return el("tr", { id: "session-" + r.item, class: r.feedback ? "needs-feedback" : null },
      itemCell(r), stateCell(r),
      el("td", { "data-label": "Host" }, r.host || "—"),
      el("td", { "data-label": "Harness" }, r.harness || "—"),
      el("td", { "data-label": "Last seen" }, when(r.last_seen, now)),
      latestCell(r, now));
  }

  function body(data, now) {
    if (!data.sessions.length) return el("p", { class: "empty" }, "No active sessions.");
    const head = el("tr", {}, ...["Plan or task", "Session state", "Host", "Harness", "Last seen", "Latest attached update"].map((h) => el("th", { scope: "col" }, h)));
    return el("table", {}, el("thead", {}, head), el("tbody", {}, ...data.sessions.map((r) => row(r, now))));
  }

  function tabText(env) {
    const d = env && env.data;
    return d ? "Sessions · " + d.sessions.length : "Sessions";
  }

  // render draws the tab's count and, from the envelope, the table. The last
  // good envelope stays on screen, marked stale, while the server is down.
  function render(env, now = Date.now()) {
    $("tab-sessions").textContent = tabText(env);
    const box = $("sessions");
    if (!env || !env.data) {
      keep(() => box.replaceChildren(el("h1", { class: "visually-hidden" }, "Sessions"), el("p", { class: "empty" }, env ? env.error : "Loading sessions…")));
      return;
    }
    const d = env.data;
    const need = el("span", { class: d.feedback ? "need" : "muted" }, d.feedback + (d.feedback === 1 ? " needs feedback" : " need feedback"));
    const head = el("h1", { class: "sessions-head" }, "Active sessions · " + d.sessions.length + " ", need);
    const banner = env.stale ? el("p", { class: "banner" }, "Control server unavailable; showing the last successful view.") : null;
    keep(() => box.replaceChildren(head, banner || "", body(d, now)));
  }
  return { render };
};
