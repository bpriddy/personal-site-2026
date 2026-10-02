// frontend-host.js: the parent-page side of the front-end protocol
// (docs/frontend-protocol.md, "Parent page behavior").
//
// Loads the visitor's front end in a sandboxed iframe from the user-content
// origin, speaks postMessage with it, falls back to the default front end and
// then to the plain HTML transcript on failure, and owns the URL/history.
//
// It also exposes window.siteHost, for the public builder modal
// (build-modal.js), which shows a visitor's draft by changing the fe_live
// cookie and then reloading the front end in place (no page reload):
//   siteHost.reload()     load a fresh /api/frontend into the iframe
//   siteHost.exitDraft()  stop showing the visitor's draft (POST exit, reload)
//   siteHost.current()    {ref, serve, draft, revision, number} of the last load
// and fires "sitehost:load" (detail: the same object) on window whenever a
// front end is chosen (not for the fallback).
//
// Routes are slugs derived from the path: leading and trailing slashes are
// trimmed, so "/" → "", "/about" → "about", "/experiments/" → "experiments",
// "/experiments/foo" → "experiments/foo". A navigate slug maps back to
// "/" + slug. Slugs must be lowercase [a-z0-9-] segments joined by "/", and
// may not start with a server-reserved segment (admin, api, static, health).
(function () {
  "use strict";

  var DEFAULT_REF = "builtin/site";
  var READY_TIMEOUT_MS = 10000;
  // Longest the page stays blank waiting for a front end. After this the
  // transcript shows while the front end keeps loading behind it, and the front
  // end takes over when it reports ready. Short enough that a slow or broken
  // front end never costs more than a moment; long enough that a healthy one
  // appears without a flash of the HTML page first.
  var REVEAL_AFTER_MS = 1500;
  var SLUG_RE = /^[a-z0-9][a-z0-9-]*(\/[a-z0-9][a-z0-9-]*)*$/;
  var RESERVED = { admin: true, api: true, "static": true, health: true, build: true };

  var root = document.documentElement;
  // ?shuffle asks for a fresh random front end (handy for cycling the
  // rotation); read once, then removed from the address bar
  var shuffle = /[?&]shuffle(=|&|$)/.test(location.search);
  if (shuffle) {
    try {
      var u = new URL(location.href);
      u.searchParams.delete("shuffle");
      history.replaceState(history.state, "", u.pathname + u.search + u.hash);
    } catch (e) { /* keep the URL */ }
  }
  var iframe = null; // the current front end's iframe
  var attempt = 0; // bumps on every load, so stale callbacks are ignored
  var currentRef = null;
  var ready = false;
  var readyTimer = 0;
  var contentPromise = null;
  var navSeq = 0;
  var revealed = false; // the transcript is showing while a front end loads
  var currentServe = ""; // the servable ref (/api/frontend "serve"), if any
  var draft = false; // View live: the visitor's own draft, never reported to the observer
  var currentInfo = { ref: "", serve: "", draft: false, revision: "", number: 0 };
  var revealTimer = 0;

  // ── observer reports (docs/frontend-protocol.md, "Observer ingestion") ──
  var OBSERVE_URL = "/api/observe";
  var MAX_REPORTS = 20; // per page load
  var reportsSent = 0;
  var reportSigs = {};
  var FIELD_MAX = 500; // chars per field, keeping a report well under 8 KB
  var STACK_MAX = 3000;

  function clip(v, max) {
    var s = v == null ? "" : typeof v === "string" ? v : String(v);
    return s.length > max ? s.slice(0, max) : s;
  }

  // observe sends one report to the observer, fire-and-forget. It never
  // throws and never affects the front end or the page.
  function observe(r) {
    if (draft) return; // a visitor's private draft isn't part of the site
    try {
      var body = {
        kind: r.kind,
        frontend: clip(r.frontend != null ? r.frontend : currentRef || "", FIELD_MAX),
        serve: clip(r.serve != null ? r.serve : currentServe, FIELD_MAX),
        route: clip(currentRoute(), FIELD_MAX),
        collection: clip(r.collection, FIELD_MAX),
        item: clip(r.item, FIELD_MAX),
        field: clip(r.field, FIELD_MAX),
        expect: clip(r.expect, FIELD_MAX),
        got: clip(r.got, FIELD_MAX),
        message: clip(r.message, FIELD_MAX * 2),
        stack: clip(r.stack, STACK_MAX)
      };
      var sig = [body.kind, body.frontend, body.route, body.collection, body.item, body.field, body.got, body.message].join("\u0000");
      if (reportSigs[sig] || reportsSent >= MAX_REPORTS) return;
      reportSigs[sig] = true;
      reportsSent++;
      var json = JSON.stringify(body);
      if (navigator.sendBeacon) {
        var sent = false;
        try { sent = navigator.sendBeacon(OBSERVE_URL, new Blob([json], { type: "application/json" })); } catch (e) { sent = false; }
        if (!sent) {
          try { sent = navigator.sendBeacon(OBSERVE_URL, json); } catch (e) { sent = false; } // text/plain
        }
        if (sent) return;
      }
      if (window.fetch) {
        fetch(OBSERVE_URL, {
          method: "POST", body: json, keepalive: true, credentials: "same-origin",
          headers: { "Content-Type": "application/json" }
        }).catch(function () { /* fire-and-forget */ });
      }
    } catch (e) {
      // reporting must never break the page
    }
  }

  function routeFromPath(pathname) {
    var p = pathname;
    try { p = decodeURIComponent(pathname); } catch (e) { /* keep raw */ }
    return p.replace(/^\/+|\/+$/g, "");
  }

  function currentRoute() { return routeFromPath(location.pathname); }

  function validSlug(slug) {
    if (slug === "") return true;
    if (typeof slug !== "string" || !SLUG_RE.test(slug)) return false;
    return !RESERVED[slug.split("/")[0]];
  }

  function post(msg) {
    msg.v = 1;
    // the iframe's origin is opaque, so "*" is the only target that works;
    // everything posted is public content
    if (iframe && iframe.contentWindow) iframe.contentWindow.postMessage(msg, "*");
  }

  function getContent() {
    if (!contentPromise) {
      contentPromise = fetch("/api/site.json", { credentials: "same-origin" }).then(function (r) {
        if (!r.ok) throw new Error("site.json: HTTP " + r.status);
        return r.json();
      });
      contentPromise.catch(function () { contentPromise = null; });
    }
    return contentPromise;
  }

  function setMode(mode) {
    root.classList.remove("fe-loading", "fe-live");
    if (mode) root.classList.add(mode);
  }

  function removeIframe() {
    clearTimeout(readyTimer);
    if (iframe && iframe.parentNode) iframe.parentNode.removeChild(iframe);
    iframe = null;
  }

  // load fetches a front end (the visit's pick, or the default when fallback)
  // and embeds it.
  function load(fallback) {
    var my = ++attempt;
    removeIframe();
    ready = false;
    currentRef = fallback ? DEFAULT_REF : null;
    currentServe = "";
    if (!revealed) setMode("fe-loading");
    readyTimer = setTimeout(function () { fail(my, "no site:ready within " + READY_TIMEOUT_MS + "ms"); }, READY_TIMEOUT_MS);

    var reroll = shuffle && !fallback;
    shuffle = false; // once per page load: later reloads keep the new pick
    fetch("/api/frontend" + (fallback ? "?fallback=1" : (reroll ? "?shuffle=1" : "")), { credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        if (!r.ok) throw new Error("api/frontend: HTTP " + r.status);
        return r.json();
      })
      .then(function (fe) {
        if (my !== attempt) return;
        if (!fe || typeof fe.url !== "string" || typeof fe.ref !== "string") throw new Error("api/frontend: bad response");
        currentRef = fe.ref;
        currentServe = typeof fe.serve === "string" ? fe.serve : "";
        if (!fallback) {
          // a fallback keeps the draft state: it's still the visitor's draft that failed
          draft = fe.draft === true && typeof fe.exit === "string" && /^\/build\/api\/[a-z]+$/.test(fe.exit);
          currentInfo = {
            ref: currentRef, serve: currentServe, draft: draft,
            revision: draft && typeof fe.revision === "string" ? fe.revision : "",
            number: draft && typeof fe.number === "number" ? fe.number : 0
          };
          if (draft) showDraftBanner(fe.exit, typeof fe.title === "string" ? fe.title : "", currentInfo.number);
          else hideDraftBanner();
        }
        var f = document.createElement("iframe");
        f.id = "frontend";
        f.setAttribute("sandbox", "allow-scripts");
        f.setAttribute("allow", "fullscreen");
        f.setAttribute("referrerpolicy", "no-referrer");
        f.setAttribute("title", "Site");
        f.setAttribute("aria-hidden", "true"); // the transcript is the accessible content
        f.src = fe.url;
        iframe = f;
        document.body.appendChild(f);
        if (!fallback) {
          try {
            window.dispatchEvent(new CustomEvent("sitehost:load", { detail: copyInfo() }));
          } catch (e) { /* listeners never break loading */ }
        }
      })
      .catch(function (err) { fail(my, err && err.message ? err.message : String(err)); });
  }

  // fail handles a front end's failure. reported: the cause was already sent
  // to the observer (a forwarded site:error), so don't report it twice.
  function fail(my, reason, reported) {
    if (my !== attempt) return;
    if (window.console) console.warn("front end " + (currentRef || "?") + " failed: " + reason);
    if (!reported) {
      observe({
        kind: "frontend-error",
        message: (currentRef !== DEFAULT_REF ? "falling back to " + DEFAULT_REF : "falling back to the transcript") + ": " + reason
      });
    }
    ready = false;
    reveal(); // don't leave the page blank (or on a dead front end) while retrying
    if (currentRef !== DEFAULT_REF) {
      load(true);
      return;
    }
    // the default failed too: the transcript is the site
    attempt++;
    removeIframe();
    setMode(null);
  }

  function reveal() {
    if (ready || revealed) return;
    revealed = true;
    setMode(null);
  }


  // openExternal handles site:open (v1.4): the sandboxed front end can't open
  // tabs itself, so the parent opens http(s) URLs, and nothing else, in a new
  // tab without an opener or referrer. At most one per second; the browser's
  // popup blocker still requires a recent click in the front end.
  var lastOpen = 0;
  function openExternal(url) {
    if (typeof url !== "string" || url.length > 2000) return false;
    var u;
    try { u = new URL(url); } catch (e) { return false; }
    if (u.protocol !== "http:" && u.protocol !== "https:") return false;
    var now = Date.now();
    if (now - lastOpen < 1000) return false;
    lastOpen = now;
    try { window.open(u.href, "_blank", "noopener,noreferrer"); } catch (e) { return false; }
    return true;
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
          post({ type: "site:init", content: content, route: currentRoute() });
        }, function (err) { fail(my, err && err.message ? err.message : String(err)); });
        break;
      case "site:ready":
        if (ready) break;
        ready = true;
        revealed = false;
        clearTimeout(readyTimer);
        setMode("fe-live");
        break;
      case "site:navigate":
        navigate(m.slug, true);
        break;
      case "site:open":
        openExternal(m.url);
        break;
      case "site:error":
        if (window.console) console.warn("front end error (" + m.kind + "): " + m.message);
        observe({
          kind: "frontend-error",
          message: clip(m.kind, 40) + ": " + clip(m.message, FIELD_MAX * 2),
          stack: m.stack
        });
        if (!ready || m.kind === "gpu-lost") fail(my, "site:error " + m.kind + ": " + m.message, true);
        break;
      case "site:gap":
        // a content gap or type break the front end hit through site.field
        var got = typeof m.got === "string" ? m.got : "missing";
        observe({
          kind: got === "missing" || got === "empty" ? "content-gap" : "type-break",
          collection: m.collection, item: m.item, field: m.field, expect: m.expect, got: got
        });
        break;
    }
  });

  // navigate shows the page for slug: updates history (when push), fetches the
  // server's page, swaps in its transcript <main> and <title>, and tells the
  // front end.
  function navigate(slug, push) {
    if (!validSlug(slug)) {
      if (window.console) console.warn("ignoring navigation to an invalid slug:", slug);
      return false;
    }
    var path = "/" + slug;
    if (push) {
      if (currentRoute() === slug) return true; // already there
      history.pushState(null, "", path);
    }
    var seq = ++navSeq;
    fetch(path, { credentials: "same-origin", headers: { Accept: "text/html" } })
      .then(function (r) { return r.text(); }) // a 404 still carries the 404 transcript
      .then(function (html) {
        if (seq !== navSeq) return;
        var doc = new DOMParser().parseFromString(html, "text/html");
        var next = doc.querySelector("#transcript main");
        var cur = document.querySelector("#transcript main");
        if (!next || !cur) throw new Error("no transcript in " + path);
        cur.replaceWith(document.importNode(next, true));
        var nextNav = doc.querySelector("#transcript .site-header nav");
        var curNav = document.querySelector("#transcript .site-header nav");
        if (nextNav && curNav) curNav.replaceWith(document.importNode(nextNav, true));
        document.title = doc.title;
        post({ type: "site:route", route: slug });
      })
      .catch(function (err) {
        if (seq !== navSeq) return;
        if (window.console) console.warn("navigation failed, reloading:", err);
        location.reload();
      });
    return true;
  }

  window.addEventListener("popstate", function () {
    // a path we wouldn't navigate to ourselves: let the server render it
    if (!navigate(currentRoute(), false)) location.reload();
  });

  // ── parent-drawn controls (docs/frontend-protocol.md, v1.2) ──
  // They sit above the front-end iframe, so every front end gets them.

  // "Make your own version of this site": a small fixed button that opens the
  // builder modal (build-modal.js). Styled in site.css (docs/design-pov.md,
  // 5.4): an accent dot, the label (twice, for the hover roll; shorter on
  // phones), and the keyboard shortcut, which works on the parent page.
  var MAC = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent || "");
  function span(cls, text) {
    var e = document.createElement("span");
    e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function drawMakeOwn() {
    var a = document.createElement("button");
    a.type = "button";
    a.id = "make-own";
    a.className = "make-own";
    a.setAttribute("aria-haspopup", "dialog");
    a.setAttribute("aria-label", "Make your own version of this site");
    a.setAttribute("aria-keyshortcuts", MAC ? "Meta+K" : "Control+K");
    a.addEventListener("click", function () { openBuilder(a); });
    var dot = span("make-own-dot");
    dot.setAttribute("aria-hidden", "true");
    var roll = span("make-own-roll");
    roll.setAttribute("aria-hidden", "true");
    [0, 1].forEach(function () {
      var line = span("make-own-line");
      line.append(span("make-own-long", "Make your own version"), span("make-own-short", "Make your own"));
      roll.append(line);
    });
    var kbd = span("make-own-kbd", MAC ? "\u2318K" : "Ctrl K");
    kbd.setAttribute("aria-hidden", "true");
    a.append(dot, roll, kbd);
    document.body.appendChild(a);
    root.classList.add("has-cta");
    // Cmd/Ctrl+K opens the builder from anywhere on the parent page
    document.addEventListener("keydown", function (e) {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && (e.key === "k" || e.key === "K")) {
        if (root.classList.contains("bm-open")) return; // the modal handles its own keys
        e.preventDefault();
        openBuilder(a);
      }
    });
  }

  function openBuilder(opener) {
    if (window.buildModal && typeof window.buildModal.open === "function") window.buildModal.open(opener);
    else location.assign("/?build=1");
  }

  // View live: a tape across the top (docs/design-pov.md, 5.6): "Your draft —
  // only you can see this", which draft and version, "Back to studio" and
  // "Exit draft". Exit clears fe_live and reloads the front end in place,
  // back to the visit's pick.
  var banner = null;
  function fitBanner() {
    if (banner) root.style.setProperty("--draft-h", banner.offsetHeight + "px");
  }
  function showDraftBanner(exit, title, number) {
    if (!banner) {
      banner = document.createElement("div");
      banner.id = "draft-banner";
      banner.className = "draft-banner";
      banner.setAttribute("role", "status");
      var dot = span("draft-banner-dot");
      dot.setAttribute("aria-hidden", "true");
      var text = span("draft-banner-text");
      text.append(span("draft-banner-long", "Your draft \u2014 only you can see this"), span("draft-banner-short", "Your draft"));
      var which = span("draft-banner-which");
      var actions = span("draft-banner-actions");
      var more = document.createElement("button");
      more.type = "button";
      more.className = "draft-banner-build";
      more.append(span("draft-banner-long", "Back to studio"), span("draft-banner-short", "Studio"));
      more.addEventListener("click", function () { openBuilder(more); });
      var btn = document.createElement("button");
      btn.type = "button";
      btn.className = "draft-banner-exit";
      btn.append(span("draft-banner-long", "Exit draft"), span("draft-banner-short", "Exit"));
      btn.addEventListener("click", function () {
        btn.disabled = true;
        exitDraft().then(function () { btn.disabled = false; });
      });
      actions.append(more, btn);
      banner.append(dot, text, which, actions);
      document.body.appendChild(banner);
      window.addEventListener("resize", fitBanner);
    }
    banner.setAttribute("data-exit", exit);
    banner.querySelector(".draft-banner-which").textContent =
      (title ? "\u201c" + title + "\u201d" + (number ? ", " : "") : "") + (number ? "version " + number : "");
    root.classList.add("fe-draft");
    // the front end starts below the banner, so the banner never covers it
    fitBanner();
  }
  function hideDraftBanner() {
    if (!banner) return;
    window.removeEventListener("resize", fitBanner);
    banner.remove();
    banner = null;
    root.classList.remove("fe-draft");
    root.style.removeProperty("--draft-h");
  }

  // exitDraft stops showing the visitor's draft and loads the visit's pick.
  function exitDraft() {
    var exit = (banner && banner.getAttribute("data-exit")) || "/build/api/exit";
    return fetch(exit, { method: "POST", credentials: "same-origin" })
      .catch(function () { /* reload anyway: the server decides what to show */ })
      .then(function () { reloadFrontend(); });
  }

  // reloadFrontend loads a fresh /api/frontend into the iframe, keeping the
  // page (route, transcript, history) as it is.
  function reloadFrontend() {
    revealed = false;
    clearTimeout(revealTimer);
    revealTimer = setTimeout(reveal, REVEAL_AFTER_MS);
    load(false);
  }

  function copyInfo() {
    return { ref: currentInfo.ref, serve: currentInfo.serve, draft: currentInfo.draft, revision: currentInfo.revision, number: currentInfo.number };
  }

  window.siteHost = { reload: reloadFrontend, exitDraft: exitDraft, current: copyInfo };

  drawMakeOwn();
  revealTimer = setTimeout(reveal, REVEAL_AFTER_MS);
  load(false);
})();
