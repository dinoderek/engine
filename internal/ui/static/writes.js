"use strict";
// The page's one way to write. A form made when a dialog opens holds one
// request key, so a resend after a lost reply applies once. A definite answer
// (2xx or 4xx) ends the attempt: the next send makes a new key.
window.engine = window.engine || {};
window.engine.writes = (function () {
  // newKey is a random request key. crypto.randomUUID exists only in a secure
  // context, and the dashboard reached over plain http by its --host name is
  // not one; getRandomValues exists in every context.
  function newKey() {
    if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
    return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, "0")).join("");
  }

  function form() {
    let key = newKey();
    return {
      // send posts fields to a write route and never throws. It resolves
      // { ok, status, data, error, reason }; status 0 is a request that got no
      // answer, and so does 5xx: send the same form again to resend.
      async send(path, fields) {
        let res;
        try {
          res = await fetch(path, {
            method: "POST",
            headers: { "Content-Type": "application/json", Accept: "application/json" },
            cache: "no-store",
            body: JSON.stringify(Object.assign({ key }, fields)),
          });
        } catch (_) {
          return { ok: false, status: 0, error: "the dashboard did not answer" };
        }
        let body = {};
        try {
          body = await res.json();
        } catch (_) {
          body = { error: res.statusText || "unreadable response" };
        }
        if (res.status < 500) key = newKey();
        return { ok: res.ok, status: res.status, data: body.data, error: body.error, reason: body.reason };
      },
    };
  }
  return { form };
})();
