// /resume (v1.14): Ben's career as a system. The graph (roles, projects,
// the themes they touch, his skills) comes from the page as JSON; this
// draws it as an SVG with a small force layout: everything bursts out of the
// centre and settles, hover traces a node's connections, click opens it,
// drag pulls it around. The resume below gets a gentle reveal as it scrolls in.
(function () {
  "use strict";
  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var NS = "http://www.w3.org/2000/svg";

  // ── the resume's sections ease in as they scroll into view ──
  (function reveal() {
    var secs = [].slice.call(document.querySelectorAll(".rz-section"));
    if (reduce || !("IntersectionObserver" in window)) return;
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) { e.target.classList.add("rz-in"); io.unobserve(e.target); }
      });
    }, { rootMargin: "0px 0px -10% 0px" });
    secs.forEach(function (s) {
      // only what's still below the fold waits for its reveal
      if (s.getBoundingClientRect().top > window.innerHeight) { s.classList.add("rz-pre"); io.observe(s); }
    });
  })();

  var stage = document.getElementById("rz-stage");
  var dataEl = document.getElementById("rz-graph");
  if (!stage || !dataEl) return;
  var data;
  try { data = JSON.parse(dataEl.textContent); } catch (e) { return; }
  if (!data || !data.nodes || !data.nodes.length) return;

  var KIND_LABEL = { me: "", role: "Role", project: "Project", theme: "Theme", skill: "Skill" };
  var RADIUS = { me: 22, role: 12, project: 7, theme: 6, skill: 8 };

  var nodes = data.nodes.map(function (n, i) {
    return { d: n, i: i, x: 0, y: 0, vx: 0, vy: 0, tx: 0, ty: 0, r: RADIUS[n.kind] || 6, hidden: false, nb: [], links: [] };
  });
  var byId = {};
  nodes.forEach(function (n) { byId[n.d.id] = n; });
  var links = [];
  (data.links || []).forEach(function (l) {
    var a = byId[l[0]], b = byId[l[1]];
    if (!a || !b) return;
    var k = { a: a, b: b };
    links.push(k);
    a.nb.push(b); b.nb.push(a);
    a.links.push(k); b.links.push(k);
  });

  // ── SVG ──
  var svg = document.createElementNS(NS, "svg");
  svg.setAttribute("role", "group");
  svg.setAttribute("aria-label", "Ben Priddy's roles, projects, themes and skills, connected");
  var gLinks = document.createElementNS(NS, "g");
  var gNodes = document.createElementNS(NS, "g");
  svg.append(gLinks, gNodes);
  stage.appendChild(svg);

  links.forEach(function (k) {
    k.el = document.createElementNS(NS, "line");
    k.el.setAttribute("class", "rz-link");
    gLinks.appendChild(k.el);
  });
  nodes.forEach(function (n) {
    var g = document.createElementNS(NS, "g");
    g.setAttribute("class", "rz-node");
    g.setAttribute("data-kind", n.d.kind);
    g.setAttribute("tabindex", "0");
    g.setAttribute("role", "button");
    g.setAttribute("aria-label", (KIND_LABEL[n.d.kind] ? KIND_LABEL[n.d.kind] + ": " : "") + n.d.label + (n.d.sub ? ", " + n.d.sub : ""));
    var c = document.createElementNS(NS, "circle");
    c.setAttribute("r", n.r);
    var t = document.createElementNS(NS, "text");
    t.textContent = n.d.kind === "me" ? "" : n.d.label;
    g.append(c, t);
    // a role names its company under its title (two are "Technical Director")
    if (n.d.kind === "role" && n.d.sub) {
      var sub = document.createElementNS(NS, "text");
      sub.setAttribute("class", "rz-sub");
      sub.textContent = n.d.sub.split(" \u00b7 ")[0];
      g.append(sub);
      n.subText = sub;
    }
    n.el = g;
    n.text = t;
    gNodes.appendChild(g);
  });

  // ── layout ──
  // An ordered system: Ben on the left with his two skill nodes, his roles
  // (newest first), each role's projects beside it, and the themes those
  // projects share, ordered to keep crossings down. On a narrow screen the
  // columns become rows. Every node springs to its slot from Ben, a layer at
  // a time; settled, it sways a little; dragged, it snaps back.
  var W = 0, H = 0, wide = true;
  var LAYER = { me: 0, skill: 0, role: 1, project: 2, theme: 3 };
  var DELAY = { me: 0, skill: 120, role: 140, project: 320, theme: 500 };
  var me = nodes.filter(function (n) { return n.d.kind === "me"; })[0];
  var roles = nodes.filter(function (n) { return n.d.kind === "role"; });
  var skills = nodes.filter(function (n) { return n.d.kind === "skill"; });
  var projects = [];
  roles.forEach(function (r) {
    r.nb.forEach(function (m) { if (m.d.kind === "project" && projects.indexOf(m) < 0) projects.push(m); });
  });
  nodes.forEach(function (n) { if (n.d.kind === "project" && projects.indexOf(n) < 0) projects.push(n); });
  var themes = nodes.filter(function (n) { return n.d.kind === "theme"; });
  // themes in the order of the projects they touch (their barycentre)
  themes.forEach(function (t) {
    var idx = t.nb.filter(function (m) { return m.d.kind === "project"; }).map(function (m) { return projects.indexOf(m); });
    t.bary = idx.length ? idx.reduce(function (a, b) { return a + b; }, 0) / idx.length : 0;
  });
  themes.sort(function (a, b) { return a.bary - b.bary; });

  function spread(list, from, to) {
    var n = list.length;
    list.forEach(function (m, i) { m.slot = n === 1 ? (from + to) / 2 : from + (to - from) * i / (n - 1); });
  }
  function size() {
    var r = stage.getBoundingClientRect();
    W = Math.max(300, r.width); H = Math.max(320, r.height);
    wide = W >= 760;
    svg.setAttribute("viewBox", "0 0 " + W + " " + H);
    var top = 34, bottom = H - 34;
    if (wide) {
      var col = { 0: 0.04, 1: 0.2, 2: 0.47, 3: 0.79 };
      me.tx = W * col[0]; me.ty = H / 2;
      skills.forEach(function (s, i) { s.tx = W * col[0]; s.ty = H / 2 + (i === 0 ? -1 : 1) * Math.min(150, H * 0.28); });
      spread(roles, top + 30, bottom - 30); roles.forEach(function (n) { n.tx = W * col[1]; n.ty = n.slot; });
      spread(projects, top, bottom); projects.forEach(function (n) { n.tx = W * col[2]; n.ty = n.slot; });
      spread(themes, top + 10, bottom - 10); themes.forEach(function (n) { n.tx = W * col[3]; n.ty = n.slot; });
    } else {
      var row = { me: 0.06, role: 0.24, project: 0.5, theme: 0.76, skill: 0.94 };
      me.tx = W / 2; me.ty = H * row.me;
      [[roles, "role"], [projects, "project"], [themes, "theme"], [skills, "skill"]].forEach(function (g) {
        spread(g[0], 22, W - 22);
        g[0].forEach(function (n) { n.tx = n.slot; n.ty = H * row[g[1]]; });
      });
    }
    // on a narrow screen, labels wait for a tap; wide, everything is named
    nodes.forEach(function (n) {
      if (n.d.kind === "me") return;
      // narrow: labels on the right half sit to the left of their node
      var left = !wide && n.tx > W * 0.55;
      [n.text, n.subText].forEach(function (t, i) {
        if (!t) return;
        t.classList.toggle("rz-label-quiet", !wide);
        t.setAttribute("text-anchor", left ? "end" : "start");
        t.setAttribute("x", left ? -(n.r + 7) : n.r + 7);
        t.setAttribute("y", i === 0 ? (n.subText && wide ? -2 : 4) : 13);
      });
    });
  }

  var t0 = performance.now();
  function step(now) {
    var moving = false;
    nodes.forEach(function (n) {
      if (n.hidden || n.dragging) return;
      if (now - t0 < DELAY[n.d.kind]) return;
      var k = 0.075, damp = 0.8;
      n.vx = (n.vx + (n.tx - n.x) * k) * damp;
      n.vy = (n.vy + (n.ty - n.y) * k) * damp;
      n.x += n.vx; n.y += n.vy;
      if (Math.abs(n.vx) + Math.abs(n.vy) > 0.02 || Math.abs(n.tx - n.x) + Math.abs(n.ty - n.y) > 0.5) moving = true;
    });
    return moving;
  }
  function draw(now) {
    var settle = Math.min(1, Math.max(0, (now - t0 - 1600) / 1200)); // sway fades in once settled
    nodes.forEach(function (n) {
      var ox = 0, oy = 0;
      if (!reduce && !n.dragging && n.d.kind !== "me") {
        var s = (now - t0) / 1000;
        ox = Math.sin(s * 0.55 + n.i * 1.7) * 1.8 * settle;
        oy = Math.cos(s * 0.45 + n.i * 2.3) * 1.8 * settle;
      }
      n.px = n.x + ox; n.py = n.y + oy;
      n.el.setAttribute("transform", "translate(" + n.px.toFixed(1) + " " + n.py.toFixed(1) + ")");
    });
    links.forEach(function (k) {
      k.el.setAttribute("x1", k.a.px.toFixed(1)); k.el.setAttribute("y1", k.a.py.toFixed(1));
      k.el.setAttribute("x2", k.b.px.toFixed(1)); k.el.setAttribute("y2", k.b.py.toFixed(1));
    });
  }
  var running = false;
  function loop(now) {
    var moving = step(now);
    draw(now);
    if (!reduce || moving) requestAnimationFrame(loop);
    else running = false;
  }
  function wake() { if (!running) { running = true; requestAnimationFrame(loop); } }
  function settleNow() {
    nodes.forEach(function (n) { n.x = n.tx; n.y = n.ty; n.vx = n.vy = 0; });
    draw(performance.now());
  }

  size();
  nodes.forEach(function (n) { n.x = me.tx; n.y = me.ty; n.vx = n.vy = 0; });
  if (reduce) settleNow(); else wake();

  if ("ResizeObserver" in window) new ResizeObserver(function () {
    size();
    if (reduce) settleNow(); else wake();
  }).observe(stage);

  // ── tracing and opening ──
  var focus = null, hover = null;
  function trace(n) {
    svg.classList.toggle("rz-tracing", !!n);
    nodes.forEach(function (m) { m.el.classList.remove("rz-on", "rz-focus"); });
    links.forEach(function (k) { k.el.classList.remove("rz-on"); });
    if (!n) return;
    n.el.classList.add("rz-on", "rz-focus");
    n.nb.forEach(function (m) { m.el.classList.add("rz-on"); });
    n.links.forEach(function (k) { k.el.classList.add("rz-on"); });
  }
  function current() { return hover || focus; }

  var panel = document.getElementById("rz-panel");
  var pKind = panel.querySelector(".rz-panel-kind");
  var pTitle = panel.querySelector(".rz-panel-title");
  var pSub = panel.querySelector(".rz-panel-sub");
  var pBody = panel.querySelector(".rz-panel-body");
  var pLinks = panel.querySelector(".rz-panel-links");
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  var GROUP = [["role", "Roles"], ["project", "Projects"], ["theme", "Themes"], ["skill", "Skills"], ["me", ""]];
  function open(n) {
    focus = n;
    trace(n);
    var d = n.d;
    pKind.textContent = d.kind === "skill" ? d.sub : d.kind === "me" ? "Profile" : KIND_LABEL[d.kind];
    pTitle.textContent = d.label;
    pSub.textContent = d.kind === "skill" ? "" : d.sub || "";
    pSub.hidden = !pSub.textContent;
    pBody.replaceChildren();
    // a role's one-line note only when it has no bullets (they say it better)
    if (!(d.kind === "role" && d.bullets && d.bullets.length)) {
      String(d.text || "").split(/\n\s*\n/).forEach(function (p) { if (p.trim()) pBody.append(el("p", null, p.trim())); });
    }
    if (d.bullets && d.bullets.length) {
      var ul = el("ul");
      d.bullets.forEach(function (b) { ul.append(el("li", null, b)); });
      pBody.append(ul);
    }
    if (d.kind === "theme") {
      var count = n.nb.filter(function (m) { return m.d.kind === "project"; }).length;
      pBody.append(el("p", null, count + (count === 1 ? " project" : " projects")));
    }
    if (d.href) {
      var a = el("a", null, "See the project →");
      a.href = d.href;
      var para = el("p");
      para.append(a);
      pBody.append(para);
    }
    pLinks.replaceChildren();
    GROUP.forEach(function (g) {
      var items = n.nb.filter(function (m) { return m.d.kind === g[0] && !m.hidden; });
      if (!items.length) return;
      var wrap = el("div");
      wrap.append(el("h3", null, g[1] || "Ben"));
      var ul = el("ul");
      items.forEach(function (m) {
        var b = el("button", null, m.d.label);
        b.type = "button";
        b.addEventListener("click", function () { open(m); m.el.focus({ preventScroll: true }); });
        var li = el("li");
        li.append(b);
        ul.append(li);
      });
      wrap.append(ul);
      pLinks.append(wrap);
    });
    pLinks.hidden = !pLinks.childNodes.length;
    panel.hidden = false;
  }
  function close() {
    focus = null;
    panel.hidden = true;
    trace(hover);
  }
  panel.querySelector(".rz-close").addEventListener("click", close);
  document.addEventListener("keydown", function (e) { if (e.key === "Escape" && !panel.hidden) close(); });

  // ── pointer: hover traces, click opens, drag pulls ──
  var drag = null;
  function point(e) {
    var r = svg.getBoundingClientRect();
    return { x: (e.clientX - r.left) * W / r.width, y: (e.clientY - r.top) * H / r.height };
  }
  nodes.forEach(function (n) {
    n.el.addEventListener("pointerenter", function () { if (!drag) { hover = n; trace(n); } });
    n.el.addEventListener("pointerleave", function () { if (!drag) { hover = null; trace(focus); } });
    n.el.addEventListener("focus", function () { hover = n; trace(n); });
    n.el.addEventListener("blur", function () { hover = null; trace(focus); });
    n.el.addEventListener("keydown", function (e) {
      if (e.key === "Enter" || e.key === " ") { e.preventDefault(); open(n); }
    });
    n.el.addEventListener("pointerdown", function (e) {
      e.preventDefault();
      var p = point(e);
      drag = { n: n, x0: p.x, y0: p.y, moved: false };
      n.el.setPointerCapture(e.pointerId);
    });
    n.el.addEventListener("pointermove", function (e) {
      if (!drag || drag.n !== n) return;
      var p = point(e);
      if (!drag.moved && Math.hypot(p.x - drag.x0, p.y - drag.y0) < 4) return;
      drag.moved = true;
      n.dragging = true;
      n.x = p.x; n.y = p.y; n.vx = n.vy = 0;
      if (reduce) draw(performance.now()); else wake();
    });
    n.el.addEventListener("pointerup", function () {
      if (!drag || drag.n !== n) return;
      var moved = drag.moved;
      n.dragging = false;
      drag = null;
      if (moved) { if (reduce) settleNow(); else wake(); } // it snaps back to its place
      else open(n);
    });
  });
  svg.addEventListener("pointerdown", function (e) { if (e.target === svg && focus) close(); });

  // ── legend: show or hide a kind ──
  [].forEach.call(document.querySelectorAll(".rz-legend button"), function (b) {
    b.addEventListener("click", function () {
      var on = b.getAttribute("aria-pressed") !== "true";
      b.setAttribute("aria-pressed", String(on));
      var kind = b.getAttribute("data-kind");
      nodes.forEach(function (n) {
        if (n.d.kind !== kind) return;
        n.hidden = !on;
        n.el.classList.toggle("rz-hidden", !on);
      });
      links.forEach(function (k) { k.el.classList.toggle("rz-hidden", k.a.hidden || k.b.hidden); });
      if (focus && focus.hidden) close();
    });
  });
})();
