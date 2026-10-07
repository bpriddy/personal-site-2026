// content.js: the site version of Particle Stream (builtin/stream).
//
//   • All content is baked: window.__BAKED (baked/site.js, inlined into
//     index.html at build time; scripts/bake-stream.sh refreshes it) holds
//     the site's content and copy. Nothing is fetched at runtime; the host's
//     site.loaded only supplies the starting route.
//   • Paged screens: each route is a short sequence of full-screen
//     compositions. Wheel, swipe, arrow keys and the pager step through them.
//   • Everything displaces the stream. Each screen is described to the wasm as
//     an obstacle scene (window.__SCENE), measured from the DOM so the two
//     always agree:
//       glyph  big type: the GPU draws it in relief (the DOM copy turns
//              transparent once it does, and stays for selection and readers)
//       plate  short text (.ob-word): the GPU draws its surface in the same
//              relief as the big type; the DOM text sits on it
//       box    block text and media: dark panels the stream flows around
//     The nav and pager are the "chrome" scene: they stay put while pages
//     slide through the water. window.__SCENE_T carries the slide (offset,
//     velocity, opacity) every frame, so the moving page plows the stream.
(function () {
  "use strict";
  var site = window.site || null;
  var B = window.__BAKED || {};
  ["pages", "projects", "experiments", "experience"].forEach(function (k) { if (!Array.isArray(B[k])) B[k] = []; });
  window.__SCENE_MODE = true;

  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var DEFAULTS = { tagline: "Creative technology / AI", experimentsEmpty: "Coming soon." };
  function T(key) {
    var v = B.copy && B.copy[key];
    return typeof v === "string" && v ? v : DEFAULTS[key] || "";
  }
  function s(v) { return typeof v === "string" ? v : v == null ? "" : String(v); }
  function list(v) { return Array.isArray(v) ? v : []; }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function go(route) {
    if (site) site.navigate(route);
    else location.hash = "#/" + route;
  }
  function link(text, route, cls) {
    var a = el("a", cls, text);
    a.href = "#/" + route;
    a.addEventListener("click", function (e) { e.preventDefault(); go(route); });
    return a;
  }
  function external(text, href, cls) {
    var b = el("button", cls, text + " ↗");
    b.type = "button";
    b.addEventListener("click", function () {
      if (site) site.openExternal(href); else window.open(href, "_blank", "noopener");
    });
    return b;
  }
  function paras(body) {
    return s(body).split(/\r?\n\s*\r?\n/).map(function (p) { return p.trim(); }).filter(Boolean);
  }
  var MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  function month(v) {
    var m = /^(\d{4})(?:-(\d{2}))?$/.exec(s(v));
    if (!m) return "";
    return m[2] ? MONTHS[+m[2] - 1] + " " + m[1] : m[1];
  }
  function dates(r) {
    var a = month(r.start), b = r.current ? "present" : month(r.end);
    return a && b ? a + " to " + b : a || b;
  }
  // long paragraphs read better at body size
  function lede(text) { return text.length > 420 ? "st-body st-long" : "st-lede"; }
  function two(n) { return (n < 9 ? "0" : "") + (n + 1); }

  // ── the parts a screen is made of ──
  // big display type, drawn by the GPU in relief
  function glyph(tag, cls, text) { return el(tag, "ob-glyph " + (cls || ""), s(text).toUpperCase()); }
  // a block the stream flows around
  function box(tag, cls) { return el(tag || "div", "ob-box " + (cls || "")); }
  function label(text) { return el("p", "st-label ob-word", text); }
  // one relief surface for a group of short text (never several small ones side by side)
  function plate(tag, cls) { return el(tag || "div", "st-plate ob-word " + (cls || "")); }

  function mediaEl(m, alt, eager) {
    if (!m || (m.kind !== "image" && m.kind !== "loop")) return null;
    var fig = el("figure", "st-media ob-box");
    if (m.width && m.height) fig.style.aspectRatio = m.width + " / " + m.height;
    if (m.kind === "loop") {
      var v = el("video");
      v.muted = true; v.loop = true; v.playsInline = true; v.preload = "none";
      v.setAttribute("muted", ""); v.setAttribute("playsinline", "");
      if (m.poster) v.poster = m.poster;
      v.src = m.src;
      v.setAttribute("aria-label", m.alt || alt);
      fig.append(v);
    } else {
      var img = el("img");
      img.src = m.src;
      img.alt = m.alt || alt;
      img.decoding = "async";
      if (!eager) img.loading = "lazy";
      if (m.width && m.height) { img.width = m.width; img.height = m.height; }
      fig.append(img);
    }
    return fig;
  }

  // ── the screens of each route ──
  function screen(cls, name) {
    var sc = el("section", "st-screen " + (cls || ""));
    sc.setAttribute("aria-label", name);
    return sc;
  }

  function homeScreens() {
    var home = B.pages.filter(function (p) { return p.slug === ""; })[0] || {};
    var name = s(home.title) || "Ben Priddy";
    var roles = B.experience;
    var current = roles.filter(function (r) { return r.current; })[0];
    var out = [];

    var hero = screen("st-hero", name);
    var hp = plate("div");
    hp.append(el("p", "st-tagline", T("tagline")));
    if (current) hp.append(el("p", "st-now", s(current.role) + " · " + s(current.company)));
    hero.append(glyph("h1", "st-name", name), hp);
    out.push(hero);

    paras(home.body).forEach(function (p, i) {
      var sc = screen("st-read", "About");
      var b = box("div", "st-panel");
      if (i === 0) b.append(el("p", "st-label", "About"));
      b.append(el("p", lede(p), p));
      sc.append(b);
      out.push(sc);
    });

    if (roles.length) {
      var xs = screen("st-xp-screen", "Experience");
      xs.append(glyph("h2", "st-h2", "Experience"));
      var ol = box("ol", "st-panel st-xp");
      roles.forEach(function (r) {
        var li = el("li", "st-xp-item");
        li.append(el("p", "st-xp-dates", dates(r)));
        var main = el("div", "st-xp-main");
        main.append(el("h3", "st-xp-role", s(r.role)), el("p", "st-xp-company", s(r.company)));
        if (r.note) main.append(el("p", "st-xp-note", s(r.note)));
        li.append(main);
        ol.append(li);
      });
      xs.append(ol);
      out.push(xs);
    }

    if (B.projects.length) {
      var ws = screen("st-list-screen", "Selected work");
      ws.append(workList(B.projects.slice(0, 6), 0, "Selected work", true));
      out.push(ws);
    }

    out.push(experimentsScreen());
    return out;
  }

  // the titles are big enough to stand in the stream on their own: relief
  // type, no surface (client and year are on each project's page)
  function workList(projects, offset, heading, allLink) {
    var wrap = el("div", "st-list");
    if (heading) wrap.append(label(heading));
    var ol = el("ol", "st-work");
    projects.forEach(function (p) {
      var li = el("li", "st-work-item");
      var a = link("", "work/" + s(p.slug), "st-work-link");
      a.append(glyph("span", "st-work-title", s(p.title) || s(p.slug)));
      li.append(a);
      ol.append(li);
    });
    wrap.append(ol);
    if (allLink) {
      var more = el("p", "st-more");
      more.append(link("All work", "work", "st-link ob-word"));
      wrap.append(more);
    }
    return wrap;
  }

  function experimentsScreen() {
    var sc = screen("st-exp-screen", "Experiments");
    sc.append(glyph("h2", "st-h2", "Experiments"));
    var exps = B.experiments;
    if (!exps.length) {
      sc.append(el("p", "st-soon ob-word", T("experimentsEmpty")));
      return sc;
    }
    var ol = box("ol", "st-panel st-exps");
    exps.forEach(function (x) {
      var li = el("li", "st-exp");
      li.append(el("h3", "st-xp-role", s(x.title) || s(x.slug)));
      if (x.summary) li.append(el("p", "st-body", s(x.summary)));
      if (x.link) li.append(external("Open it", s(x.link), "st-link"));
      ol.append(li);
    });
    sc.append(ol);
    return sc;
  }

  function workScreens() {
    var projects = B.projects;
    var out = [];
    var per = window.innerHeight < 760 ? 5 : 6;
    for (var k = 0; k < projects.length; k += per) {
      var sc = screen("st-list-screen", "Work");
      if (k === 0) sc.append(glyph("h1", "st-h2", "Work"));
      sc.append(workList(projects.slice(k, k + per), k, k === 0 ? "" : "Work, continued"));
      out.push(sc);
    }
    if (!out.length) {
      var e = screen("st-hero", "Work");
      e.append(glyph("h1", "st-name", "Work"));
      out.push(e);
    }
    return out;
  }

  function projectScreens(slug) {
    var projects = B.projects;
    var i = -1;
    projects.forEach(function (p, k) { if (p.slug === slug) i = k; });
    if (i < 0) return notFoundScreens();
    var p = projects[i];
    var title = s(p.title) || slug;
    var media = list(p.media);
    var heroImg = media.filter(function (m) { return m && m.kind === "image"; })[0];
    var rest = media.filter(function (m) { return m && m !== heroImg; });
    var out = [];

    var t = screen("st-hero st-project-hero", title);
    t.append(glyph("h1", "st-name st-project-name", title));
    var mp = plate("div");
    mp.append(el("p", "st-label", two(i) + " / " + projects.length));
    var meta = el("dl", "st-meta");
    [["Client", p.client], ["Agency", p.agency], ["Year", p.year], ["Role", list(p.roles).join(", ")]].forEach(function (row) {
      if (!s(row[1])) return;
      var d = el("div");
      d.append(el("dt", null, row[0]), el("dd", null, s(row[1])));
      meta.append(d);
    });
    mp.append(meta);
    t.append(mp);
    out.push(t);

    var brief = paras(p.summary);
    if (heroImg || brief.length) {
      var sc = screen("st-split", "Brief");
      var hm = heroImg && mediaEl(heroImg, title, true);
      if (hm) sc.append(hm);
      if (brief.length) {
        var b = box("div", "st-panel");
        b.append(el("p", "st-label", "Brief"));
        brief.forEach(function (x) { b.append(el("p", lede(x), x)); });
        sc.append(b);
      }
      out.push(sc);
    }
    [["Role", p.contribution], ["Notes", p.body]].forEach(function (sec) {
      var ps = paras(sec[1]);
      if (!ps.length) return;
      var sc = screen("st-read", sec[0]);
      var b = box("div", "st-panel");
      b.append(el("p", "st-label", sec[0]));
      ps.forEach(function (x) { b.append(el("p", "st-body", x)); });
      sc.append(b);
      out.push(sc);
    });
    if (rest.length) {
      var g = screen("st-gallery", "Gallery");
      rest.slice(0, 4).forEach(function (m, k) { var me = mediaEl(m, title + " " + (k + 1), false); if (me) g.append(me); });
      out.push(g);
    }
    var nx = screen("st-next", "Next");
    // one surface: the project's links, all work, and the next project's cue
    var np = plate("div");
    var links = el("p", "st-links");
    if (p.youtube) links.append(external("Watch the film", "https://www.youtube.com/watch?v=" + s(p.youtube), "st-link"));
    if (p.link) links.append(external("Visit the work", s(p.link), "st-link"));
    links.append(link("All work", "work", "st-link"));
    np.append(links);
    nx.append(np);
    if (projects.length > 1) {
      var next = projects[(i + 1) % projects.length];
      np.append(el("p", "st-label", "Next project"));
      var a = link("", "work/" + s(next.slug), "st-next-link");
      a.append(glyph("span", "st-h2", s(next.title) || s(next.slug)));
      nx.append(a);
    }
    out.push(nx);
    return out;
  }

  function pageScreens(p) {
    var title = s(p.title) || s(p.slug);
    var t = screen("st-hero", title);
    t.append(glyph("h1", "st-name", title));
    var out = [t];
    paras(p.body).forEach(function (x, i) {
      var sc = screen("st-read", title);
      var b = box("div", "st-panel");
      b.append(el("p", i === 0 ? "st-lede" : "st-body", x));
      sc.append(b);
      out.push(sc);
    });
    return out;
  }

  function notFoundScreens() {
    var sc = screen("st-hero", "Not found");
    var p = el("p", "st-tagline ob-word", "There's nothing here. ");
    p.append(link("Home", "", "st-link"));
    sc.append(glyph("h1", "st-name", "Not found"), p);
    return [sc];
  }

  function screensFor(r) {
    if (r === "") return homeScreens();
    if (r === "work") return workScreens();
    if (r.indexOf("work/") === 0) return projectScreens(r.slice(5));
    if (r === "experiments") return [experimentsScreen()];
    var p = B.pages.filter(function (x) { return x.slug === r; })[0];
    return p ? pageScreens(p) : notFoundScreens();
  }

  // ── the frame ──
  var stage = el("main", "st-stage");
  var nav = el("nav", "st-nav");
  nav.setAttribute("aria-label", "Pages");
  var pager = el("div", "st-pager ob-word");
  var pagerN = el("span", "st-pager-n");
  var prevB = el("button", "st-pager-b", "Back");
  prevB.type = "button"; prevB.setAttribute("aria-label", "Previous screen");
  var nextB = el("button", "st-pager-b", "Next");
  nextB.type = "button"; nextB.setAttribute("aria-label", "Next screen");
  pager.append(prevB, pagerN, nextB);
  document.body.append(stage, nav, pager);

  function renderNav(r) {
    nav.replaceChildren();
    var items = [["", "Home"], ["work", "Work"], ["experiments", "Experiments"]];
    B.pages.forEach(function (p) {
      if (p.slug && p.slug !== "work" && p.slug !== "experiments") items.push([p.slug, s(p.title) || p.slug]);
    });
    items.forEach(function (it) {
      var a = link(String(it[1]).toUpperCase(), it[0], "st-nav-link ob-glyph");
      var cur = it[0] === "" ? r === "" : r === it[0] || r.indexOf(it[0] + "/") === 0;
      if (cur) a.setAttribute("aria-current", "page");
      nav.append(a);
    });
  }

  // ── the obstacle scene, measured from the DOM ──
  var mctx = document.createElement("canvas").getContext("2d");
  function fontOf(e) {
    var cs = getComputedStyle(e);
    return cs.fontStyle + " " + cs.fontWeight + " " + cs.fontSize + " " + cs.fontFamily;
  }
  // an element's text as laid out, one entry per line: {t, x, y (baseline)}
  function lines(e, font) {
    mctx.font = font;
    var asc = mctx.measureText("Hg").fontBoundingBoxAscent || 0;
    var out = [], cur = null;
    var walker = document.createTreeWalker(e, NodeFilter.SHOW_TEXT);
    var range = document.createRange();
    var node;
    while ((node = walker.nextNode())) {
      var text = node.nodeValue, re = /\S+/g, m;
      while ((m = re.exec(text))) {
        range.setStart(node, m.index);
        range.setEnd(node, m.index + m[0].length);
        var r = range.getClientRects()[0];
        if (!r || r.width === 0) continue;
        if (cur && Math.abs(r.top - cur.top) < r.height * 0.5) {
          cur.t += " " + m[0];
        } else {
          cur = { t: m[0], x: r.left, y: r.top + asc, top: r.top };
          out.push(cur);
        }
      }
    }
    return out;
  }
  function describe(root, dy) {
    var out = { glyphs: [], words: [], boxes: [], plates: [] };
    function add(kind, e) {
      var f = fontOf(e);
      lines(e, f).forEach(function (l) { out[kind].push({ t: l.t, f: f, x: l.x, y: l.y - dy }); });
    }
    if (root.offsetParent === null && getComputedStyle(root).position !== "fixed") return out;
    root.querySelectorAll(".ob-glyph").forEach(function (e) { add("glyphs", e); });
    // panels and pills: the root itself may be one (the nav, the pager)
    [root].concat([].slice.call(root.querySelectorAll(".ob-box, .ob-word"))).forEach(function (e) {
      if (!e.classList.contains("ob-box") && !e.classList.contains("ob-word")) return;
      var r = e.getBoundingClientRect();
      if (r.width < 1 || r.height < 1) return;
      var rad = parseFloat(getComputedStyle(e).borderTopLeftRadius) || 0;
      out[e.classList.contains("ob-word") ? "plates" : "boxes"].push({ x: r.left, y: r.top - dy, w: r.width, h: r.height, r: rad });
    });
    return out;
  }
  var sceneGen = 0;
  // measured at rest: a sliding page's offset reaches the wasm through __SCENE_T
  function publishScene() {
    var sc = screens[index];
    if (!sc) return;
    var chrome = describe(nav, 0), p = describe(pager, 0);
    chrome.plates = chrome.plates.concat(p.plates);
    window.__SCENE = {
      gen: ++sceneGen,
      vw: window.innerWidth,
      vh: window.innerHeight,
      page: describe(sc, sc._dy || 0),
      chrome: chrome
    };
  }

  // the wasm sets __SCENE_DRAWN once its relief type is on screen; until then
  // (and if WebGPU never comes up) the DOM shows the big type itself
  (function watchDrawn() {
    if (window.__SCENE_DRAWN) document.documentElement.classList.add("gpu-type");
    else requestAnimationFrame(watchDrawn);
  })();

  // ── paging ──
  var screens = [], index = 0, busy = false, queued = null, seq = 0;
  var SLIDE = 0.55; // how far a page travels on a step (fraction of the screen)
  var OUT_MS = reduce ? 160 : 380, IN_MS = reduce ? 200 : 560;
  window.__SCENE_T = { dv: 0, vy: 0, op: 1 };

  function setOffset(sc, dv, op) {
    sc._dy = dv * window.innerHeight;
    sc.style.transform = dv ? "translate3d(0," + sc._dy.toFixed(1) + "px,0)" : "";
    sc.style.opacity = op >= 1 ? "" : String(op);
  }
  function updatePager() {
    pagerN.textContent = screens.length > 1 ? two(index) + " / " + two(screens.length - 1) : "";
    prevB.disabled = index === 0;
    nextB.disabled = index >= screens.length - 1;
    pager.hidden = screens.length < 2;
  }
  function activate(i) {
    screens.forEach(function (sc, k) {
      var on = k === i;
      sc.classList.toggle("is-on", on);
      sc.inert = !on;
    });
    index = i;
    updatePager();
    playLoops();
  }
  function animate(ms, fn) {
    return new Promise(function (res) {
      var t0 = performance.now(), last = t0;
      function f(now) {
        var t = Math.min(1, (now - t0) / ms);
        fn(t, Math.max(1, now - last));
        last = now;
        if (t < 1) requestAnimationFrame(f); else res();
      }
      requestAnimationFrame(f);
    });
  }
  // dir +1: the page rises out of the top and the next rises in from below
  // a click or key during a slide runs when it lands (wheel momentum doesn't queue)
  function step(to) {
    if (busy) { queued = to; return; }
    if (to === index || to < 0 || to >= screens.length) return;
    busy = true;
    var my = ++seq; // a route change mid-slide retires this one
    var dir = to > index ? 1 : -1;
    var from = screens[index], next = screens[to];
    var prev = 0;
    function frame(sc, dv, op, dtms) {
      if (my !== seq) return;
      setOffset(sc, dv, op);
      window.__SCENE_T = { dv: dv, vy: -2 * (dv - prev) / (dtms / 1000), op: op };
      prev = dv;
    }
    animate(OUT_MS, function (t, dtms) {
      var e = t * t;
      frame(from, reduce ? 0 : -dir * SLIDE * e, 1 - e, dtms);
    }).then(function () {
      if (my !== seq) return;
      setOffset(from, 0, 1);
      var start = reduce ? 0 : dir * SLIDE;
      setOffset(next, start, 0);
      activate(to);
      publishScene();
      prev = start;
      return animate(IN_MS, function (t, dtms) {
        var e = 1 - Math.pow(1 - t, 3);
        frame(next, start * (1 - e), e, dtms);
      });
    }).then(function () {
      if (my !== seq) return;
      setOffset(next, 0, 1);
      window.__SCENE_T = { dv: 0, vy: 0, op: 1 };
      busy = false;
      if (queued != null) { var q = queued; queued = null; step(q); }
    });
  }
  prevB.addEventListener("click", function () { step(index - 1); });
  nextB.addEventListener("click", function () { step(index + 1); });
  // tabbing into another screen brings it up
  stage.addEventListener("focusin", function (e) {
    var k = screens.indexOf(e.target.closest && e.target.closest(".st-screen"));
    if (k >= 0 && k !== index) step(k);
  });

  // a panel whose text doesn't fit scrolls first; then the page steps
  function scroller(t) {
    while (t && t !== stage && t.nodeType === 1) {
      if (t.scrollHeight > t.clientHeight + 2 && /(auto|scroll)/.test(getComputedStyle(t).overflowY)) return t;
      t = t.parentElement;
    }
    return null;
  }
  function canScroll(sc, dir) {
    if (!sc) return false;
    return dir > 0 ? sc.scrollTop + sc.clientHeight < sc.scrollHeight - 2 : sc.scrollTop > 2;
  }
  var wheelAcc = 0, wheelT = 0;
  window.addEventListener("wheel", function (e) {
    var dir = e.deltaY > 0 ? 1 : -1;
    if (canScroll(scroller(e.target), dir)) return;
    e.preventDefault();
    var now = performance.now();
    if (now - wheelT > 260) wheelAcc = 0;
    wheelT = now;
    if (busy) return;
    wheelAcc += e.deltaMode === 1 ? e.deltaY * 32 : e.deltaY;
    if (Math.abs(wheelAcc) > 50) { step(index + (wheelAcc > 0 ? 1 : -1)); wheelAcc = 0; }
  }, { passive: false });
  var ty = null, tsc = null, tst = 0;
  window.addEventListener("touchstart", function (e) {
    if (e.touches.length !== 1) { ty = null; return; }
    ty = e.touches[0].clientY; tsc = scroller(e.target);
    tst = tsc ? tsc.scrollTop : 0;
  }, { passive: true });
  window.addEventListener("touchend", function (e) {
    if (ty == null) return;
    var dy = ty - e.changedTouches[0].clientY;
    ty = null;
    if (Math.abs(dy) < 48) return;
    var dir = dy > 0 ? 1 : -1;
    // a swipe that scrolled a panel (or still could) stays in the panel
    if (tsc && (tsc.scrollTop !== tst || canScroll(tsc, dir))) return;
    step(index + dir);
  }, { passive: true });
  window.addEventListener("keydown", function (e) {
    if (e.altKey || e.ctrlKey || e.metaKey) return;
    var t = e.target;
    if (t && /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)) return;
    var k = e.key;
    if (k === "ArrowDown" || k === "PageDown" || (k === " " && !e.shiftKey)) { e.preventDefault(); step(index + 1); }
    else if (k === "ArrowUp" || k === "PageUp" || (k === " " && e.shiftKey)) { e.preventDefault(); step(index - 1); }
    else if (k === "Home") { e.preventDefault(); step(0); }
    else if (k === "End") { e.preventDefault(); step(screens.length - 1); }
  });

  function playLoops() {
    screens.forEach(function (sc, k) {
      sc.querySelectorAll("video").forEach(function (v) {
        if (k === index && !reduce) { v.preload = "auto"; var p = v.play(); if (p && p.catch) p.catch(function () {}); }
        else v.pause();
      });
    });
  }

  function render(r) {
    r = r || "";
    renderNav(r);
    stage.replaceChildren();
    screens = screensFor(r);
    screens.forEach(function (sc) { stage.append(sc); setOffset(sc, 0, 1); });
    busy = false;
    queued = null;
    seq++;
    window.__SCENE_T = { dv: 0, vy: 0, op: 1 };
    activate(0);
    publishScene();
  }

  var resizeT = 0;
  window.addEventListener("resize", function () {
    clearTimeout(resizeT);
    resizeT = setTimeout(function () { if (!busy) publishScene(); }, 120);
  });

  // the type must be in before it's measured and rastered
  var fontsIn = document.fonts && document.fonts.load
    ? Promise.all(["900 64px 'Inter Tight'", "400 16px 'Instrument Sans'", "400 12px 'Geist Mono'"].map(function (f) {
        return document.fonts.load(f).catch(function () {});
      }))
    : Promise.resolve();

  if (site) {
    site.loaded.then(function (init) {
      try {
        site.theme("dark");
        render(init.route);
        site.onRoute(render);
        site.ready();
        fontsIn.then(publishScene);
      } catch (e) {
        site.reportError(e);
      }
    });
  } else {
    var fromHash = function () { return location.hash.replace(/^#\/?/, ""); };
    window.addEventListener("hashchange", function () { render(fromHash()); });
    render(fromHash());
    fontsIn.then(publishScene);
  }
})();
