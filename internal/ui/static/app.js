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

  const view = { page: "inbox", repo: "", state: "", q: "", plan: "", task: "", panel: "", tab: "description", update: 0 };
  const fetched = { plans: null, plan: null, sessions: null, inbox: null };
  let pending = 0;
  let planPending = 0;
  let sessionsPending = 0;
  let inboxPending = 0;

  const { el, label, load, keep, failed } = window.engine.dom;
  const $ = (id) => document.getElementById(id);
  const control = window.engine.control({ $, refresh: () => afterWrite(), render: () => { renderPane(); panel.head(); } });
  const panel = window.engine.panel({
    $, view, writeHash, status: () => status(), repoName, control, onPlan: (id) => onPlan(id),
    href: (id, tab, update) => here({ id, tab, update }),
    render: () => { renderPlans(); renderPane(); renderSessions(); renderInbox(); },
  });
  const sessions = window.engine.sessions({
    $, view,
    panelHref: (id, tab, update) => sessionsLink({ id, tab, update }),
    planHref: (plan, task) => link(plan, task, null),
    noteOpener: (key) => panel.noteOpener(key),
  });
  const inbox = window.engine.inbox({
    $, panelHref: (id, tab, update) => pageLink("inbox", { id, tab, update }),
    noteOpener: (key) => panel.noteOpener(key),
    refresh: () => refreshInbox(),
  });

  // addOpen writes an open panel into a hash's search part.
  function addOpen(p, open) {
    if (!open || !open.id) return;
    p.set("panel", open.id);
    if (open.tab === "communications") p.set("tab", open.tab);
    if (open.update) p.set("update", String(open.update));
  }

  const current = () => ({ id: view.panel, tab: view.tab, update: view.update });

  // query is the hash's search part: the plan filters, and the open panel
  // unless the caller passes another one (or null for none).
  function query(open = current()) {
    const p = new URLSearchParams();
    for (const k of ["repo", "state", "q"]) if (view[k]) p.set(k, view[k]);
    addOpen(p, open);
    const s = p.toString();
    return s ? "?" + s : "";
  }

  // filters is the hash's search part for the plan filters alone.
  function filters() {
    const p = new URLSearchParams();
    for (const k of ["repo", "state", "q"]) if (view[k]) p.set(k, view[k]);
    const s = p.toString();
    return s ? "?" + s : "";
  }

  // pageLink is the Inbox or Sessions tab, with a panel open over it or none.
  function pageLink(page, open = current()) {
    const p = new URLSearchParams();
    addOpen(p, open);
    const s = p.toString();
    return "#/" + page + (s ? "?" + s : "");
  }

  const sessionsLink = (open) => pageLink("sessions", open);

  // here links to the page being shown, so the panel opens over it.
  function here(open) {
    return view.page === "plans" ? link(view.plan, view.task, open) : pageLink(view.page, open);
  }

  function link(plan, task, open) {
    let path = "#/plans";
    if (ID.test(plan || "")) path += "/" + plan;
    if (ID.test(task || "")) path += "/" + task;
    return path + query(open);
  }

  function readHash() {
    const [path, search] = location.hash.replace(/^#/, "").split("?");
    const parts = path.split("/").filter(Boolean);
    const p = new URLSearchParams(search || "");
    view.page = ["plans", "sessions"].includes(parts[0]) ? parts[0] : "inbox";
    // The other tabs leave the Plans filters and selection as they were.
    if (view.page === "plans") {
      view.repo = p.get("repo") || "";
      view.state = p.get("state") || "";
      view.q = p.get("q") || "";
      view.plan = ID.test(parts[1] || "") ? parts[1] : "";
      view.task = ID.test(parts[2] || "") ? parts[2] : "";
    }
    const open = p.get("panel") || "";
    view.panel = /^[pt][0-9]+$/.test(open) ? open : "";
    view.tab = p.get("tab") === "communications" ? "communications" : "description";
    view.update = view.panel && /^[1-9][0-9]*$/.test(p.get("update") || "") ? Number(p.get("update")) : 0;
  }

  function writeHash() {
    history.replaceState(null, "", here());
  }

  // opener is a link that opens the item panel; it remembers itself so
  // closing the panel can return focus to it.
  function opener(id, text) {
    const key = "open-" + id;
    const a = el("a", { href: link(view.plan, view.task, { id, tab: "description" }), "data-key": key }, text);
    a.addEventListener("click", () => panel.noteOpener(key));
    return a;
  }

  // onPlan says whether the plan on show, refreshed with the page, holds the
  // item: the plan itself or one of its tasks.
  function onPlan(id) {
    const d = view.page === "plans" && fetched.plan && fetched.plan.data;
    return !!d && (d.plan.id === id || d.tasks.some((t) => t.id === id));
  }

  function repoName(id) {
    const repos = fetched.plans && fetched.plans.data ? fetched.plans.data.repos : [];
    const repo = repos.find((r) => r.id === id);
    if (repo && repo.slug) return repo.slug;
    // Opened straight on Sessions, the plan list may not be loaded yet.
    const rows = fetched.sessions && fetched.sessions.data ? fetched.sessions.data.sessions : [];
    const row = rows.find((r) => r.repo === id && r.repo_slug);
    return row ? row.repo_slug : id;
  }

  function status() {
    const node = $("status");
    const shown = { inbox: [fetched.inbox], sessions: [fetched.sessions], plans: [fetched.plans, fetched.plan] }[view.page];
    const envs = [...shown, ...panel.envs()].filter(Boolean);
    const stale = envs.find((e) => e.stale || e.failed);
    const times = envs.map((e) => e.fetched).filter(Boolean).sort();
    const at = times.length ? new Date(times[0]).toLocaleTimeString() : "never";
    node.className = stale ? "status stale" : "status";
    if (!stale) node.textContent = "Last refresh " + at;
    else node.textContent = (times.length ? "Stale since " + at + " · " : "") + stale.error;
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

  // feedbackLink opens the unresolved question that applies to a task: its
  // own, or its plan's, where the thread lives.
  function feedbackLink(t) {
    const fb = t.feedback;
    const key = "fb-" + t.id;
    const a = el("a", { href: link(view.plan, view.task, { id: fb.item, tab: "communications", update: fb.update }), "data-key": key }, window.engine.fmt.feedbackLabel(fb, t.id));
    a.addEventListener("click", () => panel.noteOpener(key));
    return el("div", { class: "feedback" }, a);
  }

  function stateCell(t) {
    const text = GROUPS.find(([g]) => g === t.group)[1];
    const extra = (IMPLIED[t.group] === t.state ? "" : " · " + t.state) + (t.held ? " (held)" : "");
    return el("td", { "data-label": "State" }, el("span", { class: "group-" + t.group }, text), extra, t.feedback ? feedbackLink(t) : null);
  }

  function waitingCell(t) {
    const items = t.waiting.map((w) => {
      const name = w.id + (w.title ? " · " + w.title : "") + " (" + w.state + ")";
      return el("li", {}, el("a", { href: link(w.plan, w.id), "data-key": "dep-" + t.id + "-" + w.id }, name));
    });
    return el("td", { "data-label": "Waiting for" }, items.length ? el("ul", { class: "waiting" }, ...items) : "—");
  }

  function actionsCell(t) {
    const bar = control.bar(control.fromTask(t), "pane");
    return el("td", { "data-label": "Actions" }, bar || "—");
  }

  // afterWrite brings every view a write may have changed up to date.
  function afterWrite() {
    const reads = [refreshPlan(false), panel.reload()];
    if (view.page === "plans") reads.push(refreshPlans());
    return Promise.all(reads);
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
      const head = el("tr", {}, ...["Task", "State", "Host", "Waiting for", "Actions"].map((h) => el("th", { scope: "col" }, h)));
      const rows = d.tasks.map((t) =>
        el("tr", { id: "task-" + t.id, class: t.id === view.task ? "selected" : null },
          el("td", { "data-label": "Task" }, opener(t.id, t.title), el("div", { class: "muted" }, t.id)),
          stateCell(t),
          el("td", { "data-label": "Host" }, t.host || "—"),
          waitingCell(t),
          actionsCell(t)));
      const body = rows.length ? el("table", {}, el("thead", {}, head), el("tbody", {}, ...rows)) : el("p", { class: "empty" }, "This plan has no tasks yet.");
      pane.replaceChildren(banner || "",
        el("h2", {}, "Tasks · " + d.plan.id + " · " + label(d.plan.state), " ", opener(d.plan.id, "Plan details")),
        el("div", { class: "muted" }, d.plan.title + " · " + (d.plan.slug || d.plan.repo)),
        control.bar(control.fromPlan(d), "pane") || "", counts, body);
    });
  }

  // refreshOverview reloads the inbox, the sessions and the plan list with one
  // read: the process answers one envelope for each, as its own route would.
  async function refreshOverview() {
    const nInbox = ++inboxPending;
    const nSessions = ++sessionsPending;
    const nPlans = ++pending;
    let got;
    try {
      got = await load("/api/overview" + filters());
    } catch (err) {
      got = {};
      for (const name of ["inbox", "sessions", "plans"]) got[name] = failed(fetched[name], err);
    }
    // A view that failed carries an error and no data: the page keeps what it holds, stale.
    const settle = (name) => (got[name].error && !got[name].data ? failed(fetched[name], new Error(got[name].error)) : got[name]);
    if (nInbox === inboxPending) {
      fetched.inbox = settle("inbox");
      renderInbox();
    }
    if (nSessions === sessionsPending) {
      fetched.sessions = settle("sessions");
      renderSessions();
    }
    if (nPlans === pending) {
      fetched.plans = settle("plans");
      renderPlans();
    }
    panel.head();
    status();
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
    panel.head();
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
    if (view.tab === "communications") panel.adopt(fetched.plan);
    renderPane();
    status();
    if (scroll && view.task) {
      const row = $("task-" + view.task);
      if (row) row.scrollIntoView({ block: "nearest" });
    }
  }

  function renderSessions() {
    sessions.render(fetched.sessions);
  }

  // renderInbox draws the Inbox tab; refreshInbox reloads it.
  function renderInbox() {
    inbox.render(fetched.inbox);
  }

  async function refreshInbox() {
    const n = ++inboxPending;
    let env;
    try {
      env = await load("/api/inbox");
    } catch (err) {
      env = failed(fetched.inbox, err);
    }
    if (n !== inboxPending) return;
    fetched.inbox = env;
    renderInbox();
    panel.head();
    status();
  }

  // chrome shows the page the hash names and points each tab at the other
  // pages' last state, so switching tabs keeps filters and selection.
  function chrome() {
    const onPlans = view.page === "plans";
    $("list").hidden = $("pane").hidden = !onPlans;
    $("sessions").hidden = view.page !== "sessions";
    $("inbox").hidden = view.page !== "inbox";
    $("workspace").classList.toggle("single", !onPlans);
    $("tab-inbox").setAttribute("href", pageLink("inbox", null));
    $("tab-plans").setAttribute("href", link(view.plan, view.task, null));
    $("tab-sessions").setAttribute("href", sessionsLink(null));
    for (const page of ["inbox", "plans", "sessions"]) {
      if (page === view.page) $("tab-" + page).setAttribute("aria-current", "page");
      else $("tab-" + page).removeAttribute("aria-current");
    }
  }

  // refresh reads the overview, the plan on show and the open thread: three
  // calls to the control server, made together.
  function refresh() {
    if (document.visibilityState === "visible") {
      refreshOverview();
      if (view.page === "plans") refreshPlan(false);
      panel.refresh();
    }
  }

  let typing = 0;
  function filterChanged() {
    view.q = $("search").value;
    view.repo = $("repo").value;
    view.state = $("state").value;
    writeHash();
    chrome();
    // Links carry the filters, so redraw them before the filtered list arrives.
    renderPlans();
    renderPane();
    clearTimeout(typing);
    typing = setTimeout(() => refreshPlans(true), 200);
  }

  function navigate() {
    const before = view.plan;
    readHash();
    $("search").value = view.q;
    chrome();
    if (before !== view.plan) fetched.plan = null;
    renderPane();
    if (!fetched.plans) renderPlans();
    renderSessions();
    renderInbox();
    panel.sync();
    refreshOverview();
    if (view.page === "plans") refreshPlan(true);
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
