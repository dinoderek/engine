"use strict";
// The item side panel: a plan or task's Description and Communications.
// The server renders descriptions to blocks and projects the thread; this
// file builds DOM nodes from them, always as text, and owns keyboard focus.
window.engine = window.engine || {};
window.engine.panel = function (ctx) {
  const { el, label, load, failed } = window.engine.dom;
  const $ = ctx.$;
  const view = ctx.view;
  const TABS = ["description", "communications"];
  const SAFE = /^(https?:\/\/|mailto:)/i;
  const KINDS = { say: "Message", ask: "Question", reply: "Reply", acknowledged: "Acknowledged", resolved: "Decision" };

  // shown is the open item's ID. item and threadEnv are envelopes in the page's
  // usual shape; thread holds the messages read so far and the cursor.
  const st = {
    shown: "", item: null, thread: null, threadEnv: null, opener: "", rendered: "",
    nodes: new Map(), badges: new Map(), update: 0, want: 0, itemSeq: 0, threadSeq: 0, statusSeq: 0,
  };

  function inline(runs) {
    return (runs || []).map((r) => {
      if (r.href && SAFE.test(r.href)) return el("a", { href: r.href, target: "_blank", rel: "noopener noreferrer" }, r.text);
      return r.code ? el("code", {}, r.text) : r.text;
    });
  }

  function block(b) {
    switch (b.type) {
      case "heading":
        return el("h" + Math.min(6, b.level + 2), {}, ...inline(b.inline));
      case "paragraph":
        return el("p", {}, ...inline(b.inline));
      case "list":
        return el(b.ordered ? "ol" : "ul", {}, ...b.items.map((it) => el("li", {}, ...inline(it))));
      case "code":
        return el("pre", { tabindex: "0" }, el("code", b.lang ? { "data-lang": b.lang } : {}, b.text));
      default:
        return null;
    }
  }

  function renderDescription() {
    const env = st.item;
    const key = JSON.stringify(env && (env.data ? env.data.description : env.error));
    if (key === st.rendered) return;
    st.rendered = key;
    const box = $("view-description");
    if (!env) {
      box.replaceChildren(el("p", { class: "empty" }, "Loading…"));
    } else if (!env.data) {
      box.replaceChildren(el("p", { class: "empty" }, env.error));
    } else if (!env.data.description.length) {
      box.replaceChildren(el("p", { class: "empty" }, "No description."));
    } else {
      box.replaceChildren(...env.data.description.map(block).filter(Boolean));
    }
  }

  function renderHead() {
    const d = st.item && st.item.data;
    $("panel-ref").textContent = st.shown + (d ? " · " + ctx.repoName(d.repo) : "");
    $("panel-title").textContent = d ? d.title : st.item ? "Unavailable" : "Loading…";
    $("panel-state").textContent = d ? label(d.state) : "";
    const down = [st.item, st.threadEnv].some((e) => e && e.stale);
    const gone = st.item && !st.item.data ? st.item.error : "";
    $("panel-banner").hidden = !down && !gone;
    $("panel-banner").textContent = gone || "Control server unavailable; showing the last successful view.";
  }

  function questionText(q) {
    if (q.state === "resolved") return "Resolved";
    const parts = ["Open"];
    if (q.acknowledged) parts.push("acknowledged");
    if (q.replies) parts.push(q.replies + (q.replies === 1 ? " reply" : " replies"));
    return parts.join(" · ");
  }

  function messageNode(m) {
    const head = el("div", { class: "message-head" },
      el("b", {}, m.author), " ",
      el("span", { class: "muted" }, (KINDS[m.kind] || m.kind) + (m.recipient ? " to " + m.recipient : "")), " ",
      el("time", { datetime: m.at }, new Date(m.at).toLocaleString()));
    if (m.requires_reply) {
      const badge = el("span", { class: "question" });
      st.badges.set(m.id, badge);
      head.append(" ", badge);
    }
    if (m.reply_to) {
      head.append(" ", el("a", { class: "muted", href: ctx.href(view.panel, "communications", m.reply_to) }, "in reply to update " + m.reply_to));
    }
    return el("li", { id: "update-" + m.id, class: "message kind-" + m.kind, tabindex: "-1", "data-key": "update-" + m.id },
      head, m.body ? el("div", { class: "message-body" }, m.body) : null);
  }

  // renderThread adds the messages that have no node yet and refreshes the
  // question badges, leaving existing nodes, and so focus and scroll, alone.
  function renderThread() {
    const t = st.thread;
    const added = [];
    if (t) {
      for (const m of t.messages) {
        if (!st.nodes.has(m.id)) {
          const node = messageNode(m);
          st.nodes.set(m.id, node);
          $("messages").append(node);
          added.push(node);
        }
      }
      for (const [id, badge] of st.badges) {
        const q = t.questions.get(id);
        badge.textContent = q ? questionText(q) : "";
        badge.className = "question " + (q && q.state === "resolved" ? "resolved" : "open");
      }
    }
    $("thread-empty").hidden = !t || st.nodes.size > 0;
    $("more").hidden = !t || !t.more;
    const failedLoad = st.threadEnv && st.threadEnv.failed && !st.threadEnv.data;
    $("thread-note").textContent = failedLoad ? st.threadEnv.error : t ? "" : "Loading messages…";
    return added;
  }

  function applyWant() {
    const n = st.want;
    if (!n) return;
    const node = st.nodes.get(n);
    if (!node && st.thread && st.thread.more) return;
    st.want = 0;
    for (const other of st.nodes.values()) other.classList.remove("anchored");
    if (!node) {
      $("thread-note").textContent = "Update " + n + " is not a message in this thread.";
      return;
    }
    node.classList.add("anchored");
    node.scrollIntoView({ block: "nearest" });
    node.focus();
  }

  function renderTabs() {
    for (const tab of TABS) {
      const on = tab === view.tab;
      const button = $("tab-" + tab);
      button.setAttribute("aria-selected", on ? "true" : "false");
      button.setAttribute("tabindex", on ? "0" : "-1");
      $("view-" + tab).hidden = !on;
    }
  }

  function absorb(t, d) {
    for (const m of d.messages) {
      if (!t.seen.has(m.id)) {
        t.seen.add(m.id);
        t.messages.push(m);
      }
    }
    t.next = d.next;
    t.more = d.more;
  }

  // fetchThread reads the window after the cursor, or after opts.after. With
  // statusOnly it keeps the messages read so far and takes only the question
  // statuses, which is how a refresh behaves while pages remain to load.
  async function fetchThread(opts) {
    const id = st.shown;
    // A status-only refresh must not cancel a page the user asked for.
    const n = opts.statusOnly ? ++st.statusSeq : ++st.threadSeq;
    const t = st.thread;
    const after = opts.after !== undefined ? opts.after : t ? t.next : 0;
    const anchor = opts.anchor ? "&anchor=" + opts.anchor : "";
    let env;
    try {
      env = await load("/api/items/" + id + "/thread?after=" + after + anchor);
    } catch (err) {
      env = failed(st.threadEnv, err);
    }
    if (n !== (opts.statusOnly ? st.statusSeq : st.threadSeq) || id !== st.shown) return;
    st.threadEnv = env;
    if (env.data && !env.failed) {
      st.thread = st.thread || { messages: [], seen: new Set(), questions: new Map(), next: 0, more: false };
      for (const q of env.data.questions) st.thread.questions.set(q.id, q);
      if (!opts.statusOnly) absorb(st.thread, env.data);
      env.data = st.thread;
    } else if (env.failed && !env.data) {
      st.thread = null;
      st.nodes.clear();
      st.badges.clear();
      $("messages").replaceChildren();
    }
    const added = renderThread();
    renderHead();
    if (opts.more && added.length && document.activeElement === $("more")) added[0].focus();
    applyWant();
    ctx.status();
  }

  function ensureThread() {
    if (!st.shown || view.tab !== "communications") return;
    if (!st.thread) {
      fetchThread({ after: 0, anchor: st.want });
    } else if (st.want && !st.nodes.has(st.want) && st.thread.more) {
      fetchThread({ anchor: st.want });
    } else {
      applyWant();
    }
  }

  async function fetchItem() {
    const id = st.shown;
    const n = ++st.itemSeq;
    let env;
    try {
      env = await load("/api/items/" + id);
    } catch (err) {
      env = failed(st.item && st.item.data && st.item.data.id === id ? st.item : null, err);
    }
    if (n !== st.itemSeq || id !== st.shown) return;
    st.item = env;
    renderHead();
    renderDescription();
    ctx.status();
  }

  function reset() {
    st.item = st.thread = st.threadEnv = null;
    st.rendered = "";
    st.update = st.want = 0;
    st.nodes.clear();
    st.badges.clear();
    st.itemSeq++;
    st.threadSeq++;
    st.statusSeq++;
    $("messages").replaceChildren();
    $("thread-note").textContent = "";
  }

  function mobileOverlay() {
    return !!(st.shown && window.matchMedia && window.matchMedia("(max-width: 800px)").matches);
  }

  function applyInert() {
    for (const id of ["bar", "list", "pane"]) $(id).inert = mobileOverlay();
  }

  function restoreFocus() {
    const target = st.opener && document.querySelector('[data-key="' + CSS.escape(st.opener) + '"]');
    (target || $("pane")).focus();
  }

  // sync brings the panel in line with the page's view state: it opens,
  // closes or switches item, tab or selected update, and moves focus only when
  // the panel opens or closes or a message is selected.
  function sync() {
    const id = view.panel;
    if (id !== st.shown) {
      const was = st.shown;
      st.shown = id;
      reset();
      $("panel").hidden = !id;
      $("workspace").classList.toggle("with-panel", !!id);
      applyInert();
      if (!id) {
        restoreFocus();
        return;
      }
      renderHead();
      renderDescription();
      renderThread();
      fetchItem();
      if (!was) $("panel-title").focus();
    }
    if (!id) return;
    if (view.update !== st.update) {
      st.update = view.update;
      st.want = view.update;
    }
    renderTabs();
    ensureThread();
  }

  function select(tab, focus) {
    view.tab = tab;
    ctx.writeHash();
    renderTabs();
    if (focus) $("tab-" + tab).focus();
    ensureThread();
  }

  function close() {
    view.panel = "";
    view.update = 0;
    ctx.writeHash();
    ctx.render();
    sync();
  }

  function refresh() {
    if (!st.shown) return;
    fetchItem();
    if (view.tab !== "communications") return;
    if (st.thread) fetchThread({ statusOnly: st.thread.more });
    else ensureThread();
  }

  function tabKey(e) {
    const i = TABS.indexOf(view.tab);
    const next = { ArrowRight: (i + 1) % TABS.length, ArrowLeft: (i + TABS.length - 1) % TABS.length, Home: 0, End: TABS.length - 1 }[e.key];
    if (next === undefined) return;
    e.preventDefault();
    select(TABS[next], true);
  }

  function init() {
    $("panel-close").addEventListener("click", close);
    $("panel").addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close();
      }
    });
    $("tabs").addEventListener("keydown", tabKey);
    for (const tab of TABS) $("tab-" + tab).addEventListener("click", () => select(tab, false));
    $("more").addEventListener("click", () => fetchThread({ more: true }));
    if (window.matchMedia) window.matchMedia("(max-width: 800px)").addEventListener("change", applyInert);
  }

  init();
  return {
    sync,
    refresh,
    envs: () => [st.item, st.threadEnv].filter(Boolean),
    noteOpener: (key) => {
      st.opener = key;
    },
  };
};
