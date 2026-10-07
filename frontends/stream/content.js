// content.js: the site version of Particle Stream (builtin/stream). The
// experiment's stream stays as it is; this layer turns it into the site:
//
//   • the stream parts around the current page's words: window.__PHRASES is
//     set per route (the wasm re-reads it every cycle) and __PHRASES_GEN bumped
//     so the words change at once;
//   • the readable content scrolls up over the stream on a dark sheet: the
//     first screen is the stream, everything else is below it;
//   • navigation (site.navigate / site.onRoute), and every field read through
//     site.field, so the observer can fill gaps.
//
// Runs only inside the site (window.site); standalone (trunk serve) the
// experiment's own phrases keep cycling.
(function () {
  "use strict";
  var site = window.site;
  if (!site) return;

  var T = function (key, def) { return typeof site.text === "function" ? site.text(key, def) : def; };
  var F = function (item, name, opts) { return site.field(item, name, opts || { expect: "text" }); };
  var OPT = { expect: "text", optional: true };
  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function link(text, route, cls) {
    var a = el("a", cls, text);
    a.href = "#";
    a.addEventListener("click", function (e) { e.preventDefault(); site.navigate(route); });
    return a;
  }
  function paras(body) {
    return String(body || "").split(/\n\s*\n/).map(function (p) { return p.trim(); }).filter(Boolean);
  }
  var MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  function month(s) {
    var m = /^(\d{4})(?:-(\d{2}))?$/.exec(s || "");
    if (!m) return "";
    return m[2] ? MONTHS[+m[2] - 1] + " " + m[1] : m[1];
  }
  function dates(role) {
    var a = month(F(role, "start", OPT));
    var b = site.field(role, "current", { expect: "bool", optional: true }) ? T("present", "present") : month(F(role, "end", OPT));
    return a && b ? a + " " + T("dateTo", "to") + " " + b : a || b;
  }
  function upper(s) { return String(s || "").toUpperCase(); }

  // ── the words the stream parts around ──
  var gen = 0;
  function setPhrases(list) {
    list = list.map(function (s) { return upper(s).trim(); }).filter(Boolean);
    if (!list.length) list = ["BEN PRIDDY"];
    window.__PHRASES = list;
    window.__PHRASES_GEN = ++gen;
  }

  // ── media ──
  var loopObserver = typeof IntersectionObserver === "function" ? new IntersectionObserver(function (entries) {
    entries.forEach(function (e) {
      var v = e.target;
      if (e.isIntersecting && !reduce) { v.muted = true; var p = v.play(); if (p && p.catch) p.catch(function () {}); }
      else v.pause();
    });
  }, { threshold: 0.2 }) : null;
  function mediaEl(m, alt, eager) {
    var kind = m && m.kind;
    var box = el("figure", "st-media");
    if (m.width && m.height) box.style.aspectRatio = m.width + " / " + m.height;
    if (kind === "loop") {
      var v = el("video");
      v.muted = true; v.loop = true; v.playsInline = true; v.preload = "none";
      v.setAttribute("muted", ""); v.setAttribute("playsinline", "");
      if (m.poster) v.poster = m.poster;
      v.src = m.src;
      v.setAttribute("aria-label", m.alt || alt);
      box.append(v);
      if (loopObserver) loopObserver.observe(v);
    } else if (kind === "image") {
      var img = el("img");
      img.src = m.src;
      img.alt = m.alt || alt;
      img.decoding = "async";
      if (!eager) img.loading = "lazy";
      if (m.width && m.height) { img.width = m.width; img.height = m.height; }
      box.append(img);
    } else {
      return null;
    }
    return box;
  }

  // ── the page ──
  var page = el("div", "st-page");
  page.id = "st-page";
  var nav = el("nav", "st-nav");
  nav.setAttribute("aria-label", "Pages");
  var hero = el("section", "st-hero");
  hero.setAttribute("aria-hidden", "true");
  var cue = el("p", "st-cue");
  hero.append(cue);
  var sheet = el("main", "st-sheet");
  sheet.id = "st-sheet";
  page.append(hero, sheet);
  document.body.append(page, nav);

  function renderNav(route) {
    nav.replaceChildren();
    var items = [["", T("navHome", "Home")], ["work", T("navWork", "Work")], ["experiments", T("navExperiments", "Experiments")]];
    site.pages().forEach(function (p) {
      var slug = F(p, "slug", OPT);
      if (slug && slug !== "work" && slug !== "experiments") items.push([slug, F(p, "title", { expect: "text", fallback: slug })]);
    });
    items.forEach(function (it) {
      var a = link(it[1], it[0], "st-nav-link");
      var current = it[0] === "" ? route === "" : route === it[0] || route.indexOf(it[0] + "/") === 0;
      if (current) a.setAttribute("aria-current", "page");
      nav.append(a);
    });
  }

  function label(text) { return el("p", "st-label", text); }

  function workList(projects, limit) {
    var ol = el("ol", "st-work");
    projects.slice(0, limit || projects.length).forEach(function (p, i) {
      var slug = F(p, "slug", OPT);
      var li = el("li", "st-work-item");
      var a = link("", "work/" + slug, "st-work-link");
      a.append(el("span", "st-work-n", (i < 9 ? "0" : "") + (i + 1)));
      var t = el("span", "st-work-text");
      t.append(el("span", "st-work-title", F(p, "title", { expect: "text", fallback: slug })));
      var meta = [F(p, "client", OPT), F(p, "year", OPT)].filter(Boolean).join(" · ");
      if (meta) t.append(el("span", "st-work-meta", meta));
      a.append(t);
      var media = site.field(p, "media", { expect: "list", optional: true });
      var thumb = media.filter(function (m) { return m && m.kind === "loop"; })[0] || media.filter(function (m) { return m && m.kind === "image"; })[0];
      if (thumb) { var me = mediaEl(thumb, "", false); if (me) { me.classList.add("st-work-thumb"); a.append(me); } }
      li.append(a);
      ol.append(li);
    });
    return ol;
  }

  function renderHome() {
    var home = site.page("");
    var name = F(home, "title", { expect: "text", fallback: "Ben Priddy" });
    var roles = typeof site.experience === "function" ? site.experience() : [];
    var current = roles.filter(function (r) { return site.field(r, "current", { expect: "bool", optional: true }); })[0];
    setPhrases([name, T("tagline", "Creative technology / AI")].concat(current ? [F(current, "role", OPT), F(current, "company", OPT)] : []));

    var head = el("header", "st-head");
    head.append(el("h1", "st-title", name), el("p", "st-tagline", T("tagline", "Creative technology / AI")));
    sheet.append(head);

    var bio = paras(F(home, "body", OPT));
    if (bio.length) {
      var sec = el("section", "st-section st-bio");
      sec.append(label(T("aboutLabel", "About")));
      bio.forEach(function (p, i) { sec.append(el("p", i === 0 ? "st-lede" : "st-body", p)); });
      sheet.append(sec);
    }
    if (roles.length) {
      var xs = el("section", "st-section");
      xs.append(label(T("experienceLabel", "Experience")));
      var ol = el("ol", "st-xp");
      roles.forEach(function (r) {
        var li = el("li", "st-xp-item");
        li.append(el("p", "st-xp-dates", dates(r)));
        var main = el("div", "st-xp-main");
        main.append(el("h3", "st-xp-role", F(r, "role", OPT)), el("p", "st-xp-company", F(r, "company", OPT)));
        var note = F(r, "note", OPT);
        if (note) main.append(el("p", "st-xp-note", note));
        li.append(main);
        ol.append(li);
      });
      xs.append(ol);
      sheet.append(xs);
    }
    var projects = site.projects();
    if (projects.length) {
      var ws = el("section", "st-section");
      ws.append(label(T("selectedWorkLabel", "Selected work")), workList(projects, 6));
      var more = el("p", "st-more");
      more.append(link(T("allWorkLink", "All work") + " →", "work", "st-link"));
      ws.append(more);
      sheet.append(ws);
    }
    sheet.append(experimentsSection());
  }

  function experimentsSection() {
    var sec = el("section", "st-section");
    sec.append(label(T("experimentsTitle", "Experiments")));
    var exps = site.experiments();
    if (!exps.length) {
      sec.append(el("p", "st-lede st-empty", T("experimentsEmpty", "Coming soon.")));
      return sec;
    }
    var ol = el("ol", "st-work");
    exps.forEach(function (x) {
      var li = el("li", "st-work-item st-exp");
      li.append(el("h3", "st-work-title", F(x, "title", { expect: "text", fallback: F(x, "slug", OPT) })));
      var sum = F(x, "summary", OPT);
      if (sum) li.append(el("p", "st-body", sum));
      var media = site.field(x, "media", { expect: "list", optional: true });
      if (media[0]) { var me = mediaEl(media[0], F(x, "title", OPT), false); if (me) li.append(me); }
      var href = F(x, "link", OPT);
      if (href) {
        var b = el("button", "st-link", T("openExperiment", "Open it") + " ↗");
        b.type = "button";
        b.addEventListener("click", function () { site.openExternal(href); });
        li.append(b);
      }
      ol.append(li);
    });
    sec.append(ol);
    return sec;
  }

  function renderWork() {
    var projects = site.projects();
    setPhrases([T("workTitle", "Work")].concat(projects.map(function (p) { return F(p, "title", OPT); })));
    var head = el("header", "st-head");
    head.append(el("h1", "st-title", T("workTitle", "Work")),
      el("p", "st-tagline", T("projectsCount", "{n} projects").replace("{n}", String(projects.length))));
    sheet.append(head);
    var sec = el("section", "st-section");
    sec.append(workList(projects));
    sheet.append(sec);
  }

  function renderProject(slug) {
    var projects = site.projects();
    var i = -1;
    projects.forEach(function (p, k) { if (F(p, "slug", OPT) === slug) i = k; });
    if (i < 0) return renderNotFound();
    var p = projects[i];
    var title = F(p, "title", { expect: "text", fallback: slug });
    var client = F(p, "client", OPT), year = F(p, "year", OPT);
    setPhrases([title, [client, year].filter(Boolean).join(" · ")]);

    var head = el("header", "st-head");
    head.append(el("p", "st-label", (i < 9 ? "0" : "") + (i + 1) + " / " + projects.length), el("h1", "st-title", title));
    sheet.append(head);

    var dl = el("dl", "st-meta");
    [[T("clientLabel", "Client"), client], [T("agencyLabel", "Agency"), F(p, "agency", OPT)], [T("yearLabel", "Year"), year],
     [T("rolesLabel", "Role"), site.field(p, "roles", { expect: "list", optional: true }).join(", ")],
     [T("tagsLabel", "Tags"), site.field(p, "tags", { expect: "list", optional: true }).join(", ")]].forEach(function (row) {
      if (!row[1]) return;
      var d = el("div");
      d.append(el("dt", null, row[0]), el("dd", null, row[1]));
      dl.append(d);
    });
    sheet.append(dl);

    var media = site.field(p, "media", { expect: "list", optional: true });
    var hero = media.filter(function (m) { return m && m.kind === "image"; })[0];
    if (hero) { var hm = mediaEl(hero, title, true); if (hm) { hm.classList.add("st-hero-media"); sheet.append(hm); } }

    [["briefLabel", "Brief", "summary", "st-lede"], ["roleLabel", "Role", "contribution", "st-body"], ["notesLabel", "Notes", "body", "st-body"]].forEach(function (s) {
      var ps = paras(F(p, s[2], OPT));
      if (!ps.length) return;
      var sec = el("section", "st-section");
      sec.append(label(T(s[0], s[1])));
      ps.forEach(function (t) { sec.append(el("p", s[3], t)); });
      sheet.append(sec);
    });

    var links = el("p", "st-links");
    var yt = F(p, "youtube", OPT), live = F(p, "link", OPT);
    if (yt) {
      var fb = el("button", "st-link", T("filmLink", "Watch the film") + " ↗");
      fb.type = "button";
      fb.addEventListener("click", function () { site.openExternal("https://www.youtube.com/watch?v=" + yt); });
      links.append(fb);
    }
    if (live) {
      var lb = el("button", "st-link", T("liveLink", "Visit the work") + " ↗");
      lb.type = "button";
      lb.addEventListener("click", function () { site.openExternal(live); });
      links.append(lb);
    }
    if (links.childNodes.length) sheet.append(links);

    var rest = media.filter(function (m) { return m && m !== hero; });
    if (rest.length) {
      var ms = el("section", "st-section st-gallery");
      rest.forEach(function (m, k) { var me = mediaEl(m, title + " " + (k + 1), false); if (me) ms.append(me); });
      sheet.append(ms);
    }

    if (projects.length > 1) {
      var next = projects[(i + 1) % projects.length];
      var nx = el("nav", "st-next");
      nx.setAttribute("aria-label", T("moreWorkLabel", "More work"));
      nx.append(label(T("nextLabel", "Next")), link(F(next, "title", OPT), "work/" + F(next, "slug", OPT), "st-next-title"),
        link(T("allWorkLink", "All work"), "work", "st-link"));
      sheet.append(nx);
    }
  }

  function renderExperiments() {
    setPhrases([T("experimentsTitle", "Experiments"), T("experimentsEmpty", "Coming soon.")]);
    var head = el("header", "st-head");
    head.append(el("h1", "st-title", T("experimentsTitle", "Experiments")));
    sheet.append(head);
    var sec = experimentsSection();
    sec.firstChild.remove(); // the title is the page's
    sheet.append(sec);
  }

  function renderPage(p) {
    var title = F(p, "title", { expect: "text", fallback: F(p, "slug", OPT) });
    setPhrases([title]);
    var head = el("header", "st-head");
    head.append(el("h1", "st-title", title));
    sheet.append(head);
    var sec = el("section", "st-section");
    paras(F(p, "body", OPT)).forEach(function (t, i) { sec.append(el("p", i === 0 ? "st-lede" : "st-body", t)); });
    sheet.append(sec);
  }

  function renderNotFound() {
    setPhrases([T("notFoundTitle", "Not found")]);
    var head = el("header", "st-head");
    head.append(el("h1", "st-title", T("notFoundTitle", "Not found")));
    var p = el("p", "st-lede", T("notFoundBody", "There's nothing here. "));
    p.append(link(T("navHome", "Home"), "", "st-link"));
    head.append(p);
    sheet.append(head);
  }

  function render(route) {
    route = route || "";
    sheet.replaceChildren();
    renderNav(route);
    cue.textContent = T("scrollCue", "Scroll");
    if (route === "") renderHome();
    else if (route === "work") renderWork();
    else if (route.indexOf("work/") === 0) renderProject(route.slice(5));
    else if (route === "experiments") renderExperiments();
    else {
      var p = site.page(route);
      if (p && F(p, "slug", OPT) === route) renderPage(p);
      else renderNotFound();
    }
    page.scrollTop = 0;
  }

  site.loaded.then(function (init) {
    try {
      site.theme("dark");
      render(init.route);
      site.onRoute(render);
    } catch (e) {
      site.reportError(e);
    }
  });
})();
