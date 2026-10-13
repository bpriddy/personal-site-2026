// /resume (v1.15): Ben's career as a space you travel through. The graph
// (Ben, his roles, their projects, the themes those touch, and his skills as
// two constellations) comes from the page as JSON. Each node gets a place in
// 3D: Ben at the entrance, then his roles receding back in time, newest
// nearest, each role's projects around it, themes at the depth of the work
// they gather, and the skills floating near the work they belong to.
//
// Rendering is hand-written and dependency-free: a perspective camera
// projects every node onto the screen; nodes are HTML buttons (crisp text,
// focusable, clickable) moved with CSS transforms, and the links and drifting
// dust are drawn on one canvas behind them. Exponential (squared) distance
// fog fades far things into the background colour.
//
// The page scroll drives the camera along a path through the space (the stage
// is sticky while its tall section scrolls by); dragging looks around a
// little; a click flies to a node and opens it in the panel; hover, focus or
// tap trace its connections. Reduced motion: no flight, no sway, no
// scroll-driven travel: a still composition, and choosing a node or a year
// cuts to it.
(function () {
  "use strict";
  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

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
      if (s.getBoundingClientRect().top > window.innerHeight) { s.classList.add("rz-pre"); io.observe(s); }
    });
  })();

  var sys = document.getElementById("rz-system");
  var travel = document.getElementById("rz-travel");
  var stage = document.getElementById("rz-stage");
  var dataEl = document.getElementById("rz-graph");
  if (!sys || !travel || !stage || !dataEl) return;
  var data;
  try { data = JSON.parse(dataEl.textContent); } catch (e) { return; }
  if (!data || !data.nodes || !data.nodes.length) return;

  var KIND_LABEL = { me: "", role: "Role", project: "Project", theme: "Theme", skill: "Skill" };
  // dot diameters in CSS px at the reference distance
  // below this scale a small thing's label waits (until it's traced)
  var QUIET = { project: 0.42, theme: 0.3, skill: 0.4 };
  var DOT = { me: 150, role: 18, project: 12, theme: 10, skill: 8, hub: 12 };

  // ── the graph ──
  var nodes = data.nodes.map(function (d, i) {
    return { d: d, i: i, kind: d.kind, hub: !!d.hub, x: 0, y: 0, z: 0, nb: [], links: [], hidden: false,
      dim: 1, dimT: 1, op: -1, on: false, xc: 0, yc: 0, zc: 1, sx: 0, sy: 0, s: 1 };
  });
  var byId = {};
  nodes.forEach(function (n) { byId[n.d.id] = n; });
  var links = [];
  (data.links || []).forEach(function (l) {
    var a = byId[l[0]], b = byId[l[1]];
    if (!a || !b) return;
    var k = { a: a, b: b, constel: a.hub || b.hub, on: false };
    links.push(k);
    a.nb.push(b); b.nb.push(a);
    a.links.push(k); b.links.push(k);
  });
  var me = byId.me || nodes[0];
  var roles = nodes.filter(function (n) { return n.kind === "role"; });
  var themes = nodes.filter(function (n) { return n.kind === "theme"; });
  var hubs = nodes.filter(function (n) { return n.hub; });

  sys.classList.add("rz-3d");
  if (reduce) sys.classList.add("rz-still");

  // ── DOM: canvas (links, dust) under a layer of node buttons ──
  var canvas = document.createElement("canvas");
  canvas.className = "rz-canvas";
  canvas.setAttribute("aria-hidden", "true");
  var ctx = canvas.getContext("2d");
  var layer = document.createElement("div");
  layer.className = "rz-nodes";
  layer.setAttribute("role", "group");
  layer.setAttribute("aria-label", "Ben Priddy's roles, projects, themes and skills, nearest first");
  stage.insertBefore(layer, stage.firstChild);
  stage.insertBefore(canvas, layer);

  function span(cls, text) {
    var e = document.createElement("span");
    e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  nodes.forEach(function (n) {
    var d = n.d;
    var b = document.createElement("button");
    b.type = "button";
    b.className = "rz-node";
    b.setAttribute("data-kind", d.kind);
    b.setAttribute("data-id", d.id);
    if (n.hub) b.setAttribute("data-hub", "");
    if (d.group) b.setAttribute("data-group", d.group);
    var what = n.hub ? "Skills" : KIND_LABEL[d.kind];
    b.setAttribute("aria-label", (what ? what + ": " : "") + d.label + (d.sub && !n.hub ? ", " + d.sub : "") + (d.kind === "me" ? ", profile" : ""));
    var dot = span("rz-d");
    if (d.kind === "me" && d.img) {
      var img = document.createElement("img");
      img.alt = "";
      img.decoding = "async";
      img.width = img.height = 240;
      if (d.imgset) { img.srcset = d.imgset; img.sizes = "200px"; }
      img.src = d.img;
      dot.append(img);
    }
    b.append(dot);
    var t = null;
    if (d.kind !== "me") {
      t = span("rz-t");
      t.append(span("rz-t1", d.label));
      // a role names its company and years under its title (two are "Technical Director")
      if (d.kind === "role" && d.sub) t.append(span("rz-t2", d.sub));
      if (d.kind === "project" && d.year) t.append(span("rz-t2", String(d.year)));
      b.append(t);
    }
    n.el = b; n.dot = dot; n.txt = t;
    n.base = n.hub ? DOT.hub : DOT[d.kind] || 8;
  });
  // tab order: nearest first (the order they're laid out in depth)
  function appendInDepthOrder() {
    nodes.slice().sort(function (a, b) { return a.z - b.z; }).forEach(function (n) { layer.appendChild(n.el); });
  }

  // the years: a rail to travel by, one stop per role
  var rail = document.createElement("nav");
  rail.className = "rz-rail";
  rail.setAttribute("aria-label", "Travel to a year");
  var railMark = span("rz-rail-mark");
  rail.append(railMark);
  roles.forEach(function (r) {
    var b = document.createElement("button");
    b.type = "button";
    var company = String(r.d.sub || "").split(" · ")[0];
    b.append(span("rz-rail-y", r.d.year ? String(r.d.year) : ""), span("rz-rail-c", company));
    b.setAttribute("aria-label", r.d.label + ", " + (r.d.sub || ""));
    b.addEventListener("click", function () { flyTo(r, false); });
    r.railEl = b;
    rail.append(b);
  });
  stage.append(rail);
  // a phone names the role you're at in a caption (CSS shows it on narrow screens)
  var caption = span("rz-caption");
  caption.setAttribute("aria-hidden", "true");
  var capMeta = span("rz-caption-meta"), capTitle = span("rz-caption-title");
  caption.append(capMeta, capTitle);
  stage.append(caption);
  var captionFor = null;
  var hero = stage.querySelector(".rz-hero");
  var hint = document.getElementById("rz-hint");

  // ── the space ──
  var W = 0, H = 0, F = 800, U = 800, VIEW = 600, narrow = false, dpr = 1;
  var zStart = 0, zEnd = 1, path = [], dust = [];
  var nearRole = null; // the role nearest the camera (the last frame's)
  var depthMax = 1;    // the deepest node (the fly-in reveal reaches it)
  var heroUp = true;   // the title is still on screen (the last frame's)
  function smooth(e0, e1, x) { var t = Math.min(1, Math.max(0, (x - e0) / (e1 - e0))); return t * t * (3 - 2 * t); }
  function clamp(x, a, b) { return x < a ? a : x > b ? b : x; }

  function layout() {
    W = Math.max(300, stage.clientWidth);
    H = Math.max(360, stage.clientHeight);
    narrow = W < 760;
    // the phone caption sits just under the legend, wherever that ends up
    var lg = document.querySelector(".rz-legend");
    if (lg) caption.style.top = Math.round(lg.getBoundingClientRect().bottom - stage.getBoundingClientRect().top + 14) + "px";
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    canvas.width = Math.round(W * dpr); canvas.height = Math.round(H * dpr);
    F = clamp(H * 0.95, 520, 1000); // focal length, px
    U = F;                           // a world unit: what spans the screen at the reference distance
    VIEW = 0.75 * U;                 // the distance a node is looked at from (its dots at their CSS size)
    var rx = function (k) { return k * W * 0.75; }, ry = function (k) { return k * H * 0.75; };

    // Ben at the entrance, to the right of the title (under it on a phone)
    me.x = narrow ? 0 : 0.17 * W; me.y = narrow ? 0.09 * H : 0.02 * H; me.z = 0;
    // roles: down the corridor, newest first, each a little off the axis
    roles.forEach(function (r, i) {
      var a = 2.6 + i * 2.2;
      // on a phone roles sit further apart in depth, so two are never side by side
      r.z = 1.3 * U + i * (narrow ? 2.1 : 1.2) * U;
      r.x = rx(narrow ? 0.1 : 0.17) * Math.cos(a); r.y = ry(0.12) * Math.sin(a); r.ang = a;
    });
    // projects: a half ring around their role, newest nearest
    var placed = {};
    roles.forEach(function (r) {
      var ps = r.nb.filter(function (m) { return m.kind === "project"; })
        .sort(function (a, b) { return (b.d.year || 0) - (a.d.year || 0); });
      var n = ps.length, spread = narrow ? 0.95 : 0.8;
      ps.forEach(function (p, j) {
        var ang = r.ang + Math.PI + (j - (n - 1) / 2) * spread;
        p.x = r.x + rx(narrow ? 0.3 : 0.34) * Math.cos(ang);
        p.y = r.y + ry(narrow ? 0.36 : 0.36) * Math.sin(ang);
        p.z = r.z + 0.12 * U + j * (0.75 * U / Math.max(n, 2));
        placed[p.d.id] = true;
      });
    });
    var lastZ = roles.length ? roles[roles.length - 1].z : 1.3 * U;
    var orphans = nodes.filter(function (n) { return n.kind === "project" && !placed[n.d.id]; });
    orphans.forEach(function (p, j) {
      var ang = j * 2.4;
      p.x = rx(0.3) * Math.cos(ang); p.y = ry(0.25) * Math.sin(ang); p.z = lastZ + 0.9 * U + j * 0.15 * U;
    });
    // themes: on an outer ring, at the depth of the work they gather
    themes.forEach(function (t) {
      var ps = t.nb.filter(function (m) { return m.kind === "project"; });
      t.mz = ps.length ? ps.reduce(function (s, m) { return s + m.z; }, 0) / ps.length : lastZ;
    });
    themes.slice().sort(function (a, b) { return a.mz - b.mz; }).forEach(function (t, i) {
      var ang = 0.4 + i * 2.39996; // the golden angle keeps neighbours apart
      t.x = rx(narrow ? 0.34 : 0.46) * Math.cos(ang);
      t.y = ry(narrow ? 0.36 : 0.46) * Math.sin(ang);
      t.z = t.mz + 0.25 * U;
    });
    // the two constellations: a hub, its skills in a loose ring around it
    hubs.forEach(function (h) {
      var items = h.nb.filter(function (m) { return m.kind === "skill" && !m.hub; });
      if (h.d.group === "do") {
        h.z = narrow ? 1.0 * U : 0.6 * U;
        h.x = rx(narrow ? -0.12 : -0.34); h.y = ry(narrow ? -0.3 : -0.24);
      } else {
        // what he reviews stands at the entrance too, its links reaching
        // back through the years to the work that used it
        h.z = narrow ? 1.2 * U : 0.95 * U;
        h.x = rx(narrow ? 0.1 : 0.3); h.y = ry(narrow ? 0.34 : -0.4);
      }
      var m = items.length, R = h.d.group === "do" ? 0.14 : 0.21;
      items.forEach(function (s, j) {
        var ang = -Math.PI / 2 + (j + 0.5) * 2 * Math.PI / m;
        s.x = h.x + rx(narrow ? R * 1.15 : R) * Math.cos(ang);
        s.y = h.y + ry(R * (narrow ? 0.7 : 0.95)) * Math.sin(ang);
        s.z = h.z + (j % 2 ? 0.12 : -0.08) * U + (j % 3) * 0.05 * U;
        s.sideLeft = Math.cos(ang) < -0.05; // named away from the hub
      });
    });

    // the camera's path: from the entrance (Ben on the right of the title,
    // or under it on a phone), past each role, to the oldest work
    var maxZ = 0;
    nodes.forEach(function (n) { if (n.z > maxZ) maxZ = n.z; });
    depthMax = maxZ;
    zStart = -VIEW;
    // the end: the oldest role's work around you
    zEnd = Math.max(zStart + U, Math.min(maxZ - 0.55 * U, (roles.length ? roles[roles.length - 1].z - VIEW : 0) + 0.25 * U));
    path = [{ z: zStart, x: 0, y: narrow ? -0.04 * H : 0 }];
    roles.forEach(function (r) { path.push({ z: r.z - VIEW, x: r.x * 0.6, y: r.y * 0.6 }); });
    path.push({ z: zEnd + 1, x: path[path.length - 1].x, y: path[path.length - 1].y });
    path.sort(function (a, b) { return a.z - b.z; });

    // dust: a sense of speed and depth
    var count = narrow ? 110 : 240, seed = 7;
    var rnd = function () { seed = (seed * 16807) % 2147483647; return seed / 2147483647; };
    dust = [];
    for (var i = 0; i < count; i++) {
      dust.push({ x: (rnd() - 0.5) * W * 2.2, y: (rnd() - 0.5) * H * 2.2, z: -VIEW + rnd() * (maxZ + 2 * U), r: 0.6 + rnd() * 1.1 });
    }

    // the scroll that drives it: a screen per role or so
    if (!reduce) {
      var scrollLen = clamp((zEnd - zStart) * 0.42, 1400, 4200);
      travel.style.height = Math.round(H + scrollLen) + "px";
    }
    stops = [zStart];
    roles.forEach(function (r) { var z = clamp(r.z - VIEW, zStart, zEnd); if (z > stops[stops.length - 1] + 1) stops.push(z); });
    if (zEnd > stops[stops.length - 1] + 1) stops.push(zEnd);
    if (stops.length < 2) stops.push(zStart + 1);
    roles.forEach(function (r) {
      r.railEl.style.top = (8 + 84 * progressOf(r.z - VIEW)) + "%";
    });
    appendInDepthOrder();
  }
  // scroll and depth: each stretch between stops (the entrance, each role's
  // viewpoint, the end) gets an equal share of the scroll, eased so the
  // camera lingers at a stop and moves on between them
  var stops = [];
  function ease(t) { return 0.3 * t + 0.7 * t * t * (3 - 2 * t); }
  function zAt(p) {
    var n = stops.length - 1, x = clamp(p, 0, 1) * n, i = Math.min(n - 1, Math.floor(x));
    return stops[i] + (stops[i + 1] - stops[i]) * ease(x - i);
  }
  function progressOf(z) {
    var n = stops.length - 1;
    if (z <= stops[0]) return 0;
    for (var i = 0; i < n; i++) {
      if (z <= stops[i + 1]) {
        var want = (z - stops[i]) / (stops[i + 1] - stops[i]), lo = 0, hi = 1;
        for (var k = 0; k < 24; k++) { var mid = (lo + hi) / 2; if (ease(mid) < want) lo = mid; else hi = mid; }
        return (i + (lo + hi) / 2) / n;
      }
    }
    return 1;
  }
  function pathAt(z) {
    if (z <= path[0].z) return path[0];
    for (var i = 1; i < path.length; i++) {
      if (z <= path[i].z) {
        var a = path[i - 1], b = path[i], t = smooth(a.z, b.z, z);
        return { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t };
      }
    }
    return path[path.length - 1];
  }

  // ── the camera ──
  var cam = { x: 0, y: 0, z: 0, yaw: 0, pitch: 0 };
  var look = { yaw: 0, pitch: 0 };   // where dragging points it
  var stillZ = null;                 // reduced motion: where a cut put it
  var focus = null, hover = null;
  var travelTop = 0, scrollRange = 1;
  function measure() {
    travelTop = travel.getBoundingClientRect().top + window.scrollY;
    scrollRange = Math.max(1, travel.offsetHeight - H);
  }
  function scrollProgress() { return reduce ? 0 : clamp((window.scrollY - travelTop) / scrollRange, 0, 1); }
  function target() {
    var z = reduce ? (stillZ == null ? zStart : stillZ) : zAt(scrollProgress());
    var p = pathAt(z), x = p.x, y = p.y;
    if (focus && !focus.hidden) {
      // frame the open node beside the panel (above it on a phone), while
      // the camera is near its depth
      var fx = narrow ? focus.x : focus.x - (0.36 * W - W / 2) * VIEW / F;
      var fy = narrow ? focus.y - (0.24 * H - H / 2) * VIEW / F : focus.y;
      var w = 1 - smooth(0.25 * U, 0.9 * U, Math.abs(z - (focus.z - VIEW)));
      x += (fx - x) * w; y += (fy - y) * w;
    }
    return { x: x, y: y, z: z };
  }

  // ── colours, from the house tokens (they change with the theme) ──
  var col = {};
  function readColours() {
    var cs = getComputedStyle(stage);
    col.fg = cs.getPropertyValue("--fg").trim() || "#ECE8DF";
    col.fg3 = cs.getPropertyValue("--fg-3").trim() || "#6E6A62";
    col.accent = cs.getPropertyValue("--accent").trim() || "#FF5B1F";
    wake();
  }

  // ── one frame ──
  var NEAR = 0;
  var t0 = performance.now(), last = t0, intro = reduce ? 1 : 0;
  function frame(now) {
    var dt = Math.min(0.05, (now - last) / 1000);
    last = now;
    var tg = target();
    var moving = false;
    if (reduce) {
      cam.x = tg.x; cam.y = tg.y; cam.z = tg.z; cam.yaw = 0; cam.pitch = 0;
    } else {
      intro = Math.min(1, (now - t0) / 1600);
      var ease = 1 - Math.pow(1 - intro, 3);
      var k = 1 - Math.exp(-dt * 7);
      var tz = tg.z - (1 - ease) * 0.9 * U; // the way in: from further out
      cam.x += (tg.x - cam.x) * k; cam.y += (tg.y - cam.y) * k; cam.z += (tz - cam.z) * k;
      var kl = 1 - Math.exp(-dt * 5);
      cam.yaw += (look.yaw - cam.yaw) * kl; cam.pitch += (look.pitch - cam.pitch) * kl;
      moving = intro < 1 || Math.abs(tz - cam.z) > 0.5 || Math.abs(tg.x - cam.x) > 0.5 || Math.abs(tg.y - cam.y) > 0.5 ||
        Math.abs(look.yaw - cam.yaw) > 0.0005 || Math.abs(look.pitch - cam.pitch) > 0.0005;
    }
    NEAR = 0.16 * U;
    var cy = Math.cos(cam.yaw), sy = Math.sin(cam.yaw), cp = Math.cos(cam.pitch), sp = Math.sin(cam.pitch);
    // a phone's roles sit further apart, so its fog reaches further
    var fog0 = 0.55 * U, fogD = (narrow ? 3.8 : 2.6) * U, secs = (now - t0) / 1000;
    function toCam(px, py, pz, out) {
      var x = px - cam.x, y = py - cam.y, z = pz - cam.z;
      var x1 = x * cy - z * sy, z1 = x * sy + z * cy;
      out.x = x1; out.y = y * cp - z1 * sp; out.z = y * sp + z1 * cp;
      return out;
    }
    function fogOf(z) { var d = Math.max(0, z - fog0) / fogD; return Math.exp(-d * d); }
    var tracing = !!(hover || focus);
    var c = { x: 0, y: 0, z: 0 };

    // nodes
    for (var i = 0; i < nodes.length; i++) {
      var n = nodes[i];
      if (n.hidden) continue;
      var ox = 0, oy = 0;
      if (!reduce && n.kind !== "me") {
        ox = Math.sin(secs * 0.5 + n.i * 1.7) * 0.007 * U;
        oy = Math.cos(secs * 0.4 + n.i * 2.3) * 0.007 * U;
      }
      toCam(n.x + ox, n.y + oy, n.z, c);
      n.xc = c.x; n.yc = c.y; n.zc = c.z;
      n.dim += (n.dimT - n.dim) * (reduce ? 1 : 1 - Math.exp(-dt * 10));
      if (Math.abs(n.dimT - n.dim) > 0.01) moving = true;
      var op = 0;
      if (c.z > NEAR) {
        n.s = VIEW / c.z;
        n.sx = W / 2 + F * c.x / c.z;
        n.sy = H / 2 + F * c.y / c.z;
        n.fog = fogOf(c.z);
        var nearFade = smooth(NEAR, 0.42 * U, c.z);
        // the fly-in reveals the space front to back, all the way to its deepest node
        var appear = reduce ? 1 : smooth(0, 1, (intro * 1.6) - Math.max(0, n.z) / Math.max(6 * U, depthMax * 1.05));
        op = n.fog * nearFade * appear;
        if (n.sx < -400 || n.sx > W + 400 || n.sy < -300 || n.sy > H + 300) op = 0;
      }
      n.vis = op;
      op *= n.dim;
      if (op < 0.015) {
        if (n.op !== 0) { n.op = 0; n.el.style.opacity = "0"; n.el.classList.add("rz-gone"); }
        continue;
      }
      if (n.op === 0) n.el.classList.remove("rz-gone");
      n.op = op;
      var s = clamp(n.s, 0.12, 3.2);
      n.el.style.opacity = op.toFixed(3);
      n.el.style.transform = "translate(" + n.sx.toFixed(1) + "px," + n.sy.toFixed(1) + "px)";
      n.el.style.zIndex = String(Math.max(1, 100000 - Math.round(c.z)));
      n.dot.style.transform = "translate(-50%,-50%) scale(" + s.toFixed(3) + ")";
      if (n.txt) {
        var ls = clamp(n.s, narrow ? 0.86 : 0.8, narrow ? 1.05 : 1.3);
        var gap = (n.base / 2) * s + 8;
        var left = !n.hub && (n.sideLeft != null ? n.sideLeft : n.sx > W * (narrow ? 0.5 : 0.64));
        if (left !== n.left) { n.left = left; n.el.classList.toggle("rz-left", left); }
        // a constellation's name sits over it; everything else is named beside its dot
        n.txt.style.transform = n.hub
          ? "translate(-50%,calc(-100% - " + gap.toFixed(1) + "px)) scale(" + ls.toFixed(3) + ")"
          : left
          ? "translate(calc(-100% - " + gap.toFixed(1) + "px),-50%) scale(" + ls.toFixed(3) + ")"
          : "translate(" + gap.toFixed(1) + "px,-50%) scale(" + ls.toFixed(3) + ")";
        // small things are named when they're near enough to read, or traced
        var quiet = !n.on && (n.kind === "role" || n.hub ? n.s < 0.3 : n.fog < 0.35 || n.s < QUIET[n.kind] * (narrow ? 1.2 : 1));
        // a phone names only the role you're at; the others wait for a tap
        // a phone names roles in its caption instead, unless one is traced
        if (narrow && n.kind === "role" && !n.on) quiet = true;
        // and at the entrance, while the title is up, only Ben's photo shows
        if (narrow && heroUp && !n.on && n.kind !== "me") quiet = true;
        if (quiet !== n.quiet) { n.quiet = quiet; n.el.classList.toggle("rz-quiet", quiet); }
      }
    }

    // canvas: dust, then links
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, W, H);
    ctx.fillStyle = col.fg3;
    var appearAll = reduce ? 1 : smooth(0, 1, intro * 1.4);
    for (var j = 0; j < dust.length; j++) {
      var p = dust[j];
      toCam(p.x, p.y, p.z, c);
      if (c.z < NEAR * 0.5) continue;
      var a = fogOf(c.z) * smooth(NEAR * 0.5, 0.6 * U, c.z) * 0.55 * appearAll;
      if (a < 0.02) continue;
      var px = W / 2 + F * c.x / c.z, py = H / 2 + F * c.y / c.z;
      if (px < 0 || px > W || py < 0 || py > H) continue;
      var r = clamp(p.r * VIEW / c.z, 0.5, 3);
      ctx.globalAlpha = a;
      ctx.fillRect(px - r / 2, py - r / 2, r, r);
    }
    var ca = { x: 0, y: 0, z: 0 }, cb = { x: 0, y: 0, z: 0 };
    ctx.lineCap = "round";
    for (var l = 0; l < links.length; l++) {
      var k2 = links[l], A = k2.a, B = k2.b;
      if (A.hidden || B.hidden) continue;
      ca.x = A.xc; ca.y = A.yc; ca.z = A.zc; cb.x = B.xc; cb.y = B.yc; cb.z = B.zc;
      if (ca.z < NEAR && cb.z < NEAR) continue;
      // clip at the near plane
      if (ca.z < NEAR || cb.z < NEAR) {
        var inA = ca.z >= NEAR, from = inA ? ca : cb, to = inA ? cb : ca;
        var t = (NEAR - from.z) / (to.z - from.z);
        to = { x: from.x + (to.x - from.x) * t, y: from.y + (to.y - from.y) * t, z: NEAR };
        if (inA) cb = to; else ca = to;
      }
      var fa = A.zc > NEAR ? A.vis : 0, fb = B.zc > NEAR ? B.vis : 0;
      var base = Math.sqrt(fa * fb); // a link fades with its fainter end
      if (base < 0.02 && !k2.on) continue;
      var alpha, width = 1, colour = col.fg3;
      if (tracing) {
        if (k2.on) { colour = col.accent; alpha = Math.max(0.35, base) * 0.95; width = 1.6; }
        else alpha = base * 0.06;
      } else {
        alpha = base * (k2.constel ? 0.55 : 0.42);
      }
      if (alpha < 0.01) continue;
      ctx.globalAlpha = alpha;
      ctx.strokeStyle = colour;
      ctx.lineWidth = width;
      ctx.setLineDash(k2.constel ? [2, 5] : []);
      ctx.beginPath();
      ctx.moveTo(W / 2 + F * ca.x / ca.z, H / 2 + F * ca.y / ca.z);
      ctx.lineTo(W / 2 + F * cb.x / cb.z, H / 2 + F * cb.y / cb.z);
      ctx.stroke();
    }
    ctx.setLineDash([]);
    ctx.globalAlpha = 1;

    // the title leaves as the journey starts; the rail marks where you are
    var prog = reduce ? progressOf(cam.z) : scrollProgress();
    var heroOp = 1 - smooth(0.0, 0.06, prog);
    heroUp = heroOp > 0.4;
    if (hero && heroOp !== hero.lastOp) {
      hero.lastOp = heroOp;
      hero.style.opacity = heroOp.toFixed(3);
      hero.style.transform = "translateY(" + (-(1 - heroOp) * 24).toFixed(1) + "px)";
      hero.classList.toggle("rz-gone", heroOp < 0.05);
    }
    railMark.style.top = (8 + 84 * prog).toFixed(2) + "%";
    var nearest = null, best = Infinity;
    roles.forEach(function (r) { var d = Math.abs(r.z - VIEW - cam.z); if (d < best) { best = d; nearest = r; } });
    roles.forEach(function (r) { r.railEl.classList.toggle("rz-here", r === nearest && best < 0.6 * U); });
    nearRole = nearest;
    var capRole = narrow && !heroUp && nearest && best < 0.9 * U ? nearest : null;
    if (capRole !== captionFor) {
      captionFor = capRole;
      caption.classList.toggle("rz-on", !!capRole);
      if (capRole) {
        var parts = String(capRole.d.sub || "").split(" \u00b7 ");
        capMeta.textContent = (capRole.d.year ? capRole.d.year + " \u00b7 " : "") + (parts[0] || "");
        capTitle.textContent = capRole.d.label;
      }
    }
    var cz = String(Math.round(cam.z - zStart));
    if (stage.dataset.depth !== cz) stage.dataset.depth = cz;
    return moving;
  }

  // ── the loop: runs while the stage is on screen and something moves ──
  var running = false, inView = true;
  function loop(now) {
    var moving = frame(now);
    if (inView && (!reduce || moving)) requestAnimationFrame(loop);
    else running = false;
  }
  function wake() { if (!running && W) { running = true; last = performance.now(); requestAnimationFrame(loop); } }

  layout();
  measure();
  readColours();
  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (es) {
      inView = es[es.length - 1].isIntersecting;
      if (inView) wake();
    }).observe(stage);
  }
  var resizeTimer = 0;
  function onResize() {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () { layout(); measure(); wake(); }, 60);
  }
  if ("ResizeObserver" in window) new ResizeObserver(onResize).observe(stage);
  else window.addEventListener("resize", onResize);
  window.addEventListener("scroll", wake, { passive: true });
  if (window.matchMedia) {
    var mq = window.matchMedia("(prefers-color-scheme: light)");
    if (mq.addEventListener) mq.addEventListener("change", readColours);
  }
  new MutationObserver(readColours).observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });

  // ── travel: fly (scroll) to a node, or cut to it ──
  function flyTo(n, keepScroll) {
    var z = clamp(n.z - VIEW, zStart, zEnd);
    if (reduce) { stillZ = z; wake(); return; }
    if (keepScroll) return;
    measure();
    var top = travelTop + progressOf(z) * scrollRange;
    window.scrollTo({ top: Math.round(top), behavior: "smooth" });
    wake();
  }

  // ── tracing and opening ──
  function trace(n) {
    var tracing = !!n;
    nodes.forEach(function (m) { m.on = false; });
    links.forEach(function (k) { k.on = false; });
    if (n) {
      n.on = true;
      n.nb.forEach(function (m) { m.on = true; });
      n.links.forEach(function (k) { k.on = true; });
    }
    nodes.forEach(function (m) {
      m.dimT = !tracing || m.on ? 1 : 0.16;
      m.el.classList.toggle("rz-on", tracing && m.on);
      m.el.classList.toggle("rz-focus", m === n);
    });
    wake();
  }

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
    flyTo(n);
    var d = n.d;
    pKind.textContent = n.hub ? d.sub : d.kind === "skill" ? d.sub : d.kind === "me" ? "Profile" : KIND_LABEL[d.kind];
    pTitle.textContent = d.label;
    pSub.textContent = d.kind === "skill" ? "" : d.sub || "";
    pSub.hidden = !pSub.textContent;
    pBody.replaceChildren();
    // a role's one-line note only when it has no bullets (they say it better)
    if (!(d.kind === "role" && d.bullets && d.bullets.length)) {
      String(d.text || "").split(/\n\s*\n/).forEach(function (p) { if (p.trim()) pBody.append(el("p", null, p.trim())); });
    }
    if (d.bullets && d.bullets.length && !n.hub) {
      var ul = el("ul");
      d.bullets.forEach(function (b) { ul.append(el("li", null, b)); });
      pBody.append(ul);
    }
    if (d.kind === "theme") {
      var count = n.nb.filter(function (m) { return m.kind === "project"; }).length;
      pBody.append(el("p", null, count + (count === 1 ? " project" : " projects")));
    }
    if (d.kind === "skill" && !n.hub) {
      var others = n.nb.filter(function (m) { return !m.hub; }).length;
      pBody.append(el("p", "rz-panel-note", others ? "Where it shows up in the work:" : "One of the things I can " + (d.group === "do" ? "do" : "review") + "."));
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
      var items = n.nb.filter(function (m) { return m.kind === g[0] && !m.hidden; });
      if (!items.length) return;
      var wrap = el("div");
      wrap.append(el("h3", null, g[0] === "skill" && n.hub ? "Each one" : g[1] || "Ben"));
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

  nodes.forEach(function (n) {
    n.el.addEventListener("pointerenter", function (e) { if (e.pointerType === "mouse" && !drag) { hover = n; trace(n); } });
    n.el.addEventListener("pointerleave", function (e) { if (e.pointerType === "mouse" && !drag) { hover = null; trace(focus); } });
    n.el.addEventListener("focus", function () {
      hover = n; trace(n);
      // the keyboard travels too: tabbing to something takes you to it
      if (n.el.matches(":focus-visible") && focus !== n) flyTo(n);
    });
    n.el.addEventListener("blur", function () { hover = null; trace(focus); });
    n.el.addEventListener("click", function (e) {
      if (dragged) { e.preventDefault(); return; }
      open(n);
    });
  });

  // ── look around: drag the space (horizontal only on touch: vertical scrolls) ──
  var drag = null, dragged = false;
  stage.addEventListener("pointerdown", function (e) {
    if (reduce || e.button > 0) return;
    if (e.target.closest(".rz-panel, .rz-legend, .rz-rail, .rz-hero a")) return;
    drag = { x: e.clientX, y: e.clientY, id: e.pointerId, touch: e.pointerType !== "mouse" };
    dragged = false;
  });
  window.addEventListener("pointermove", function (e) {
    if (!drag || e.pointerId !== drag.id) return;
    var dx = e.clientX - drag.x, dy = e.clientY - drag.y;
    if (!dragged && Math.hypot(dx, dy) < 6) return;
    dragged = true;
    look.yaw = clamp(-dx * 0.0022, -0.38, 0.38);
    look.pitch = drag.touch ? 0 : clamp(dy * 0.0022, -0.24, 0.24);
    wake();
  });
  function endDrag(e) {
    if (!drag || (e && e.pointerId !== drag.id)) return;
    var was = dragged;
    drag = null;
    look.yaw = look.pitch = 0; // it eases back
    wake();
    // a click on empty space (not a drag) closes the panel
    if (!was && e && e.type === "pointerup" && focus && !e.target.closest(".rz-node, .rz-panel, .rz-legend, .rz-rail")) close();
    setTimeout(function () { dragged = false; }, 0);
  }
  window.addEventListener("pointerup", endDrag);
  window.addEventListener("pointercancel", endDrag);

  // ── legend: show or hide a kind ──
  [].forEach.call(stage.querySelectorAll(".rz-legend button"), function (b) {
    b.addEventListener("click", function () {
      var on = b.getAttribute("aria-pressed") !== "true";
      b.setAttribute("aria-pressed", String(on));
      var kind = b.getAttribute("data-kind");
      nodes.forEach(function (n) {
        if (n.kind !== kind) return;
        n.hidden = !on;
        n.el.classList.toggle("rz-hidden", !on);
        if (!on) n.op = -1;
      });
      if (focus && focus.hidden) close();
      wake();
    });
  });
  if (hint && reduce) hint.textContent = "Choose a year, or anything in the space, to go to it and open it. Hover or tap to trace its connections.";
})();
