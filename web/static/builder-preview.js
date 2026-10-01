// builder-preview.js: the builder preview host, shared by the admin builder
// and the public one (/build). It embeds one servable ref
// (a revision "rev/<id>" or a built-in) in the same sandboxed iframe the
// public site uses and speaks the host protocol with it
// (docs/frontend-protocol.md), but instead of falling back it reports what
// happens: ready, errors and content gaps are shown in #preview-log, and
// navigation changes the preview's route without touching the admin URL.
// Content gaps are also forwarded to the observer (POST /api/observe), like
// the public parent page does.
//
// #preview attributes: data-ref (required), data-frontend, data-preview-url
// (where to mint the iframe URL; default the admin's), data-observe="0" (don't
// forward gaps: visitor drafts aren't on the site), data-friendly="1" (plain
// words on the state line, for visitors).
(function () {
  "use strict";

  var box = document.getElementById("preview");
  if (!box) return;
  var ref = box.getAttribute("data-ref");
  var frontendID = box.getAttribute("data-frontend") || ref;
  var logEl = document.getElementById("preview-log");
  var stateEl = document.getElementById("preview-state");
  var routeEl = document.getElementById("preview-route");
  var READY_TIMEOUT_MS = 10000;
  var previewURL = box.getAttribute("data-preview-url") || "/admin/builder/preview";
  var observeGaps = box.getAttribute("data-observe") !== "0";
  var friendly = box.getAttribute("data-friendly") === "1";
  var FRIENDLY = {
    loading: "Loading…",
    ready: "Ready",
    timeout: "It didn't start in time. Try Reload, or ask Claude to fix it.",
    error: "Something went wrong while it started. Ask Claude to fix it."
  };

  var iframe = null;
  var route = "";
  var ready = false;
  var timer = 0;
  var attempt = 0;
  var contentPromise = null;
  var gapsSent = {};

  function setState(s, text) {
    box.setAttribute("data-state", s);
    if (stateEl) stateEl.textContent = (friendly && FRIENDLY[s]) || text || s;
  }

  function log(kind, text) {
    if (!logEl) return;
    var li = document.createElement("li");
    li.className = "b-log-" + kind;
    li.textContent = new Date().toLocaleTimeString() + "  " + text;
    logEl.prepend(li);
    while (logEl.children.length > 50) logEl.removeChild(logEl.lastChild);
  }

  function post(msg) {
    msg.v = 1;
    // opaque-origin iframe: "*" is the only target that works; content is public
    if (iframe && iframe.contentWindow) iframe.contentWindow.postMessage(msg, "*");
  }

  function getContent() {
    if (!contentPromise) {
      contentPromise = fetch("/api/site.json", { credentials: "same-origin", cache: "no-store" }).then(function (r) {
        if (!r.ok) throw new Error("site.json: HTTP " + r.status);
        return r.json();
      });
      contentPromise.catch(function () { contentPromise = null; });
    }
    return contentPromise;
  }

  function showRoute() {
    if (routeEl) routeEl.textContent = "/" + route;
  }

  function load() {
    var my = ++attempt;
    ready = false;
    clearTimeout(timer);
    if (iframe && iframe.parentNode) iframe.parentNode.removeChild(iframe);
    iframe = null;
    contentPromise = null; // fresh content on every reload
    setState("loading", "loading…");
    timer = setTimeout(function () {
      if (my !== attempt || ready) return;
      setState("timeout", "no site:ready within 10s (the public site would fall back)");
      log("error", "no site:ready within " + READY_TIMEOUT_MS / 1000 + "s");
    }, READY_TIMEOUT_MS);
    fetch(previewURL + "?ref=" + encodeURIComponent(ref), { credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        if (!r.ok) throw new Error("preview URL: HTTP " + r.status);
        return r.json();
      })
      .then(function (p) {
        if (my !== attempt) return;
        var f = document.createElement("iframe");
        f.setAttribute("sandbox", "allow-scripts");
        f.setAttribute("allow", "fullscreen");
        f.setAttribute("referrerpolicy", "no-referrer");
        f.setAttribute("title", "Preview of " + ref);
        f.src = p.url;
        iframe = f;
        box.appendChild(f);
      })
      .catch(function (err) {
        setState("error", String(err && err.message || err));
        log("error", String(err && err.message || err));
      });
  }

  function observeGap(m) {
    if (!observeGaps) return;
    try {
      var got = typeof m.got === "string" ? m.got : "missing";
      var body = {
        kind: got === "missing" || got === "empty" ? "content-gap" : "type-break",
        frontend: frontendID, serve: ref, route: route,
        collection: String(m.collection || ""), item: String(m.item || ""), field: String(m.field || ""),
        expect: String(m.expect || ""), got: got, message: "", stack: ""
      };
      var sig = [body.collection, body.item, body.field, body.got].join("\u0000");
      if (gapsSent[sig]) return;
      gapsSent[sig] = true;
      var json = JSON.stringify(body);
      if (!(navigator.sendBeacon && navigator.sendBeacon("/api/observe", new Blob([json], { type: "application/json" })))) {
        fetch("/api/observe", { method: "POST", body: json, headers: { "Content-Type": "application/json" }, credentials: "same-origin" })
          .catch(function () {});
      }
    } catch (e) { /* reporting never breaks the preview */ }
  }

  window.addEventListener("message", function (ev) {
    if (!iframe || ev.source !== iframe.contentWindow) return;
    var m = ev.data;
    if (!m || typeof m !== "object" || m.v !== 1) return;
    var my = attempt;
    switch (m.type) {
      case "site:hello":
        getContent().then(function (content) {
          if (my !== attempt) return;
          post({ type: "site:init", content: content, route: route });
        }, function (err) { log("error", String(err)); });
        break;
      case "site:ready":
        if (ready) break;
        ready = true;
        clearTimeout(timer);
        setState("ready", "ready");
        log("ok", "site:ready");
        break;
      case "site:navigate":
        var slug = typeof m.slug === "string" ? m.slug.replace(/^\/+|\/+$/g, "") : "";
        route = slug;
        showRoute();
        log("info", "navigate → /" + slug);
        post({ type: "site:route", route: slug });
        break;
      case "site:error":
        log("error", "site:error (" + m.kind + "): " + m.message + (ready ? "" : "  [before ready: the public site would fall back]"));
        if (!ready) setState("error", "error before ready");
        break;
      case "site:gap":
        log("gap", "content gap: " + m.collection + "/" + (m.item || "(home)") + " ." + m.field + " expected " + m.expect + ", got " + m.got);
        observeGap(m);
        break;
    }
  });

  var home = document.getElementById("preview-home");
  if (home) home.addEventListener("click", function () {
    route = "";
    showRoute();
    post({ type: "site:route", route: "" });
  });
  var reload = document.getElementById("preview-reload");
  if (reload) reload.addEventListener("click", load);

  showRoute();
  load();
})();
