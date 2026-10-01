// build-modal.js: the public builder, as a modal on the site itself
// (docs/frontend-protocol.md, v1.2 "public builder"; loaded by the public
// shell after frontend-host.js and builder-stream.js).
//
// The corner button (#make-own, drawn by frontend-host.js), the transcript's
// .make-own-link and ?build=1 (where the old /build pages redirect) open it.
// Inside: a prompt and Build, the run's progress (builder-stream.js, the same
// SSE stream as the admin), and "Your creations": the session's front ends and
// their revisions. The only preview is the live site: picking a revision (or a
// run finishing one) sets the fe_live cookie (POST /build/api/fe/<slug>/live)
// and reloads the page's own front-end iframe in place (siteHost.reload). Exit
// goes back to the normal site the same way.
//
// The modal is a dialog (role=dialog, aria-modal, focus trapped, Esc / close
// button / a click outside close it; focus returns to whatever opened it).
// While a build streams, closing it minimizes it to a small pill instead, so
// the visitor can watch the site change behind it.
(function () {
  "use strict";

  var API = "/build/api";
  var POLL_MS = 5000;
  var root = document.documentElement;

  var state = null; // GET /build/api/frontends
  var loading = null; // the in-flight state fetch
  var mode = { kind: "new", slug: "", rev: "" }; // what Build does: a new front end, or change slug's rev
  var busy = null; // {slug, title} while a run streams
  var isOpen = false;
  var opener = null;
  var pollTimer = 0;
  var inerted = [];

  // ── DOM ──
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function keyed(e, key) {
    e.setAttribute("data-key", key);
    return e;
  }
  function button(cls, text, onClick) {
    var b = el("button", cls, text);
    b.type = "button";
    if (onClick) b.addEventListener("click", onClick);
    return b;
  }

  var wrap = el("div", "bm-root");
  wrap.id = "build-modal";
  wrap.hidden = true;
  var backdrop = el("div", "bm-backdrop");
  var dialog = el("div", "bm-dialog");
  dialog.setAttribute("role", "dialog");
  dialog.setAttribute("aria-modal", "true");
  dialog.setAttribute("aria-labelledby", "bm-title");
  dialog.setAttribute("aria-describedby", "bm-desc");
  dialog.tabIndex = -1;

  var head = el("div", "bm-head");
  var title = el("h2", "bm-title", "Make your own version");
  title.id = "bm-title";
  var minBtn = button("bm-icon bm-minimize", "", function () { minimize(); });
  minBtn.setAttribute("aria-label", "Minimize while it builds");
  minBtn.title = "Minimize while it builds";
  minBtn.append(iconLine());
  minBtn.hidden = true;
  var closeBtn = button("bm-icon bm-close", "", function () { close(); });
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.title = "Close";
  closeBtn.append(iconCross());
  head.append(title, minBtn, closeBtn);

  var body = el("div", "bm-body");
  var desc = el("p", "bm-lede", "Describe how you'd like Ben's site to look and feel. Claude builds it in a minute or two, with Ben's real pages inside, and you'll see it right here on the site. Only you can see it until you send it to Ben.");
  desc.id = "bm-desc";
  var notice = el("p", "bm-notice");
  notice.setAttribute("role", "status");
  notice.hidden = true;

  var liveBox = el("div", "bm-live");
  liveBox.hidden = true;
  var liveText = el("span", "bm-live-text");
  var exitBtn = button("bm-btn bm-btn-quiet bm-exit", "Back to the normal site", function () { exitDraft(); });
  liveBox.append(liveText, exitBtn);

  var form = el("form", "bm-form");
  form.noValidate = true;
  var label = el("label", "bm-label", "Describe your version of the site");
  label.htmlFor = "bm-prompt";
  var ta = el("textarea", "bm-input");
  ta.id = "bm-prompt";
  ta.name = "prompt";
  ta.rows = 3;
  ta.placeholder = "e.g. A quiet night sky with slowly drifting stars, and the pages as constellations you can tap";
  var row = el("div", "bm-row");
  var buildBtn = el("button", "bm-btn bm-btn-primary bm-build", "Build");
  buildBtn.type = "submit";
  var newBtn = button("bm-btn bm-btn-quiet bm-new", "Start a new one instead", function () {
    setMode({ kind: "new", slug: "", rev: "" });
    ta.focus();
  });
  row.append(buildBtn, newBtn);
  var hint = el("p", "bm-hint");
  form.append(label, ta, row, hint);

  var progress = el("section", "bm-progress");
  progress.hidden = true;
  progress.setAttribute("aria-label", "Progress");
  var pStatus = el("p", "bm-status");
  pStatus.setAttribute("role", "status");
  var pSteps = el("ol", "bm-steps");
  var notes = el("details", "bm-notes");
  var notesSum = el("summary", null, "Claude's notes");
  var pThinking = el("div", "bm-thinking");
  var pText = el("div", "bm-text");
  notes.append(notesSum, pThinking, pText);
  progress.append(pStatus, pSteps, notes);

  var mine = el("section", "bm-mine");
  mine.setAttribute("aria-labelledby", "bm-mine-h");
  var mineH = el("h3", "bm-h3", "Your creations");
  mineH.id = "bm-mine-h";
  var empty = el("p", "bm-empty", "Nothing yet. What you build appears here, with every version, so you can go back to any of them.");
  var list = el("ul", "bm-list");
  mine.append(mineH, empty, list);

  var foot = el("p", "bm-foot", "There's no account: your creations live in this browser, so clearing your cookies loses them.");
  body.append(desc, notice, liveBox, form, progress, mine, foot);
  dialog.append(head, body);
  wrap.append(backdrop, dialog);

  // minimized while a build streams
  var pill = button("bm-pill", "", function () { open(); });
  pill.id = "build-pill";
  pill.hidden = true;
  var pillDot = el("span", "bm-pill-dot");
  pillDot.setAttribute("aria-hidden", "true");
  var pillText = el("span", "bm-pill-text", "Building…");
  pill.append(pillDot, pillText);

  document.body.append(wrap, pill);

  function svg(d) {
    var ns = "http://www.w3.org/2000/svg";
    var s = document.createElementNS(ns, "svg");
    s.setAttribute("viewBox", "0 0 24 24");
    s.setAttribute("width", "20");
    s.setAttribute("height", "20");
    s.setAttribute("aria-hidden", "true");
    s.setAttribute("focusable", "false");
    var p = document.createElementNS(ns, "path");
    p.setAttribute("d", d);
    p.setAttribute("fill", "none");
    p.setAttribute("stroke", "currentColor");
    p.setAttribute("stroke-width", "2");
    p.setAttribute("stroke-linecap", "round");
    s.append(p);
    return s;
  }
  function iconCross() { return svg("M6 6l12 12M18 6L6 18"); }
  function iconLine() { return svg("M6 18h12"); }

  // ── data ──
  function api(path, payload) {
    var init = { credentials: "same-origin", cache: "no-store", headers: {} };
    if (payload !== undefined) {
      init.method = "POST";
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(payload);
    }
    return fetch(API + path, init).then(function (r) {
      return r.text().then(function (t) {
        var j = null;
        try { j = t ? JSON.parse(t) : null; } catch (e) { j = null; }
        if (!r.ok) {
          var err = new Error((j && typeof j.error === "string" && j.error) || "Something went wrong. Please try again.");
          err.status = r.status;
          throw err;
        }
        return j;
      });
    });
  }

  function refresh() {
    if (loading) return loading;
    loading = api("/frontends").then(function (s) {
      state = s;
      render();
      return s;
    }).catch(function () {
      showNotice("Couldn't load your creations. Close this and try again in a moment.");
    }).then(function (s) {
      loading = null;
      schedulePoll();
      return s;
    });
    return loading;
  }

  // while a run is going on that this page isn't streaming (a reload mid-build,
  // another tab), check back until it's done
  function schedulePoll() {
    clearTimeout(pollTimer);
    if (!isOpen || busy || !state) return;
    if (state.frontends.some(function (f) { return f.running; })) pollTimer = setTimeout(refresh, POLL_MS);
  }

  function findFrontend(slug) {
    if (!state) return null;
    for (var i = 0; i < state.frontends.length; i++) if (state.frontends[i].slug === slug) return state.frontends[i];
    return null;
  }
  function findRevision(f, id) {
    if (!f) return null;
    for (var i = 0; i < f.revisions.length; i++) if (f.revisions[i].id === id) return f.revisions[i];
    return null;
  }
  function frontendOfRevision(id) {
    if (!state) return null;
    for (var i = 0; i < state.frontends.length; i++) if (findRevision(state.frontends[i], id)) return state.frontends[i];
    return null;
  }

  // ── rendering ──
  function showNotice(text) {
    notice.textContent = text || "";
    notice.hidden = !text;
  }

  function setMode(m) {
    mode = m;
    renderForm();
  }

  function renderForm() {
    var enabled = !!(state && state.enabled);
    form.hidden = !enabled;
    if (state && state.maxPrompt) ta.maxLength = state.maxPrompt;
    var f = mode.kind === "change" ? findFrontend(mode.slug) : null;
    if (mode.kind === "change" && !f) mode = { kind: "new", slug: "", rev: "" };
    var rv = f ? findRevision(f, mode.rev) : null;
    if (mode.kind === "new") {
      label.textContent = "Describe your version of the site";
      buildBtn.textContent = "Build";
      ta.placeholder = "e.g. A quiet night sky with slowly drifting stars, and the pages as constellations you can tap";
      hint.textContent = "";
      newBtn.hidden = true;
    } else if (rv) {
      label.textContent = "What should change in version " + rv.number + " of “" + f.title + "”?";
      buildBtn.textContent = "Make the change";
      ta.placeholder = "e.g. Make the text bigger and the background warmer";
      hint.textContent = "Changes start from version " + rv.number + ". Older versions stay as they are.";
      newBtn.hidden = false;
    } else {
      label.textContent = "Describe “" + f.title + "”";
      buildBtn.textContent = "Build";
      ta.placeholder = "e.g. A quiet night sky with slowly drifting stars";
      hint.textContent = "";
      newBtn.hidden = false;
    }
    var off = !!busy;
    ta.disabled = buildBtn.disabled = newBtn.disabled = off;
  }

  function render() {
    if (!state) return;
    if (!busy) showNotice(state.notice || "");
    // what the site shows
    var live = state.live;
    var lf = live ? findFrontend(live.slug) : null;
    liveBox.hidden = !live;
    if (live) liveText.textContent = "On the site now: " + (lf ? "“" + lf.title + "”, " : "") + "version " + live.number + ". Only you can see it.";
    renderForm();
    renderList();
    minBtn.hidden = !busy;
  }

  function renderList() {
    var items = [];
    var live = state.live;
    state.frontends.forEach(function (f) {
      var li = el("li", "bm-fe");
      li.setAttribute("data-slug", f.slug);
      var h = el("div", "bm-fe-head");
      h.append(el("span", "bm-fe-title", f.title), el("span", "bm-chip bm-chip-" + f.status.key, f.status.label));
      li.append(h);
      if (f.status.key !== "draft") li.append(el("p", "bm-fe-note", f.status.note));
      if (f.running || (busy && busy.slug === f.slug)) li.append(el("p", "bm-fe-note bm-working", "Claude is working on a new version…"));

      if (!f.revisions.length) {
        if (!f.running && !(busy && busy.slug === f.slug)) {
          var again = el("p", "bm-fe-note", "Not built yet. ");
          again.append(keyed(button("bm-link", "Build it", function () {
            setMode({ kind: "change", slug: f.slug, rev: "" });
            ta.focus();
          }), "build:" + f.slug));
          li.append(again);
        }
        items.push(li);
        return;
      }
      var revs = el("ol", "bm-revs");
      var selected = null;
      f.revisions.forEach(function (rv) {
        var on = !!(live && live.revision === rv.id);
        if (on) selected = rv;
        var item = el("li", "bm-rev-item");
        var b = keyed(button("bm-rev", "", function () { show(f, rv); }), "rev:" + rv.id);
        b.setAttribute("data-rev", rv.id);
        b.setAttribute("aria-pressed", on ? "true" : "false");
        var top = el("span", "bm-rev-top");
        top.append(el("span", "bm-rev-n", "Version " + rv.number));
        if (rv.parentNumber) top.append(el("span", "bm-rev-from", "from version " + rv.parentNumber));
        top.append(el("span", "bm-rev-state", on ? "On the site" : "Show on the site"));
        b.append(top);
        if (rv.prompt) b.append(el("span", "bm-rev-prompt", "“" + rv.prompt + "”"));
        if (f.status.revision === rv.id && f.status.key !== "draft") b.append(el("span", "bm-chip bm-chip-" + f.status.key, f.status.label));
        item.append(b);
        revs.append(item);
      });
      li.append(revs);

      // the actions act on the version on the site, else the newest
      selected = selected || f.revisions[0];
      {
        var acts = el("div", "bm-fe-actions");
        if (state.enabled) {
          acts.append(keyed(button("bm-btn bm-change", "Change it", function () {
            setMode({ kind: "change", slug: f.slug, rev: selected.id });
            ta.focus();
          }), "change:" + f.slug));
        }
        var st = f.status;
        var sub;
        if (st.revision === selected.id && st.key === "pending") sub = button("bm-btn bm-btn-quiet", "Version " + selected.number + " is with Ben");
        else if (st.revision === selected.id && st.key === "approved") sub = button("bm-btn bm-btn-quiet", "Version " + selected.number + " is approved");
        else if (st.revision === selected.id && st.key === "rejected") sub = button("bm-btn bm-btn-quiet", "Change it to submit again");
        if (sub) sub.disabled = true;
        else sub = button("bm-btn bm-btn-quiet bm-submit", "Submit version " + selected.number + " for review", function () { submit(f, selected, sub); });
        acts.append(keyed(sub, "submit:" + f.slug));
        li.append(acts);
        li.append(el("p", "bm-hint", "Submitting sends version " + selected.number + " to Ben. If he approves it, visitors may see it."));
      }
      items.push(li);
    });
    // keep focus on the same control across the re-render
    var had = document.activeElement && list.contains(document.activeElement) ? document.activeElement.getAttribute("data-key") : null;
    list.replaceChildren.apply(list, items);
    if (had !== null) {
      var again = had ? list.querySelector('[data-key="' + had.replace(/["\\]/g, "") + '"]') : null;
      (again && !again.disabled ? again : dialog).focus();
    }
    empty.hidden = items.length > 0;
  }

  // ── actions ──

  // show puts one of the visitor's revisions on the site (fe_live) and
  // reloads the site's front end in place.
  function show(f, rv) {
    return api("/fe/" + encodeURIComponent(f.slug) + "/live", { rev: rv.id }).then(function (live) {
      state.live = live;
      if (window.siteHost) window.siteHost.reload();
      if (!busy) setMode({ kind: "change", slug: f.slug, rev: rv.id });
      render();
    }).catch(function (err) {
      showNotice(err.message);
      refresh();
    });
  }

  function exitDraft() {
    exitBtn.disabled = true;
    var done = window.siteHost ? window.siteHost.exitDraft() : fetch(API + "/exit", { method: "POST", credentials: "same-origin" });
    return Promise.resolve(done).catch(function () {}).then(function () {
      exitBtn.disabled = false;
      if (state) state.live = null;
      if (!busy) mode = { kind: "new", slug: "", rev: "" };
      render();
    });
  }

  function submit(f, rv, btn) {
    btn.disabled = true;
    api("/fe/" + encodeURIComponent(f.slug) + "/submit", { rev: rv.id }).then(function (res) {
      f.status = res.status;
      render();
      showNotice(res.message || "");
    }).catch(function (err) {
      btn.disabled = false;
      showNotice(err.message);
    });
  }

  function resetProgress() {
    progress.hidden = false;
    progress.classList.remove("bm-failed");
    pStatus.classList.remove("bm-error");
    pStatus.textContent = "";
    pSteps.replaceChildren();
    pThinking.textContent = pText.textContent = "";
    notes.open = false;
  }

  function setPill(text, done) {
    pillText.textContent = text;
    pill.classList.toggle("bm-pill-done", !!done);
  }

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    if (busy || !state || !state.enabled) return;
    var prompt = ta.value.trim();
    if (!prompt) {
      showNotice("Describe the front end you'd like first.");
      ta.focus();
      return;
    }
    showNotice("");
    resetProgress();
    var m = mode;
    busy = { slug: m.slug, title: "" };
    renderForm();
    minBtn.hidden = false;
    setPill("Building…", false);

    var start;
    if (m.kind === "new") {
      pStatus.textContent = "Starting…";
      start = api("/new", { prompt: prompt }).then(function (created) {
        busy.slug = created.slug;
        busy.title = created.title;
        return refresh().then(function () { return { slug: created.slug, parent: "" }; });
      });
    } else {
      start = Promise.resolve({ slug: m.slug, parent: m.rev });
    }

    start.then(function (target) {
      render();
      return window.BuilderStream.run({
        url: API + "/fe/" + encodeURIComponent(target.slug) + "/chat",
        prompt: prompt,
        parent: target.parent,
        friendly: true,
        onStatus: function (text, isError) {
          pStatus.textContent = text;
          pStatus.classList.toggle("bm-error", !!isError);
          if (!isError) setPill(text.length > 48 ? "Building…" : text, false);
        },
        onThinking: function (t) { pThinking.textContent += t; },
        onText: function (t) { pText.textContent += t; },
        onTool: function (text) { pSteps.append(el("li", null, text)); }
      }).then(function (res) { res.slug = target.slug; return res; });
    }).then(function (res) {
      if (res.outcome !== "revision") {
        progress.classList.toggle("bm-failed", res.outcome === "error");
        if (res.outcome === "error") setPill("It didn't work · Open", true);
        return finish(false);
      }
      ta.value = "";
      pStatus.textContent = "Version " + res.number + " is ready. Showing it on the site…";
      // switch the site to it right away
      return api("/fe/" + encodeURIComponent(res.slug) + "/live", { rev: res.revision }).then(function (live) {
        if (state) state.live = live;
        if (window.siteHost) window.siteHost.reload();
        mode = { kind: "change", slug: res.slug, rev: res.revision };
        pStatus.textContent = "Version " + res.number + " is on the site now. Want to change anything?";
        setPill("Version " + res.number + " is ready · Open", true);
        return finish(true);
      });
    }).catch(function (err) {
      // creating failed (a limit, an empty prompt, ...): its message is for the visitor
      pStatus.textContent = err && err.message ? err.message : "The builder is unavailable right now. Please try again in a little while.";
      pStatus.classList.add("bm-error");
      progress.classList.add("bm-failed");
      setPill("It didn't work · Open", true);
      return finish(false);
    });
  });

  function finish(ok) {
    busy = null;
    return refresh().then(function () {
      render();
      // minimized, the pill stays, now saying how it went; open, it goes
      if (isOpen) {
        pill.hidden = true;
        root.classList.remove("bm-minimized");
      }
      if (isOpen && ok) ta.focus();
    });
  }

  // ── open / close / focus ──
  function focusables() {
    var q = 'button:not([disabled]), [href], textarea:not([disabled]), input:not([disabled]), select:not([disabled]), summary, [tabindex]:not([tabindex="-1"])';
    return Array.prototype.filter.call(dialog.querySelectorAll(q), function (n) {
      return !n.closest("[hidden]") && n.getClientRects().length > 0;
    });
  }

  function setInert(on) {
    if (on) {
      inerted = [];
      Array.prototype.forEach.call(document.body.children, function (n) {
        if (n === wrap || n === pill || n.inert) return;
        n.inert = true;
        inerted.push(n);
      });
    } else {
      inerted.forEach(function (n) { n.inert = false; });
      inerted = [];
    }
  }

  function open(from) {
    if (from) opener = from;
    else if (!opener || !document.contains(opener)) opener = document.getElementById("make-own");
    pill.hidden = true;
    root.classList.remove("bm-minimized");
    if (isOpen) return;
    isOpen = true;
    wrap.hidden = false;
    root.classList.add("bm-open");
    setInert(true);
    refresh();
    // focus the prompt if it's usable, else the dialog itself
    var target = !form.hidden && !ta.disabled ? ta : dialog;
    target.focus();
  }

  function hide() {
    isOpen = false;
    clearTimeout(pollTimer);
    wrap.hidden = true;
    root.classList.remove("bm-open");
    setInert(false);
  }

  // close: while a build streams it only minimizes
  function close() {
    if (!isOpen) return;
    if (busy) {
      minimize();
      return;
    }
    hide();
    var back = opener && document.contains(opener) ? opener : document.getElementById("make-own");
    if (back) back.focus();
  }

  function minimize() {
    if (!busy) {
      close();
      return;
    }
    hide();
    pill.hidden = false;
    root.classList.add("bm-minimized");
    pill.focus();
  }

  backdrop.addEventListener("click", function () { close(); });
  document.addEventListener("keydown", function (e) {
    if (!isOpen) return;
    if (e.key === "Escape") {
      e.preventDefault();
      close();
      return;
    }
    if (e.key !== "Tab") return;
    var f = focusables();
    if (!f.length) {
      e.preventDefault();
      dialog.focus();
      return;
    }
    var first = f[0], last = f[f.length - 1];
    if (e.shiftKey && (document.activeElement === first || document.activeElement === dialog)) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  });
  // keep focus inside while open (e.g. after the site's iframe reloads)
  document.addEventListener("focusin", function (e) {
    if (isOpen && !dialog.contains(e.target)) {
      var f = focusables();
      (f[0] || dialog).focus();
    }
  });

  // the site changed what it shows (a reload, or the banner's Exit)
  window.addEventListener("sitehost:load", function (e) {
    var d = e.detail || {};
    if (isOpen) { // a new iframe or banner joined the page
      setInert(false);
      setInert(true);
    }
    if (!state) return;
    if (d.draft && d.revision) {
      var f = frontendOfRevision(d.revision);
      state.live = f ? { frontend: f.id, slug: f.slug, revision: d.revision, number: d.number } : state.live;
    } else {
      state.live = null;
    }
    render();
  });

  // entry points: the transcript link, and ?build=1 (the old /build pages)
  Array.prototype.forEach.call(document.querySelectorAll("a.make-own-link"), function (a) {
    a.setAttribute("aria-haspopup", "dialog");
    a.addEventListener("click", function (e) {
      e.preventDefault();
      open(a);
    });
  });

  window.buildModal = { open: open, close: close };

  try {
    var params = new URLSearchParams(location.search);
    if (params.has("build")) {
      params.delete("build");
      var qs = params.toString();
      history.replaceState(history.state, "", location.pathname + (qs ? "?" + qs : "") + location.hash);
      open();
    }
  } catch (e) { /* an old browser: the button still works */ }
})();
