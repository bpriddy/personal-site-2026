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

  // ── content contract (docs/content-contract.json) ──────────────────────────
  // The second net under the server's normalization: whatever arrives (an old
  // or newer server, a hand-edited payload), front ends see every declared
  // field as a string, every collection as an array of objects, and items
  // tagged with _collection so site.field can report gaps against them.

  // declared text fields per collection; the first is the item key (slug)
  var DECLARED = {
    pages: ["slug", "title", "body"],
    experiments: ["slug", "title", "summary", "link"],
    projects: ["slug", "title", "client", "agency", "year", "summary", "contribution", "body", "link", "youtube"]
  };
  // list-of-text fields (v1.4): always arrays of strings
  var LISTS = { projects: ["tags", "roles", "palette"] };
  // media lists (v1.4): always arrays of {kind, src, poster, width, height, alt}
  var MEDIA = { experiments: ["media"], projects: ["media"] };
  // a media path on this origin; never another host (the CSP would block it,
  // and front ends must not hotlink)
  var MEDIA_PATH = /^\/media\/[A-Za-z0-9][A-Za-z0-9._\/-]*$/;
  var EXPECT_ALIASES = { text: "text", string: "text", list: "list", array: "list", number: "number", bool: "bool", "boolean": "bool" };
  var MAX_GAP_REPORTS = 50; // per page load, after deduplication
  var gapsSent = {};
  var gapCount = 0;
  var hasOwn = Object.prototype.hasOwnProperty;
  // field → original type name, for declared fields that normalization had to
  // default (so site.field reports the type break, not just "empty")
  var coercedTypes = typeof WeakMap === "function" ? new WeakMap() : null;

  function isObject(v) { return v !== null && typeof v === "object" && !Array.isArray(v); }

  function own(obj, key) {
    try {
      return obj != null && hasOwn.call(obj, key) ? obj[key] : undefined;
    } catch (e) {
      return undefined; // a throwing getter
    }
  }

  // typeName: the `got` of a site:gap. "missing" and "empty" are gaps; any
  // other name is a type break.
  function typeName(v) {
    if (v === undefined || v === null) return "missing";
    if (Array.isArray(v)) return "array";
    if (typeof v === "boolean") return "boolean";
    return typeof v; // string, number, object, function, bigint, symbol
  }

  // copyObject: a shallow copy of an object's own enumerable fields.
  function copyObject(src) {
    var out = {};
    var keys = [];
    try { keys = Object.keys(src); } catch (e) { /* exotic object */ }
    for (var i = 0; i < keys.length; i++) {
      var k = keys[i];
      if (k === "__proto__") continue;
      var v = own(src, k);
      if (v !== undefined) out[k] = v;
    }
    return out;
  }

  function normalizeItem(collection, raw) {
    var item = copyObject(raw);
    var fields = DECLARED[collection];
    var coerced = null;
    for (var i = 0; i < fields.length; i++) {
      var f = fields[i];
      var v = item[f];
      if (typeof v === "string") continue;
      if ((typeof v === "number" && isFinite(v)) || typeof v === "boolean") {
        item[f] = String(v); // lossless
        continue;
      }
      if (v !== undefined && v !== null) {
        coerced = coerced || {};
        coerced[f] = typeName(v);
      }
      item[f] = "";
    }
    var lists = LISTS[collection] || [];
    for (var l = 0; l < lists.length; l++) {
      var lv = item[lists[l]];
      var out = [];
      if (Array.isArray(lv)) {
        for (var li = 0; li < lv.length; li++) {
          var s = own(lv, li);
          if (typeof s === "string" && s.trim() !== "") out.push(s);
        }
      } else if (lv !== undefined && lv !== null) {
        coerced = coerced || {};
        coerced[lists[l]] = typeName(lv);
      }
      item[lists[l]] = out;
    }
    var media = MEDIA[collection] || [];
    for (var m = 0; m < media.length; m++) {
      var mv = item[media[m]];
      if (mv !== undefined && mv !== null && !Array.isArray(mv)) {
        coerced = coerced || {};
        coerced[media[m]] = typeName(mv);
      }
      item[media[m]] = normalizeMedia(mv);
    }
    var gen = item._generated;
    var list = [];
    if (Array.isArray(gen)) {
      for (var j = 0; j < gen.length; j++) if (typeof gen[j] === "string") list.push(gen[j]);
    }
    item._generated = list;
    item._collection = collection;
    if (coerced && coercedTypes) coercedTypes.set(item, coerced);
    return item;
  }

  function mediaPath(v) {
    return typeof v === "string" && v.length <= 300 && MEDIA_PATH.test(v) && v.indexOf("..") < 0 && v.indexOf("//") < 0;
  }

  function dimension(v) {
    return typeof v === "number" && isFinite(v) && v > 0 ? Math.round(v) : 0;
  }

  // normalizeMedia: only items a front end can show safely: an object with a
  // kind string and a same-origin /media/ src; every key present.
  function normalizeMedia(list) {
    var out = [];
    if (!Array.isArray(list)) return out;
    for (var i = 0; i < list.length; i++) {
      var raw = own(list, i);
      if (!isObject(raw)) continue;
      var kind = own(raw, "kind");
      var src = own(raw, "src");
      if (typeof kind !== "string" || kind === "" || !mediaPath(src)) continue;
      var poster = own(raw, "poster");
      var alt = own(raw, "alt");
      out.push({
        kind: kind,
        src: src,
        poster: mediaPath(poster) ? poster : "",
        width: dimension(own(raw, "width")),
        height: dimension(own(raw, "height")),
        alt: typeof alt === "string" ? alt : ""
      });
    }
    return out;
  }

  // safeURL: an absolute http(s) URL as a string, or "".
  function safeURL(v) {
    if (typeof v !== "string" || v.length > 2000) return "";
    try {
      var u = new URL(v);
      if (u.protocol !== "http:" && u.protocol !== "https:") return "";
      return u.href;
    } catch (e) {
      return "";
    }
  }

  // normalize: the contract shape from any value. Unknown top-level keys and
  // unknown item fields pass through untouched.
  function normalize(raw) {
    var c = isObject(raw) ? copyObject(raw) : {};
    if (typeof c.contractVersion !== "number") c.contractVersion = 1;
    for (var coll in DECLARED) {
      if (!hasOwn.call(DECLARED, coll)) continue;
      var src = Array.isArray(c[coll]) ? c[coll] : [];
      var out = [];
      for (var i = 0; i < src.length; i++) {
        var it = own(src, i);
        if (isObject(it)) out.push(normalizeItem(coll, it));
      }
      c[coll] = out;
    }
    return c;
  }

  function reportGap(item, name, expect, got) {
    var collection = own(item, "_collection");
    if (typeof collection !== "string" || collection === "") return; // not a content item: nothing to heal
    var slug = own(item, "slug");
    slug = typeof slug === "string" ? slug : slug == null ? "" : safeString(slug);
    var key = collection + "\u0000" + slug + "\u0000" + name;
    if (gapsSent[key] || gapCount >= MAX_GAP_REPORTS) return;
    gapsSent[key] = true;
    gapCount++;
    post({ type: "site:gap", collection: collection, item: slug, field: name, expect: expect, got: got });
  }

  function defaultFallback(expect) {
    switch (expect) {
      case "list": return [];
      case "number": return 0;
      case "bool": return false;
      default: return "";
    }
  }

  // check: [value to return, got] where got is null when v matches expect.
  // Lossless coercions return the coerced value but still report the break.
  function check(v, expect) {
    var t = typeName(v);
    switch (expect) {
      case "list":
        return Array.isArray(v) ? [v, null] : [undefined, t];
      case "number":
        if (typeof v === "number" && isFinite(v)) return [v, null];
        if (typeof v === "string") {
          if (v.trim() === "") return [undefined, "empty"];
          var n = Number(v);
          if (isFinite(n)) return [n, "string"];
        }
        return [undefined, t];
      case "bool":
        if (typeof v === "boolean") return [v, null];
        if (v === "true" || v === "false") return [v === "true", "string"];
        return [undefined, t];
      default: // text
        if (typeof v === "string") return v.trim() !== "" ? [v, null] : [undefined, "empty"];
        if ((typeof v === "number" && isFinite(v)) || typeof v === "boolean") return [String(v), t];
        return [undefined, t];
    }
  }

  // ── route transitions (v1.6) ──
  // Every front end gets them without doing anything: on a route change the
  // content on screen animates out (a short stagger, top to bottom), the front
  // end renders the new route (its onRoute listeners), and the new content
  // animates in. About 0.8-1.2s in all. The exit starts at site.navigate, so
  // it runs while the parent fetches the page. Web Animations only (no CSS
  // is injected; transforms compose with the front end's own). A front end
  // with its own transition turns this off with site.transitions(false).
  var transitionsOn = true;
  var reduceMQ = window.matchMedia ? window.matchMedia("(prefers-reduced-motion: reduce)") : null;
  var BLOCKS = "h1,h2,h3,h4,h5,h6,p,li,dt,dd,figure,img,video,picture,blockquote,pre,table,button,a,label,canvas,svg,hr,input,textarea";
  var MAX_BLOCKS = 48;
  var exiting = null; // {done: Promise, anims: Animation[], timer}
  function canAnimate() {
    return transitionsOn && document.body && typeof document.body.animate === "function";
  }
  function calm() { return !!(reduceMQ && reduceMQ.matches); }
  // the outermost content blocks currently on screen, top to bottom
  function visibleBlocks() {
    var vw = window.innerWidth, vh = window.innerHeight, out = [];
    var all = document.body.querySelectorAll(BLOCKS);
    for (var i = 0; i < all.length && out.length < MAX_BLOCKS; i++) {
      var el = all[i], inside = false;
      for (var j = out.length - 1; j >= 0; j--) if (out[j].contains(el)) { inside = true; break; }
      if (inside) continue;
      var r = el.getBoundingClientRect();
      if (r.width < 2 || r.height < 2 || r.bottom <= 0 || r.top >= vh || r.right <= 0 || r.left >= vw) continue;
      var cs = window.getComputedStyle(el);
      if (cs.visibility === "hidden" || cs.display === "none" || parseFloat(cs.opacity) === 0) continue;
      out.push(el);
    }
    out.sort(function (a, b) {
      var ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect();
      return (ra.top - rb.top) || (ra.left - rb.left);
    });
    return out;
  }
  // play runs a fade (replacing opacity) and, unless still, a drift (added to
  // the element's own transform); returns the animations
  function play(el, o0, o1, y0, y1, opts) {
    var out = [];
    try { out.push(el.animate([{ opacity: o0 }, { opacity: o1 }], opts)); } catch (e) { /* not animatable */ }
    if (y0 !== y1) {
      var t = {}; for (var k in opts) t[k] = opts[k];
      t.composite = "add";
      try { out.push(el.animate([{ transform: "translateY(" + y0 + "px)" }, { transform: "translateY(" + y1 + "px)" }], t)); } catch (e) { /* no composite: skip the drift */ }
    }
    return out;
  }
  function startExit() {
    if (exiting) return exiting.done;
    var still = calm(), els = visibleBlocks(), anims = [], last = 0;
    var step = still ? 0 : Math.min(28, 200 / Math.max(1, els.length));
    var dur = still ? 140 : 320;
    els.forEach(function (el, i) {
      var a = play(el, 1, 0, 0, still ? 0 : -12, { duration: dur, delay: i * step, easing: "cubic-bezier(.5,0,.75,0)", fill: "forwards" });
      if (a.length) { anims.push.apply(anims, a); last = Math.max(last, i * step + dur); }
    });
    var ex = { anims: anims, timer: 0, route: null, waiting: false };
    ex.done = new Promise(function (resolve) { setTimeout(resolve, last); });
    // the parent may not navigate after all (same page, refused slug): come back
    ex.timer = setTimeout(function () { if (exiting === ex) { exiting = null; enter(anims); } }, last + 1500);
    exiting = ex;
    return ex.done;
  }
  function enter(old) {
    // after the front end has rendered (give it two frames), bring the new content in
    var go = function () {
      (old || []).forEach(function (a) { try { a.cancel(); } catch (e) { /* gone */ } });
      if (!canAnimate()) return;
      var still = calm(), els = visibleBlocks();
      var step = still ? 0 : Math.min(30, 260 / Math.max(1, els.length));
      els.forEach(function (el, i) {
        play(el, 0, 1, still ? 0 : 14, 0, { duration: still ? 160 : 520, delay: i * step, easing: "cubic-bezier(.16,1,.3,1)", fill: "backwards" });
      });
    };
    if (window.requestAnimationFrame) requestAnimationFrame(function () { requestAnimationFrame(go); });
    else setTimeout(go, 32);
  }
  // routeTo: a route change from the parent (after site.navigate, or back/forward)
  function routeTo(route) {
    if (!canAnimate() || (route === site.route && !exiting)) { setRoute(route); return; }
    var ex = exiting || (startExit(), exiting);
    ex.route = route; // the latest wins if routes arrive while it's leaving
    if (ex.waiting) return;
    ex.waiting = true;
    ex.done.then(function () {
      clearTimeout(ex.timer);
      if (exiting === ex) exiting = null;
      setRoute(ex.route);
      enter(ex.anims);
    });
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
      var s = slug == null ? "" : String(slug);
      if (canAnimate() && s !== site.route) startExit(); // out while the parent fetches the page
      post({ type: "site:navigate", slug: s });
    },

    // transitions (v1.6): false turns off the host's route transitions, for a
    // front end that animates its own; true turns them back on.
    transitions: function (on) {
      transitionsOn = on !== false;
    },

    ready: function () {
      if (readySent) return;
      readySent = true;
      post({ type: "site:ready" });
    },

    // theme (v1.5): "light" or "dark", the theme of the site bar the parent
    // draws above the front end, so it sits well with this design. Call it any
    // time (per route is fine); until it's called the bar follows the system.
    theme: function (t) {
      if (t !== "light" && t !== "dark") throw new TypeError('site.theme: "light" or "dark"');
      post({ type: "site:theme", theme: t });
    },

    // get: a dotted path ("pages.0.title") or an array of keys into
    // site.content; fallback when any step is missing, null or not an object.
    get: function (path, fallback) {
      try {
        var parts = Array.isArray(path) ? path : path == null || path === "" ? [] : String(path).split(".");
        var cur = site.content;
        for (var i = 0; i < parts.length; i++) {
          if (cur === null || typeof cur !== "object") return fallback;
          cur = own(cur, String(parts[i]));
        }
        return cur === undefined || cur === null ? fallback : cur;
      } catch (e) {
        return fallback;
      }
    },

    pages: function () { return collection("pages"); },
    experiments: function () { return collection("experiments"); },
    projects: function () { return collection("projects"); },

    // collection: site.content[name] if it is an array, else []. Never throws.
    collection: function (name) {
      try { return collection(String(name)); } catch (e) { return []; }
    },

    // project: the published project with this slug, or {}.
    project: function (slug) {
      try {
        var want = slug == null ? "" : String(slug);
        var list = collection("projects");
        for (var i = 0; i < list.length; i++) {
          if (list[i] && list[i].slug === want) return list[i];
        }
      } catch (e) { /* fall through */ }
      return {};
    },

    // openExternal: ask the parent to open an http(s) URL in a new tab (the
    // sandbox can't open popups or navigate the top window). Returns whether
    // the request was sent; anything but http(s) is refused. Call it from a
    // click handler: the parent can only open a tab right after a user
    // gesture.
    openExternal: function (url) {
      var href = safeURL(url);
      if (!href) return false;
      post({ type: "site:open", url: href });
      return true;
    },

    // page: the page with this slug ("" or no argument: home), or {}.
    page: function (slug) {
      try {
        var want = slug == null ? "" : String(slug);
        var list = collection("pages");
        for (var i = 0; i < list.length; i++) {
          if (list[i] && list[i].slug === want) return list[i];
        }
      } catch (e) { /* fall through */ }
      return {};
    },

    // field: item[name] if it matches opts.expect ("text": a string with
    // non-whitespace content, "list": an array, "number": a finite number,
    // "bool": a boolean; default "text"). Otherwise opts.fallback (default ""
    // / [] / 0 / false), and a site:gap report (once per item and field per
    // page load) so the observer can fill or fix the value. Lossless
    // coercions (text from a number or boolean, a number from a numeric
    // string, a bool from "true"/"false") return the coerced value but still
    // report the type break. Never throws.
    field: function (item, name, opts) {
      var expect = "text";
      var fallback;
      var hasFallback = false;
      try {
        if (opts && typeof opts === "object") {
          expect = EXPECT_ALIASES[opts.expect] || "text";
          if (own(opts, "fallback") !== undefined) {
            fallback = opts.fallback;
            hasFallback = true;
          }
        }
      } catch (e) { /* defaults */ }
      var optional = false;
      try { optional = !!(opts && typeof opts === "object" && own(opts, "optional") === true); } catch (e) { /* not optional */ }
      if (!hasFallback) fallback = defaultFallback(expect);
      try {
        name = typeof name === "string" ? name : safeString(name);
        var v = item !== null && (typeof item === "object" || typeof item === "function") ? own(item, name) : undefined;
        var r = check(v, expect);
        if (r[1] === null) return r[0];
        var got = r[1];
        if ((got === "empty" || got === "missing") && coercedTypes && isObject(item)) {
          var orig = coercedTypes.get(item);
          if (orig && orig[name]) got = orig[name]; // normalization defaulted a wrong type
        }
        // optional: empty is a legitimate value (no link, no video), not a
        // gap; type breaks are still reported
        if (!(optional && (got === "empty" || got === "missing"))) reportGap(item, name, expect, got);
        return r[0] !== undefined ? r[0] : fallback;
      } catch (e) {
        return fallback;
      }
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

  // collection: site.content[name] if it's an array, else a fresh [].
  function collection(name) {
    var list = site.content && typeof site.content === "object" ? own(site.content, name) : undefined;
    return Array.isArray(list) ? list : [];
  }

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
      try {
        site.content = normalize(m.content);
      } catch (e) {
        // never leave front ends without the shape; not a site:error, which
        // before ready would make the parent fall back over bad content
        site.content = { contractVersion: 1, pages: [], experiments: [], projects: [] };
        try { console.warn("site-host: content normalization failed:", e); } catch (e2) { /* no console */ }
      }
      site.route = typeof m.route === "string" ? m.route : "";
      if (!initialized) {
        initialized = true;
        resolveLoaded({ content: site.content, route: site.route });
      } else {
        setRoute(site.route);
      }
    } else if (m.type === "site:route" && typeof m.route === "string") {
      routeTo(m.route);
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
