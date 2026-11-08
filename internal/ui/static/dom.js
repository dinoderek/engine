"use strict";
// Shared helpers. Every value from the server is inserted as text.
window.engine = window.engine || {};
window.engine.dom = (function () {
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

  // keep remembers which element had focus so a re-render can restore it.
  function keep(render) {
    const key = document.activeElement && document.activeElement.dataset.key;
    render();
    if (key) {
      const again = document.querySelector('[data-key="' + CSS.escape(key) + '"]');
      if (again) again.focus();
    }
  }

  // failed turns a load error into the envelope the page keeps: the last data
  // stays, marked stale. An unknown item keeps nothing.
  function failed(env, err) {
    if (err.status === 404) return { error: err.message, failed: true };
    return Object.assign({}, env, { error: err.message, failed: true, stale: !!(env && env.data) });
  }

  return { el, label, load, keep, failed };
})();
