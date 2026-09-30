// frontend-host.js: the parent-page side of the front-end protocol
// (docs/frontend-protocol.md, "Parent page behavior").
//
// Loads the visitor's front end in a sandboxed iframe from the user-content
// origin, speaks postMessage with it, falls back to the default front end and
// then to the plain HTML transcript on failure, and owns the URL/history.
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
  var SLUG_RE = /^[a-z0-9][a-z0-9-]*(\/[a-z0-9][a-z0-9-]*)*$/;
  var RESERVED = { admin: true, api: true, "static": true, health: true };

  var root = document.documentElement;
  var iframe = null; // the current front end's iframe
  var attempt = 0; // bumps on every load, so stale callbacks are ignored
  var currentRef = null;
  var ready = false;
  var readyTimer = 0;
  var contentPromise = null;
  var navSeq = 0;

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
    setMode("fe-loading");
    readyTimer = setTimeout(function () { fail(my, "no site:ready within " + READY_TIMEOUT_MS + "ms"); }, READY_TIMEOUT_MS);

    fetch("/api/frontend" + (fallback ? "?fallback=1" : ""), { credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        if (!r.ok) throw new Error("api/frontend: HTTP " + r.status);
        return r.json();
      })
      .then(function (fe) {
        if (my !== attempt) return;
        if (!fe || typeof fe.url !== "string" || typeof fe.ref !== "string") throw new Error("api/frontend: bad response");
        currentRef = fe.ref;
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
      })
      .catch(function (err) { fail(my, err && err.message ? err.message : String(err)); });
  }

  function fail(my, reason) {
    if (my !== attempt) return;
    if (window.console) console.warn("front end " + (currentRef || "?") + " failed: " + reason);
    if (currentRef !== DEFAULT_REF) {
      load(true);
      return;
    }
    // the default failed too: the transcript is the site
    attempt++;
    removeIframe();
    setMode(null);
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
        clearTimeout(readyTimer);
        setMode("fe-live");
        break;
      case "site:navigate":
        navigate(m.slug, true);
        break;
      case "site:error":
        if (window.console) console.warn("front end error (" + m.kind + "): " + m.message);
        if (!ready || m.kind === "gpu-lost") fail(my, "site:error " + m.kind + ": " + m.message);
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

  load(false);
})();
