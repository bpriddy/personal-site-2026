// site-host.js: the host API for front ends, served by the user-content service
// at /site-host.js (docs/frontend-protocol.md, "Host API" and "Messages").
// A classic script: front ends load it with <script src="/site-host.js"> and
// use window.site. It runs in a sandboxed, opaque-origin iframe, so it never
// touches storage or cookies.
(function () {
  "use strict";

  if (window.site && window.site.__hostVersion === 1) return; // loaded twice

  // substituted with the main site's origin by the user-content service
  var MAIN_ORIGIN = "__MAIN_ORIGIN__";
  var KINDS = { error: true, unhandledrejection: true, "gpu-lost": true, report: true };

  var parentWin = window.parent;
  var routeListeners = [];
  var readySent = false;
  var initialized = false;
  var resolveLoaded;

  function post(msg) {
    msg.v = 1;
    if (!parentWin || parentWin === window) return; // not framed: nobody to talk to
    try {
      parentWin.postMessage(msg, MAIN_ORIGIN);
    } catch (e) {
      // e.g. an unserializable field; nothing useful to do from here
    }
  }

  function errorFields(err) {
    var message, stack;
    if (err && typeof err === "object") {
      message = err.message != null ? String(err.message) : safeString(err);
      if (err.stack != null) stack = String(err.stack);
    } else {
      message = safeString(err);
    }
    return { message: message, stack: stack };
  }

  function safeString(v) {
    try { return String(v); } catch (e) { return "(unprintable)"; }
  }

  function setRoute(route) {
    site.route = route;
    for (var i = 0; i < routeListeners.length; i++) {
      try {
        routeListeners[i](route);
      } catch (e) {
        site.reportError(e, "error");
      }
    }
  }

  var site = {
    __hostVersion: 1,
    content: null,
    route: null,
    loaded: null,

    onRoute: function (fn) {
      if (typeof fn !== "function") throw new TypeError("site.onRoute: fn must be a function");
      routeListeners.push(fn);
      return function unsubscribe() {
        var i = routeListeners.indexOf(fn);
        if (i >= 0) routeListeners.splice(i, 1);
      };
    },

    navigate: function (slug) {
      post({ type: "site:navigate", slug: slug == null ? "" : String(slug) });
    },

    ready: function () {
      if (readySent) return;
      readySent = true;
      post({ type: "site:ready" });
    },

    reportError: function (err, kind) {
      if (kind === undefined) kind = "report";
      if (!KINDS[kind]) kind = "report";
      var f = errorFields(err);
      var msg = { type: "site:error", kind: kind, message: f.message };
      if (f.stack !== undefined) msg.stack = f.stack;
      post(msg);
    },

    // textCanvas draws text (wrapped at maxWidth, "\n" for hard breaks) into
    // an OffscreenCanvas (or a 2D canvas where unavailable), sized to fit,
    // ready for GPUQueue.copyExternalImageToTexture.
    textCanvas: function (text, opts) {
      opts = opts || {};
      var font = opts.font || "32px system-ui, sans-serif";
      var color = opts.color || "#fff";
      var maxWidth = opts.maxWidth > 0 ? opts.maxWidth : 1024;
      var padding = opts.padding > 0 ? opts.padding : 0;
      var sizeMatch = /(\d+(?:\.\d+)?)px/.exec(font);
      var fontPx = sizeMatch ? parseFloat(sizeMatch[1]) : 32;
      var lineHeight = opts.lineHeight > 0 ? opts.lineHeight : Math.ceil(fontPx * 1.25);

      var canvas = makeCanvas(1, 1);
      var ctx = canvas.getContext("2d");
      ctx.font = font;

      var lines = [];
      var paragraphs = String(text == null ? "" : text).split("\n");
      for (var p = 0; p < paragraphs.length; p++) {
        var words = paragraphs[p].split(/\s+/).filter(Boolean);
        var line = "";
        for (var w = 0; w < words.length; w++) {
          var candidate = line ? line + " " + words[w] : words[w];
          if (line && ctx.measureText(candidate).width > maxWidth) {
            lines.push(line);
            line = words[w];
          } else {
            line = candidate;
          }
        }
        lines.push(line);
      }

      var width = 0;
      for (var i = 0; i < lines.length; i++) width = Math.max(width, ctx.measureText(lines[i]).width);
      canvas.width = Math.max(1, Math.ceil(Math.min(width, maxWidth) + padding * 2));
      canvas.height = Math.max(1, Math.ceil(lines.length * lineHeight + padding * 2));

      // resizing resets the context state
      ctx.font = font;
      ctx.fillStyle = color;
      ctx.textBaseline = "middle";
      for (var j = 0; j < lines.length; j++) {
        ctx.fillText(lines[j], padding, padding + j * lineHeight + lineHeight / 2, maxWidth);
      }
      return canvas;
    }
  };

  function makeCanvas(w, h) {
    if (typeof OffscreenCanvas === "function") {
      try { return new OffscreenCanvas(w, h); } catch (e) { /* fall through */ }
    }
    var c = document.createElement("canvas");
    c.width = w;
    c.height = h;
    return c;
  }

  site.loaded = new Promise(function (resolve) { resolveLoaded = resolve; });

  window.addEventListener("message", function (ev) {
    if (ev.source !== parentWin || ev.origin !== MAIN_ORIGIN) return;
    var m = ev.data;
    if (!m || typeof m !== "object" || m.v !== 1) return;
    if (m.type === "site:init") {
      site.content = m.content;
      site.route = typeof m.route === "string" ? m.route : "";
      if (!initialized) {
        initialized = true;
        resolveLoaded({ content: site.content, route: site.route });
      } else {
        setRoute(site.route);
      }
    } else if (m.type === "site:route" && typeof m.route === "string") {
      setRoute(m.route);
    }
  });

  window.addEventListener("error", function (ev) {
    var err = ev.error;
    var f = err ? errorFields(err) : { message: ev.message || "error" };
    var msg = { type: "site:error", kind: "error", message: f.message };
    if (f.stack !== undefined) msg.stack = f.stack;
    post(msg);
  });

  window.addEventListener("unhandledrejection", function (ev) {
    site.reportError(ev.reason, "unhandledrejection");
  });

  window.site = site;
  post({ type: "site:hello" });
})();
