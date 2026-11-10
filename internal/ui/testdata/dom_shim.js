"use strict";
// A small DOM, enough to run the dashboard's own scripts against its own
// index.html without a browser. It models what the panel's behaviour depends
// on: the tree, attributes, events that bubble, hidden and inert subtrees,
// and which element holds focus.
const vm = require("node:vm");
const fs = require("node:fs");
const path = require("node:path");

class TextNode {
  constructor(data) {
    this.nodeType = 3;
    this.data = String(data);
    this.parentNode = null;
  }
  get textContent() {
    return this.data;
  }
}

const NATIVE = new Set(["button", "input", "select", "textarea"]);

class Element {
  constructor(doc, tag) {
    this.ownerDocument = doc;
    this.nodeType = 1;
    this.localName = tag.toLowerCase();
    this.tagName = tag.toUpperCase();
    this.attrs = new Map();
    this.childNodes = [];
    this.parentNode = null;
    this.listeners = {};
    this.inert = false;
    this.value = "";
  }
  get id() { return this.attrs.get("id") || ""; }
  get children() { return this.childNodes.filter((n) => n.nodeType === 1); }
  get firstElementChild() { return this.children[0] || null; }
  get hidden() { return this.attrs.has("hidden"); }
  set hidden(v) { if (v) this.attrs.set("hidden", ""); else this.attrs.delete("hidden"); }
  get className() { return this.attrs.get("class") || ""; }
  set className(v) { this.attrs.set("class", v); }
  get dataset() {
    const out = {};
    for (const [k, v] of this.attrs) if (k.startsWith("data-")) out[k.slice(5).replace(/-(.)/g, (_, c) => c.toUpperCase())] = v;
    return out;
  }
  get classList() {
    const names = () => this.className.split(/\s+/).filter(Boolean);
    const set = (list) => { this.className = list.join(" "); };
    return {
      add: (c) => { if (!names().includes(c)) set([...names(), c]); },
      remove: (c) => set(names().filter((n) => n !== c)),
      toggle: (c, force) => {
        const on = force === undefined ? !names().includes(c) : !!force;
        set(on ? [...new Set([...names(), c])] : names().filter((n) => n !== c));
      },
      contains: (c) => names().includes(c),
    };
  }
  get textContent() { return this.childNodes.map((n) => n.textContent).join(""); }
  set textContent(v) {
    this.replaceChildren(...(v === "" ? [] : [String(v)]));
  }
  // innerHTML parses markup written the way the server renders descriptions.
  set innerHTML(html) {
    this.replaceChildren(...parse(this.ownerDocument, html, true).childNodes);
  }
  getAttribute(k) { return this.attrs.has(k) ? this.attrs.get(k) : null; }
  setAttribute(k, v) { this.attrs.set(k, String(v)); }
  removeAttribute(k) { this.attrs.delete(k); }
  hasAttribute(k) { return this.attrs.has(k); }
  append(...nodes) {
    for (const n of nodes) {
      const node = typeof n === "string" ? new TextNode(n) : n;
      if (node.parentNode) node.parentNode.childNodes.splice(node.parentNode.childNodes.indexOf(node), 1);
      node.parentNode = this;
      this.childNodes.push(node);
    }
  }
  replaceChildren(...nodes) {
    for (const c of this.childNodes) c.parentNode = null;
    this.childNodes = [];
    this.append(...nodes);
  }
  remove() { if (this.parentNode) this.parentNode.replaceChildren(...this.parentNode.childNodes.filter((n) => n !== this)); }
  contains(n) { for (let x = n; x; x = x.parentNode) if (x === this) return true; return false; }
  scrollIntoView() { this.ownerDocument.scrolled.push(this.id || this.localName); }
  addEventListener(type, fn) { (this.listeners[type] = this.listeners[type] || []).push(fn); }
  dispatchEvent(ev) {
    ev.target = this;
    for (let x = this; x && !ev.stopped; x = x.parentNode) {
      for (const fn of x.listeners ? x.listeners[ev.type] || [] : []) fn(ev);
      if (!ev.bubbles) break;
    }
    return !ev.defaultPrevented;
  }
  click() {
    const ev = new Event("click", { bubbles: true });
    if (this.dispatchEvent(ev) && this.localName === "a" && (this.getAttribute("href") || "").startsWith("#")) {
      this.ownerDocument.defaultView.navigate(this.getAttribute("href"));
    }
  }
  focusable() {
    for (let x = this; x; x = x.parentNode) {
      if (x.nodeType === 1 && (x.hidden || x.inert)) return false;
    }
    if (this.hasAttribute("disabled") || !this.ownerDocument.documentElement.contains(this)) return false;
    return NATIVE.has(this.localName) || this.hasAttribute("tabindex") || this.localName === "a" && this.hasAttribute("href");
  }
  focus() { if (this.focusable()) this.ownerDocument.activeElement = this; }
  blur() { if (this.ownerDocument.activeElement === this) this.ownerDocument.activeElement = this.ownerDocument.body; }
  matches(sel) {
    const m = sel.match(/^(?:#([\w-]+)|\.([\w-]+)|\[([\w-]+)="((?:[^"\\]|\\.)*)"\]|([a-z0-9]+))$/i);
    if (!m) throw new Error("shim: unsupported selector " + sel);
    if (m[1]) return this.id === m[1];
    if (m[2]) return this.classList.contains(m[2]);
    if (m[3]) return this.attrs.get(m[3]) === m[4].replace(/\\(.)/g, "$1");
    return this.localName === m[5].toLowerCase();
  }
  querySelectorAll(sel) {
    const out = [];
    const walk = (n) => { for (const c of n.children) { if (c.matches(sel)) out.push(c); walk(c); } };
    walk(this);
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}

class Event {
  constructor(type, init = {}) {
    this.type = type;
    this.bubbles = !!init.bubbles;
    this.key = init.key;
    this.defaultPrevented = false;
    this.stopped = false;
  }
  preventDefault() { this.defaultPrevented = true; }
  stopPropagation() { this.stopped = true; }
}

const TOKEN = /<!--[\s\S]*?-->|<!doctype[^>]*>|<\/([a-z0-9]+)\s*>|<([a-z0-9]+)((?:\s+[^\s=>/]+(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+))?)*)\s*\/?>|([^<]+)/gi;
const VOID = new Set(["meta", "link", "input", "br", "hr", "img"]);
const ENTITIES = { lt: "<", gt: ">", quot: '"', amp: "&" };

// parse builds the tree of an HTML file written the way index.html is, or of
// a fragment, whose top-level text it keeps.
function parse(doc, html, fragment) {
  const root = new Element(doc, "html");
  let at = root;
  for (const m of html.matchAll(TOKEN)) {
    if (m[1]) {
      at = at.parentNode || root;
    } else if (m[2]) {
      const tag = m[2].toLowerCase();
      if (tag === "html") continue;
      const el = new Element(doc, tag);
      for (const a of m[3].matchAll(/([^\s=>/]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?/g)) {
        el.attrs.set(a[1], a[2] ?? a[3] ?? a[4] ?? "");
      }
      at.append(el);
      if (!VOID.has(tag)) at = el;
    } else if (m[4] && (fragment || at !== root) && m[4].trim()) {
      at.append(m[4].replace(/&(lt|gt|quot|amp);|&#(\d+);/g, (_, e, n) => (n ? String.fromCharCode(Number(n)) : ENTITIES[e])));
    }
  }
  return root;
}

// boot loads index.html and the scripts it names, in order, into a fresh
// page at the given hash, and returns handles for driving it.
function boot(staticDir, hash, fetchImpl, opts = {}) {
  const doc = {
    scrolled: [],
    listeners: {},
    visibilityState: "visible",
    createElement: (tag) => new Element(doc, tag),
    getElementById: (id) => doc.documentElement.querySelector("#" + id),
    querySelector: (sel) => doc.documentElement.querySelector(sel),
    addEventListener: (type, fn) => (doc.listeners[type] = doc.listeners[type] || []).push(fn),
    fire: (type) => { for (const fn of doc.listeners[type] || []) fn(new Event(type)); },
  };
  doc.documentElement = parse(doc, fs.readFileSync(path.join(staticDir, "index.html"), "utf8"));
  doc.body = doc.documentElement.querySelector("body");
  doc.activeElement = doc.body;
  const win = { listeners: {}, intervals: 0 };
  win.addEventListener = (type, fn) => (win.listeners[type] = win.listeners[type] || []).push(fn);
  win.location = { hash };
  win.history = { replaceState: (_s, _t, url) => { win.location.hash = url; } };
  win.navigate = (to) => {
    win.location.hash = to;
    queueMicrotask(() => { for (const fn of win.listeners.hashchange || []) fn(new Event("hashchange")); });
  };
  win.matchMedia = () => ({ matches: !!opts.narrow, addEventListener: () => {} });
  win.setInterval = () => ++win.intervals;
  const sandbox = Object.assign(win, {
    window: win, document: doc, fetch: fetchImpl, history: win.history, location: win.location,
    CSS: { escape: (s) => String(s).replace(/[^\w-]/g, (c) => "\\" + c) },
    URLSearchParams, setTimeout, clearTimeout, console,
  });
  doc.defaultView = win;
  vm.createContext(sandbox);
  const scripts = doc.documentElement.querySelectorAll("script").map((s) => s.getAttribute("src"));
  for (const src of scripts) vm.runInContext(fs.readFileSync(path.join(staticDir, src), "utf8"), sandbox, { filename: src });
  doc.fire("DOMContentLoaded");
  return { doc, win, scripts, Event };
}

module.exports = { boot, Event };
