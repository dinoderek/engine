"use strict";
// The engine dashboard. Every value from the server is inserted as text.
(function () {
  const REFRESH_MS = 30000;
  const GROUPS = [
    ["done", "Done"],
    ["in-progress", "In progress"],
    ["ready", "Ready to run"],
    ["blocked", "Blocked"],
  ];
  const IMPLIED = { done: "done", "in-progress": "running", ready: "todo" };
  const ID = /^[prt][0-9]+$/;

  const view = { repo: "", state: "", q: "", plan: "", task: "" };
  const fetched = { plans: null, plan: null };
  let pending = 0;
  let planPending = 0;

  const $ = (id) => document.getElementById(id);

  function el(tag, attrs, ...children) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v !== undefined && v !== null && v !== false) node.setAttribute(k, v);
    }
    for (const c of children) {
      if (c !== undefined && c !== null) node.append(c);
    }
    return node;
  }

  function label(state) {
    return state ? state.charAt(0).toUpperCase() + state.slice(1).replace(/-/g, " ") : "";
  }

  function query() {
    const p = new URLSearchParams();
    for (const k of ["repo", "state", "q"]) if (view[k]) p.set(k, view[k]);
    const s = p.toString();
    return s ? "?" + s : "";
  }

  function link(plan, task) {
    let path = "#/plans";
    if (ID.test(plan || "")) path += "/" + plan;
    if (ID.test(task || "")) path += "/" + task;
    return path + query();
  }

  function readHash() {
    const [path, search] = location.hash.replace(/^#/, "").split("?");
    const parts = path.split("/").filter(Boolean);
    const p = new URLSearchParams(search || "");
    view.repo = p.get("repo") || "";
    view.state = p.get("state") || "";
    view.q = p.get("q") || "";
    view.plan = ID.test(parts[1] || "") ? parts[1] : "";
    view.task = ID.test(parts[2] || "") ? parts[2] : "";
  }

  function writeHash() {
    history.replaceState(null, "", link(view.plan, view.task));
  }

  async function load(url) {
    const res = await fetch(url, { headers: { Accept: "application/json" }, cache: "no-store" });
    let body = {};
    try {
      body = await res.json();
    } catch (_) {
      body = { error: "unreadable response" };
    }
    if (!res.ok) throw Object.assign(new Error(body.error || res.statusText), { status: res.status });
    return body;
  }

  function status() {
    const node = $("status");
    const envs = [fetched.plans, fetched.plan].filter(Boolean);
    const stale = envs.find((e) => e.stale || e.failed);
    const times = envs.map((e) => e.fetched).filter(Boolean).sort();
    const at = times.length ? new Date(times[0]).toLocaleTimeString() : "never";
    node.className = stale ? "status stale" : "status";
    if (!stale) node.textContent = "Last refresh " + at;
    else node.textContent = (times.length ? "Stale since " + at + " · " : "") + stale.error;
  }

  // keep remembers which element had focus so a re-render can restore it.
  function keep(render) {
    const key = document.activeElement && document.activeElement.dataset.key;
    render();
    if (key) {
      const again = document.querySelector('[data-key="' + CSS.escape(key) + '"]');
      if (again) again.focus();
    }
  }

  function options(select, values, chosen) {
    const keepFirst = select.firstElementChild;
    select.replaceChildren(keepFirst);
    for (const [value, text] of values) select.append(el("option", { value }, text));
    if (chosen && !values.some(([v]) => v === chosen)) select.append(el("option", { value: chosen }, chosen));
    select.value = chosen;
  }

  function renderPlans() {
    const env = fetched.plans;
    if (!env || !env.data) {
      $("plans").replaceChildren(el("li", { class: "empty" }, env ? env.error : "Loading…"));
      return;
    }
    const data = env.data;
    options($("repo"), data.repos.map((r) => [r.id, r.slug || r.id]), view.repo);
    options($("state"), data.states.map((s) => [s, label(s)]), view.state);
    keep(() => {
      const rows = data.plans.map((p) =>
        el("li", {},
          el("a", { href: link(p.id), "data-key": "plan-" + p.id, "aria-current": p.id === view.plan ? "true" : null },
            el("div", { class: "meta" }, p.id + " · " + (p.slug || p.repo)),
            el("div", {}, p.title),
            el("div", { class: "meta" }, label(p.state)))));
      if (!rows.length) {
        const what = data.total ? "No plans match these filters." : "No plans yet.";
        rows.push(el("li", { class: "empty" }, what));
      }
      $("plans").replaceChildren(...rows);
    });
  }

  function stateCell(t) {
    const text = GROUPS.find(([g]) => g === t.group)[1];
    const extra = IMPLIED[t.group] === t.state ? "" : " · " + t.state;
    return el("td", { "data-label": "State" }, el("span", { class: "group-" + t.group }, text), extra);
  }

  function waitingCell(t) {
    const items = t.waiting.map((w) => {
      const name = w.id + (w.title ? " · " + w.title : "") + " (" + w.state + ")";
      if (!w.plan) return el("li", {}, name + " · standalone task");
      return el("li", {}, el("a", { href: link(w.plan, w.id), "data-key": "dep-" + t.id + "-" + w.id }, name));
    });
    return el("td", { "data-label": "Waiting for" }, items.length ? el("ul", { class: "waiting" }, ...items) : "—");
  }

  function renderPane() {
    const pane = $("pane");
    const env = fetched.plan;
    if (!view.plan) {
      pane.replaceChildren(el("p", { class: "empty" }, "Select a plan to see its tasks."));
      return;
    }
    if (!env || !env.data) {
      pane.replaceChildren(el("p", { class: "empty" }, env ? env.error : "Loading " + view.plan + "…"));
      return;
    }
    const d = env.data;
    keep(() => {
      const banner = env.stale ? el("p", { class: "banner" }, "Control server unavailable; showing the last successful view.") : null;
      const counts = el("div", { class: "counts", "aria-label": "Task summary" },
        ...GROUPS.map(([g, text]) => el("span", { class: "count group-" + g }, el("b", {}, String(d.counts[g.replace("-", "_")])), " " + text)));
      const head = el("tr", {}, ...["Task", "State", "Host", "Waiting for"].map((h) => el("th", { scope: "col" }, h)));
      const rows = d.tasks.map((t) =>
        el("tr", { id: "task-" + t.id, class: t.id === view.task ? "selected" : null },
          el("td", { "data-label": "Task" }, t.title, el("div", { class: "muted" }, t.id)),
          stateCell(t),
          el("td", { "data-label": "Host" }, t.host || "—"),
          waitingCell(t)));
      const body = rows.length ? el("table", {}, el("thead", {}, head), el("tbody", {}, ...rows)) : el("p", { class: "empty" }, "This plan has no tasks yet.");
      pane.replaceChildren(banner || "",
        el("h2", {}, "Tasks · " + d.plan.id + " · " + label(d.plan.state)),
        el("div", { class: "muted" }, d.plan.title + " · " + (d.plan.slug || d.plan.repo)),
        counts, body);
    });
  }

  function failed(env, err) {
    if (err.status === 404) return { error: err.message, failed: true };
    return Object.assign({}, env, { error: err.message, failed: true, stale: !!(env && env.data) });
  }

  // refreshPlans reloads the list; cached reuses the dashboard's last read.
  async function refreshPlans(cached) {
    const n = ++pending;
    const q = query();
    let env;
    try {
      env = await load("/api/plans" + q + (cached ? (q ? "&" : "?") + "cached=1" : ""));
    } catch (err) {
      env = failed(fetched.plans, err);
    }
    if (n !== pending) return;
    fetched.plans = env;
    renderPlans();
    status();
  }

  async function refreshPlan(scroll) {
    const id = view.plan;
    const n = ++planPending;
    if (!id) {
      fetched.plan = null;
    } else {
      let env;
      try {
        env = await load("/api/plans/" + id);
      } catch (err) {
        env = failed(fetched.plan && fetched.plan.data && fetched.plan.data.plan.id === id ? fetched.plan : null, err);
      }
      if (n !== planPending) return;
      fetched.plan = env;
    }
    renderPane();
    status();
    if (scroll && view.task) {
      const row = $("task-" + view.task);
      if (row) row.scrollIntoView({ block: "nearest" });
    }
  }

  function refresh() {
    if (document.visibilityState === "visible") {
      refreshPlans();
      refreshPlan(false);
    }
  }

  let typing = 0;
  function filterChanged() {
    view.q = $("search").value;
    view.repo = $("repo").value;
    view.state = $("state").value;
    writeHash();
    clearTimeout(typing);
    typing = setTimeout(() => refreshPlans(true), 200);
  }

  function navigate() {
    const before = view.plan;
    readHash();
    $("search").value = view.q;
    if (before !== view.plan) fetched.plan = null;
    renderPane();
    if (!fetched.plans) renderPlans();
    refreshPlans();
    refreshPlan(true);
  }

  document.addEventListener("DOMContentLoaded", () => {
    $("search").addEventListener("input", filterChanged);
    $("repo").addEventListener("change", filterChanged);
    $("state").addEventListener("change", filterChanged);
    window.addEventListener("hashchange", navigate);
    document.addEventListener("visibilitychange", refresh);
    setInterval(refresh, REFRESH_MS);
    navigate();
  });
})();
