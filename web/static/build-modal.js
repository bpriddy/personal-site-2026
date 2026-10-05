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
//
// Its look is the "Studio" (docs/design-pov.md, 5.5; build-modal.css): a
// sheet docked right on wide screens and a bottom sheet on phones (drag the
// grabber down to close), a mono header with the run's state, the visitor's
// versions as a numbered log of their own prompts, and the composer pinned
// at the bottom (Enter sends, Shift+Enter is a new line).
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
  var versionsOpen = {}; // slug → the visitor opened its versions (kept across re-renders)
  var creditDraft = {}; // slug → what the visitor typed in "Credit me as", across re-renders

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

  // a hairline that runs along the sheet's top edge while Claude works
  var runLine = el("div", "bm-runline");
  runLine.setAttribute("aria-hidden", "true");
  var grab = el("div", "bm-grab");
  grab.setAttribute("aria-hidden", "true");

  var head = el("div", "bm-head");
  var title = el("h2", "bm-title", "Re-imagine this site");
  title.id = "bm-title";
  var headMeta = el("span", "bm-head-meta");
  var runState = el("span", "bm-state", "Idle");
  var minBtn = button("bm-icon bm-minimize", "", function () { minimize(); });
  minBtn.setAttribute("aria-label", "Minimize while it builds");
  minBtn.title = "Minimize while it builds";
  minBtn.append(el("span", "bm-icon-text", "Hide"), iconLine());
  minBtn.hidden = true;
  var closeBtn = button("bm-icon bm-close", "", function () { close(); });
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.title = "Close (Esc)";
  closeBtn.append(el("span", "bm-icon-text", "Esc"), iconCross());
  head.append(title, headMeta, runState, minBtn, closeBtn);

  var body = el("div", "bm-body");
  var intro = el("div", "bm-intro");
  var desc = el("p", "bm-desc", "Claude builds it in a minute or two, with Ben's real pages inside, and it appears right here on the site. Only you can see it until you send it to Ben.");
  desc.id = "bm-desc";
  var starters = el("div", "bm-starters");
  starters.setAttribute("role", "group");
  starters.setAttribute("aria-label", "Ideas to start from");
  [
    ["Brutalist", "Brutalist: raw grid, heavy type, nothing rounded"],
    ["Made of water", "Made of water: everything ripples gently, deep blue, calm"],
    ["A 1994 GeoCities page, but good", "A 1994 GeoCities page, but good: tiled background, marquee energy, done with real taste"]
  ].forEach(function (s) {
    starters.append(button("bm-starter", s[0], function () {
      ta.value = s[1];
      ta.focus();
      ta.setSelectionRange(ta.value.length, ta.value.length);
    }));
  });
  intro.append(starters, desc);
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
  ta.rows = 2;
  ta.placeholder = "Describe the site you want \u2014 a mood, a reference, a rule.";
  var hint = el("p", "bm-hint");
  var row = el("div", "bm-row");
  var keys = el("p", "bm-keys", "Enter to send \u00b7 Shift+Enter for a new line");
  keys.setAttribute("aria-hidden", "true");
  var buildBtn = el("button", "bm-btn bm-btn-primary bm-build", "Build");
  buildBtn.type = "submit";
  var newBtn = button("bm-btn bm-btn-text bm-new", "Start a new one instead", function () {
    setMode({ kind: "new", slug: "", rev: "" });
    ta.focus();
  });
  var attachBar = el("div", "bm-attach");
  row.append(attachBar, keys, newBtn, buildBtn);
  form.append(label, ta, hint, row);
  var attach = window.BuilderAttach ? window.BuilderAttach.mount({
    textarea: ta, bar: attachBar, max: 1,
    onError: function (msg) { showNotice(msg); }
  }) : null;
  // Enter sends; Shift+Enter (or an IME composition) stays in the text
  ta.addEventListener("keydown", function (e) {
    if (e.key !== "Enter" || e.shiftKey || e.isComposing || e.altKey) return;
    e.preventDefault();
    if (buildBtn.disabled) return;
    if (form.requestSubmit) form.requestSubmit(buildBtn);
    else buildBtn.click();
  });

  var progress = el("section", "bm-progress");
  progress.hidden = true;
  progress.setAttribute("aria-label", "Progress");
  var pStatus = el("p", "bm-status");
  pStatus.setAttribute("role", "status");
  var pSteps = el("ol", "bm-steps");
  pSteps.setAttribute("aria-hidden", "true"); // the status line speaks for it
  var notes = el("details", "bm-notes");
  var notesSum = el("summary", null, "Claude's notes");
  var pThinking = el("div", "bm-thinking");
  var pText = el("div", "bm-text");
  notes.append(notesSum, pThinking, pText);
  progress.append(pStatus, pSteps, notes);

  // your creations: below everything else, folded away behind its title until opened
  var mine = el("details", "bm-mine");
  var mineSum = el("summary", "bm-mine-sum");
  var mineH = el("h3", "bm-h3", "Your creations");
  mineH.id = "bm-mine-h";
  var mineCount = el("span", "bm-mine-count");
  mineSum.append(mineH, mineCount);
  var empty = el("p", "bm-empty", "Nothing yet. What you build appears here, with every version, so you can go back to any of them.");
  var list = el("ul", "bm-list");
  var foot = el("p", "bm-foot", "There's no account: your creations live in this browser, so clearing your cookies loses them.");
  mine.append(mineSum, empty, list, foot);

  // the prompt comes first: it's what this dialog is for
  var composer = el("div", "bm-composer");
  composer.append(form);
  body.append(notice, liveBox, composer, intro, progress, mine);
  dialog.append(runLine, grab, head, body);
  wrap.append(backdrop, dialog);

  // minimized while a build streams
  var pill = button("bm-pill", "", function () { open(); });
  pill.id = "build-pill";
  pill.hidden = true;
  var pillDot = el("span", "bm-pill-dot");
  pillDot.setAttribute("aria-hidden", "true");
  var pillText = el("span", "bm-pill-text", "Building\u2026");
  pill.append(pillDot, pillText);

  document.body.append(wrap, pill);

  function svg(d) {
    var ns = "http://www.w3.org/2000/svg";
    var s = document.createElementNS(ns, "svg");
    s.setAttribute("viewBox", "0 0 24 24");
    s.setAttribute("width", "14");
    s.setAttribute("height", "14");
    s.setAttribute("aria-hidden", "true");
    s.setAttribute("focusable", "false");
    var p = document.createElementNS(ns, "path");
    p.setAttribute("d", d);
    p.setAttribute("fill", "none");
    p.setAttribute("stroke", "currentColor");
    p.setAttribute("stroke-width", "1.5");
    p.setAttribute("stroke-linecap", "square");
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

  // follow: a run whose stream dropped ({slug, had: its version count before});
  // when it finishes, its new version goes on the site as if we'd streamed it
  var follow = null;
  function checkFollow() {
    if (!follow || busy) return;
    var f = findFrontend(follow.slug);
    if (!f) { follow = null; return; }
    if (f.running) return; // still working: the poll comes back
    var had = follow.had;
    follow = null;
    if (f.revisions.length > had) {
      var rv = f.revisions[0];
      show(f, rv).then(function () {
        pStatus.classList.remove("bm-error");
        pStatus.textContent = "Version " + rv.number + " is on the site now. Want to change anything?";
        setPill("Version " + rv.number + " is ready · Open", true);
      });
    } else {
      pStatus.textContent = "That one didn't finish. Please try again.";
    }
  }
  function refresh() {
    if (loading) return loading;
    loading = api("/frontends").then(function (s) {
      state = s;
      render();
      checkFollow();
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
    if ((!isOpen && !follow) || busy || !state) return;
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
    if (attach) attach.setDisabled(!!busy);
    if (state && state.maxPrompt) ta.maxLength = state.maxPrompt;
    var f = mode.kind === "change" ? findFrontend(mode.slug) : null;
    if (mode.kind === "change" && !f) mode = { kind: "new", slug: "", rev: "" };
    var rv = f ? findRevision(f, mode.rev) : null;
    if (mode.kind === "new") {
      label.textContent = "Describe your version of the site";
      buildBtn.textContent = "Build";
      ta.placeholder = "Describe the site you want \u2014 a mood, a reference, a rule.";
      hint.textContent = "";
      newBtn.hidden = true;
    } else if (rv) {
      label.textContent = "What should change in version " + rv.number + " of “" + f.title + "”?";
      buildBtn.textContent = "Make the change";
      ta.placeholder = "What should change? A colour, a feeling, a rule.";
      hint.textContent = "Changes start from version " + rv.number + ". Older versions stay as they are.";
      newBtn.hidden = false;
    } else {
      label.textContent = "Describe “" + f.title + "”";
      buildBtn.textContent = "Build";
      ta.placeholder = "Describe it \u2014 a mood, a reference, a rule.";
      hint.textContent = "";
      newBtn.hidden = false;
    }
    form.classList.toggle("bm-form-change", mode.kind !== "new"); // a longer label, set smaller
    var off = !!busy;
    ta.disabled = buildBtn.disabled = newBtn.disabled = off;
    composer.classList.toggle("bm-composer-off", !enabled);
  }

  // the header's meta: which draft and version is on the site, and whether
  // Claude is working
  function renderHead() {
    var live = state && state.live;
    var lf = live ? findFrontend(live.slug) : null;
    var n = state ? state.frontends.length : 0;
    headMeta.textContent = live && lf ? "Draft " + two(n - state.frontends.indexOf(lf)) + " / v" + live.number : "";
    runState.textContent = busy ? "Working" : "Idle";
    dialog.classList.toggle("bm-busy", !!busy);
    var empty = !n && !busy;
    intro.classList.toggle("bm-intro-empty", empty);
    starters.hidden = !empty || !(state && state.enabled);
  }
  function two(n) { return (n < 10 ? "0" : "") + n; }

  function render() {
    if (!state) return;
    renderHead();
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
      var count = el("span", "bm-fe-count", f.revisions.length ? two(f.revisions.length) + (f.revisions.length === 1 ? " version" : " versions") : "");
      h.append(el("span", "bm-fe-title", f.title), el("span", "bm-chip bm-chip-" + f.status.key, f.status.label), count);
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
        var mark = el("span", "bm-rev-mark");
        mark.setAttribute("aria-hidden", "true");
        top.append(mark, el("span", "bm-rev-n", "Version " + rv.number));
        if (rv.parentNumber) top.append(el("span", "bm-rev-from", "from version " + rv.parentNumber));
        top.append(el("span", "bm-rev-state", on ? "On the site" : "Show on the site"));
        b.append(top);
        if (rv.prompt) b.append(el("span", "bm-rev-prompt", "“" + rv.prompt + "”"));
        if (f.status.revision === rv.id && f.status.key !== "draft") b.append(el("span", "bm-chip bm-chip-" + f.status.key, f.status.label));
        item.append(b);
        revs.append(item);
      });
      // the versions fold away (closed unless the visitor opened them)
      var vers = el("details", "bm-versions");
      vers.open = !!versionsOpen[f.slug];
      var vsum = el("summary", "bm-versions-sum", f.revisions.length === 1 ? "1 version" : f.revisions.length + " versions");
      keyed(vsum, "versions:" + f.slug);
      vers.addEventListener("toggle", function () { versionsOpen[f.slug] = vers.open; });
      vers.append(vsum, revs);
      li.append(vers);

      // the actions act on the version on the site, else the newest
      selected = selected || f.revisions[0];
      {
        var acts = el("div", "bm-fe-actions");
        if (state.enabled) {
          acts.append(keyed(button("bm-btn bm-btn-text bm-change", "Change it", function () {
            setMode({ kind: "change", slug: f.slug, rev: selected.id });
            ta.focus();
          }), "change:" + f.slug));
        }
        var st = f.status;
        var sub;
        if (st.revision === selected.id && st.key === "pending") sub = button("bm-btn bm-btn-outline", "Version " + selected.number + " is with Ben");
        else if (st.revision === selected.id && st.key === "approved") sub = button("bm-btn bm-btn-outline", "Version " + selected.number + " is approved");
        else if (st.revision === selected.id && st.key === "rejected") sub = button("bm-btn bm-btn-outline", "Change it to submit again");
        var credit = null;
        if (sub) sub.disabled = true;
        else {
          // how to credit the visitor in the site bar if Ben approves it (optional)
          credit = el("label", "bm-credit");
          credit.append(el("span", "bm-credit-label", "Credit me as"));
          var ci = el("input", "bm-credit-input");
          ci.type = "text";
          ci.maxLength = 80;
          ci.placeholder = "Your name or handle (optional)";
          ci.autocomplete = "nickname";
          ci.value = f.slug in creditDraft ? creditDraft[f.slug] : (f.credit || "");
          ci.addEventListener("input", function () { creditDraft[f.slug] = ci.value; });
          credit.append(keyed(ci, "credit:" + f.slug));
          sub = button("bm-btn bm-btn-outline bm-submit", "Submit version " + selected.number + " for review", function () { submit(f, selected, sub, ci.value); });
        }
        if (credit) li.append(credit);
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
    mineCount.textContent = items.length ? two(items.length) : "";
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


  function submit(f, rv, btn, credit) {
    btn.disabled = true;
    api("/fe/" + encodeURIComponent(f.slug) + "/submit", { rev: rv.id, credit: (credit || "").trim() }).then(function (res) {
      f.credit = (credit || "").trim();
      delete creditDraft[f.slug];
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

  // a line of the mono log: VERB  path, typed in; the newest carries the caret
  function logStep(text) {
    var m = /^(\S+)\s+(.*)$/.exec(text || "");
    var li = el("li", "bm-step");
    if (m) li.append(el("span", "bm-step-verb", m[1]), el("span", "bm-step-what", m[2]));
    else li.textContent = text;
    pSteps.append(li);
    pSteps.scrollTop = pSteps.scrollHeight;
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
    renderHead();
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
      var f0 = findFrontend(target.slug), had = f0 ? f0.revisions.length : 0;
      return window.BuilderStream.run({
        url: API + "/fe/" + encodeURIComponent(target.slug) + "/chat",
        prompt: prompt,
        parent: target.parent,
        images: attach ? attach.images() : [],
        friendly: true,
        onStatus: function (text, isError) {
          pStatus.textContent = text;
          pStatus.classList.toggle("bm-error", !!isError);
          if (!isError) setPill(text.length > 48 ? "Building…" : text, false);
        },
        onThinking: function (t) { pThinking.textContent += t; },
        onText: function (t) { pText.textContent += t; },
        onTool: function (text) { logStep(text); }
      }).then(function (res) { res.slug = target.slug; res.had = had; return res; });
    }).then(function (res) {
      // the connection dropped mid-run: keep checking, and show the version when it lands
      if (res.outcome === "dropped") follow = { slug: res.slug, had: res.had };
      if (res.outcome !== "revision") {
        progress.classList.toggle("bm-failed", res.outcome === "error");
        if (res.outcome === "error") setPill("It didn't work · Open", true);
        return finish(false);
      }
      ta.value = "";
      if (attach) attach.clear();
      logStep("Live version " + res.number);
      pStatus.textContent = "Version " + res.number + " is ready. Showing it on the site\u2026";
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
    clearTimeout(hideTimer);
    wrap.classList.remove("bm-leaving");
    dialog.style.transform = "";
    wrap.hidden = false;
    root.classList.add("bm-open");
    setInert(true);
    refresh();
    // focus the prompt if it's usable, else the dialog itself
    var target = !form.hidden && !ta.disabled ? ta : dialog;
    target.focus({ preventScroll: true });
  }

  // hide closes the sheet; the exit is a short reverse of the entrance (CSS),
  // after which it leaves the page's layout
  var hideTimer = 0;
  function hide() {
    isOpen = false;
    clearTimeout(pollTimer);
    root.classList.remove("bm-open");
    setInert(false);
    var quick = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (quick) {
      wrap.hidden = true;
      return;
    }
    wrap.classList.add("bm-leaving");
    clearTimeout(hideTimer);
    hideTimer = setTimeout(function () {
      wrap.classList.remove("bm-leaving");
      dialog.style.transform = "";
      if (!isOpen) wrap.hidden = true;
    }, 320);
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

  // phones: drag the sheet down by its grabber or header to close it
  // (released past 40% of its height, or flicked down)
  (function () {
    var startY = 0, startT = 0, dy = 0, dragging = false;
    function isSheet() { return window.matchMedia && window.matchMedia("(max-width: 1023px)").matches; }
    function down(e) {
      if (!isSheet() || e.button > 0 || e.target.closest("button")) return;
      dragging = true;
      startY = e.clientY;
      startT = Date.now();
      dy = 0;
      dialog.classList.add("bm-dragging");
      try { e.target.setPointerCapture(e.pointerId); } catch (err) { /* fine */ }
    }
    function move(e) {
      if (!dragging) return;
      dy = Math.max(0, e.clientY - startY);
      dialog.style.transform = "translateY(" + dy + "px)";
    }
    function up() {
      if (!dragging) return;
      dragging = false;
      dialog.classList.remove("bm-dragging");
      var v = dy / Math.max(1, Date.now() - startT); // px per ms
      if (dy > dialog.offsetHeight * 0.4 || (v > 0.5 && dy > 24)) {
        close();
        if (isOpen) dialog.style.transform = ""; // minimizing instead keeps it
      } else {
        dialog.style.transform = "";
      }
    }
    [grab, head].forEach(function (n) {
      n.addEventListener("pointerdown", down);
      n.addEventListener("pointermove", move);
      n.addEventListener("pointerup", up);
      n.addEventListener("pointercancel", up);
    });
  })();
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
