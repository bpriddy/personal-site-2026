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
  var desc = el("p", "bm-desc", "Claude builds it in about 10\u201315 minutes, with Ben's real pages inside, and it appears right here on the site. Only you can see it until you send it to Ben.");
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
  newBtn.hidden = true; // the target switch says it now
  // what the prompt applies to, always in view: change a version, or start a
  // new site (a radio pair; the button and label repeat it)
  var target = el("div", "bm-target");
  target.setAttribute("role", "radiogroup");
  target.setAttribute("aria-label", "What your prompt applies to");
  function targetOpt(cls, kicker, onPick) {
    var b = button("bm-target-opt " + cls, "", onPick);
    b.setAttribute("role", "radio");
    var k = el("span", "bm-target-kicker", kicker);
    var n = el("span", "bm-target-name");
    b.append(k, n);
    return { btn: b, name: n };
  }
  var tChange = targetOpt("bm-target-change", "Change", function () {
    var c = changeCandidate();
    if (c) chooseMode({ kind: "change", slug: c.slug, rev: c.rev });
    ta.focus();
  });
  var tNew = targetOpt("bm-target-new", "New", function () {
    chooseMode({ kind: "new", slug: "", rev: "" });
    ta.focus();
  });
  tNew.name.textContent = "Start a new site";
  target.append(tChange.btn, tNew.btn);
  form.append(target, label, ta, hint, row);
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

  // while a build runs (here, in another tab, or before a reload): what's being
  // built and a clear way to stop it (v1.11)
  var gen = el("section", "bm-gen");
  gen.hidden = true;
  gen.setAttribute("aria-label", "Build in progress");
  var genHead = el("div", "bm-gen-head");
  var genLabel = el("p", "bm-gen-label", "Building your version");
  var genCopy = copyButton(function () { var g = genInfo(); return g ? g.prompt : ""; }, "Copy the prompt being built");
  genHead.append(genLabel, genCopy);
  var genPrompt = el("p", "bm-gen-prompt");
  // a naive countdown from 15:00 (v1.13): builds take 10–15 minutes
  var GEN_ESTIMATE = 15 * 60;
  var genClock = el("div", "bm-gen-clock");
  var genTime = el("span", "bm-gen-time");
  genTime.setAttribute("role", "timer");
  var genClockNote = el("span", "bm-gen-clock-note");
  genClock.append(genTime, genClockNote);
  // "email me when it's ready" (v1.13): offered while it builds, if the
  // server can send email
  var genNotify = el("form", "bm-gen-notify");
  genNotify.noValidate = true;
  var gnLead = el("p", "bm-gen-notify-lead", "You don't have to wait here. We can email you when it's ready.");
  var gnRow = el("div", "bm-gen-notify-row");
  var gnInput = el("input", "bm-gen-notify-input");
  gnInput.type = "email";
  gnInput.name = "email";
  gnInput.autocomplete = "email";
  gnInput.placeholder = "you@example.com";
  gnInput.setAttribute("aria-label", "Your email");
  var gnSend = el("button", "bm-btn bm-btn-primary bm-gen-notify-send", "Email me");
  gnSend.type = "submit";
  gnRow.append(gnInput, gnSend);
  var gnFine = el("p", "bm-gen-notify-fine", "Only used to tell you about this build, then deleted.");
  var gnErr = el("p", "bm-gen-notify-err");
  gnErr.setAttribute("role", "alert");
  gnErr.hidden = true;
  var gnDone = el("div", "bm-gen-notify-done");
  var gnDoneText = el("p", "bm-gen-notify-done-text");
  gnDoneText.setAttribute("aria-live", "polite");
  var gnChange = button("bm-link", "Change", function () { notifyEditing = true; applyGen(); gnInput.focus(); });
  var gnStop = button("bm-link", "Don't email me", function () { unsetNotify(); });
  gnDone.append(gnDoneText, gnChange, gnStop);
  genNotify.append(gnLead, gnRow, gnFine, gnErr, gnDone);
  genNotify.addEventListener("submit", function (e) { e.preventDefault(); setNotify(); });
  var cancelBtn = button("bm-btn bm-cancel", "Cancel this build", function () { cancelGen(); });
  cancelBtn.prepend(iconCross());
  var genNote = el("p", "bm-gen-note", "The prompt box opens again when it's done or canceled. Canceling keeps everything as it was.");
  gen.append(genHead, genPrompt, genClock, genNotify, cancelBtn, genNote);

  // building paused for the budget (v1.12): instead of the prompt, a calm
  // note of when the studio reopens, what still works, and a place to keep
  // an idea in this browser until then
  var pausedBox = el("section", "bm-paused");
  pausedBox.hidden = true;
  pausedBox.setAttribute("aria-labelledby", "bm-paused-h");
  var psKicker = el("p", "bm-paused-kicker", "Studio closed");
  var psHead = el("h3", "bm-paused-h");
  psHead.id = "bm-paused-h";
  psHead.tabIndex = -1; // focused when the state appears, so it's read first
  var psText = el("p", "bm-paused-text");
  psText.id = "bm-paused-text";
  var psMean = el("p", "bm-paused-kicker bm-paused-mean", "Meanwhile");
  var psList = el("ul", "bm-paused-list");
  function pausedRow(text, b) {
    var li = el("li", "bm-paused-row");
    li.append(el("span", "bm-paused-what", text), b);
    psList.append(li);
    return li;
  }
  var rowMine = pausedRow("Show any of your versions on the site, or send one to Ben.",
    button("bm-btn bm-btn-outline", "Your creations", function () { openMine(); }));
  var rowShuffle = pausedRow("See what other visitors have made.",
    button("bm-btn bm-btn-outline", "Show another version", function () { shuffleOther(); }));
  // keep an idea (localStorage only: nothing is queued on the server, so
  // nothing is ever built, or spent, without the visitor there)
  var IDEA_KEY = "bm-idea";
  var idea = el("div", "bm-idea");
  var ideaForm = el("div", "bm-idea-form");
  var ideaLabel = el("label", "bm-label bm-idea-label", "Keep an idea for when it reopens");
  ideaLabel.htmlFor = "bm-idea-input";
  var ideaTa = el("textarea", "bm-input bm-idea-input");
  ideaTa.id = "bm-idea-input";
  ideaTa.rows = 2;
  ideaTa.placeholder = "Describe the site you want — a mood, a reference, a rule.";
  var ideaRow = el("div", "bm-row");
  var ideaHint = el("p", "bm-keys bm-idea-hint", "Saved in this browser only");
  var ideaSave = button("bm-btn bm-btn-primary bm-idea-save", "Keep this idea", function () { saveIdea(); });
  ideaRow.append(ideaHint, ideaSave);
  ideaForm.append(ideaLabel, ideaTa, ideaRow);
  var ideaKept = el("div", "bm-idea-kept");
  var ideaKeptHead = el("p", "bm-paused-kicker", "Your idea, kept");
  var ideaQuote = el("p", "bm-idea-quote");
  var ideaKeptNote = el("p", "bm-hint", "We'll keep it here, in this browser. When the studio reopens, it'll be waiting in the prompt box, ready to build.");
  var ideaActs = el("div", "bm-fe-actions");
  var ideaEdit = button("bm-btn bm-btn-text", "Edit it", function () { editIdea(); });
  var ideaForget = button("bm-btn bm-btn-text", "Forget it", function () { forgetIdea(); });
  ideaActs.append(ideaEdit, ideaForget);
  ideaKept.append(ideaKeptHead, ideaQuote, ideaKeptNote, ideaActs);
  var ideaNote = el("p", "bm-sr");
  ideaNote.setAttribute("role", "status");
  idea.append(ideaForm, ideaKept, ideaNote);
  pausedBox.append(psKicker, psHead, psText, psMean, psList, idea);

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
  body.append(notice, liveBox, gen, pausedBox, composer, intro, progress, mine);
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
  function iconCopy() { return svg("M9 9h10v10H9zM5 15V5h10"); }

  // copyButton: a small button that copies a prompt's full text (getText is
  // read at click time), saying "Copied" for a moment
  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) return navigator.clipboard.writeText(text);
    return new Promise(function (resolve, reject) {
      var t = document.createElement("textarea");
      t.value = text;
      t.setAttribute("readonly", "");
      t.style.position = "fixed";
      t.style.opacity = "0";
      document.body.append(t);
      t.select();
      try { document.execCommand("copy") ? resolve() : reject(new Error("copy")); } catch (e) { reject(e); }
      t.remove();
    });
  }
  function copyButton(getText, label) {
    var b = button("bm-copy", "", function (e) {
      e.stopPropagation();
      var text = getText();
      if (!text) return;
      copyText(text).then(function () {
        b.classList.add("bm-copied");
        txt.textContent = "Copied";
        setTimeout(function () { b.classList.remove("bm-copied"); txt.textContent = "Copy"; }, 1600);
      }, function () { txt.textContent = "Couldn't copy"; });
    });
    b.setAttribute("aria-label", label || "Copy the prompt");
    b.title = label || "Copy the prompt";
    var txt = el("span", "bm-copy-text", "Copy");
    b.append(iconCopy(), txt);
    return b;
  }
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
          err.key = j && typeof j.key === "string" ? j.key : "";
          err.paused = j && j.paused && typeof j.paused.limit === "string" ? j.paused : null;
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
      pStatus.textContent = "That build stopped without a new version.";
      if (!isOpen) setPill("Build stopped · Open", true);
    }
  }
  function refresh() {
    if (loading) return loading;
    loading = api("/frontends").then(function (s) {
      state = s;
      if (!modeChosen && !busy && s.live && s.live.slug) mode = { kind: "change", slug: s.live.slug, rev: s.live.revision };
      if (!busy && !follow) {
        for (var i = 0; i < s.frontends.length; i++) {
          if (s.frontends[i].running) { follow = { slug: s.frontends[i].slug, had: s.frontends[i].revisions.length }; break; }
        }
      }
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
    if (busy || !state) return;
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

  // ── a build in progress (v1.11) ──
  // genInfo: the build going on now, streamed here (busy) or seen in the
  // list (a reload, another tab): {slug, prompt, startedAt (ms)}, or null
  var GEN_KEY = "bm-generating";
  var canceling = false, genTimer = 0;
  // the address a build will email, as the server masks it: slug → "b•••@x.com"
  // ("" = none); what we set here wins over a list that hasn't caught up
  var notifySet = {}, notifyEditing = false, notifyBusy = false;
  function genInfo() {
    var g = null;
    if (busy) g = { slug: busy.slug, prompt: busy.prompt || "", startedAt: busy.startedAt || 0, notify: "" };
    else if (state) {
      for (var i = 0; i < state.frontends.length; i++) {
        var f = state.frontends[i];
        if (f.running) { g = { slug: f.slug, prompt: f.run ? f.run.prompt : "", startedAt: f.run ? Date.parse(f.run.startedAt) || 0 : 0, notify: f.run && f.run.notify || "" }; break; }
      }
    }
    if (g && state && !busy) {
      // a streamed build's run is in the list too: take its server start time and address
      var lf = findFrontend(g.slug);
      if (lf && lf.run && lf.run.notify) g.notify = lf.run.notify;
    }
    if (g && busy) {
      var bf = findFrontend(busy.slug);
      if (bf && bf.running && bf.run) {
        g.startedAt = Date.parse(bf.run.startedAt) || g.startedAt;
        g.notify = bf.run.notify || "";
      }
    }
    if (g && Object.prototype.hasOwnProperty.call(notifySet, g.slug)) g.notify = notifySet[g.slug];
    return g;
  }
  function clock(startedAt) {
    var left = GEN_ESTIMATE - Math.max(0, Math.round((Date.now() - startedAt) / 1000));
    if (left <= 0) return { text: "Any minute now", note: "Taking a little longer than usual.", over: true };
    var m = Math.floor(left / 60), s = left % 60;
    return { text: m + ":" + (s < 10 ? "0" : "") + s, note: "left. Builds usually take 10\u201315 minutes.", over: false };
  }
  function tickClock(g) {
    var c = clock(g.startedAt || Date.now());
    genTime.textContent = c.text;
    genClockNote.textContent = c.note;
    genClock.classList.toggle("bm-over", c.over);
  }
  function renderNotify(g) {
    var on = !!(state && state.notify);
    genNotify.hidden = !on;
    if (!on) return;
    var has = !!g.notify && !notifyEditing;
    gnLead.hidden = has;
    gnRow.hidden = has;
    gnFine.hidden = has;
    gnDone.hidden = !has;
    if (has) gnDoneText.textContent = "We'll email " + g.notify + " when it's ready.";
    gnSend.disabled = notifyBusy;
    gnInput.disabled = notifyBusy;
  }
  function setNotify() {
    var g = genInfo();
    var email = gnInput.value.trim();
    if (!g || notifyBusy) return;
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      gnErr.textContent = "That doesn't look like an email address.";
      gnErr.hidden = false;
      gnInput.focus();
      return;
    }
    notifyBusy = true;
    gnErr.hidden = true;
    applyGen();
    var slug = g.slug;
    api("/fe/" + encodeURIComponent(slug) + "/notify", { email: email }).then(function (res) {
      notifySet[slug] = res && res.notify || "";
      notifyEditing = false;
      gnInput.value = "";
    }).catch(function (err) {
      gnErr.textContent = err && err.message ? err.message : "Couldn't save that. Please try again.";
      gnErr.hidden = false;
    }).then(function () {
      notifyBusy = false;
      applyGen();
      if (!gnDone.hidden) gnChange.focus();
    });
  }
  function unsetNotify() {
    var g = genInfo();
    if (!g || notifyBusy) return;
    notifyBusy = true;
    var slug = g.slug;
    fetch(API + "/fe/" + encodeURIComponent(slug) + "/notify", { method: "DELETE", credentials: "same-origin", cache: "no-store" })
      .then(function (r) { if (r.ok) notifySet[slug] = ""; })
      .catch(function () {})
      .then(function () { notifyBusy = false; notifyEditing = false; applyGen(); gnInput.focus(); });
  }
  function applyGen() {
    var g = genInfo();
    root.classList.toggle("bm-generating", !!g);
    gen.hidden = !g;
    try { if (g) localStorage.setItem(GEN_KEY, "1"); else localStorage.removeItem(GEN_KEY); } catch (e) { /* storage off: the list still tells */ }
    if (!g) {
      canceling = false;
      clearInterval(genTimer);
      genTimer = 0;
      notifySet = {};
      notifyEditing = false;
      gnErr.hidden = true;
      return;
    }
    genPrompt.textContent = g.prompt ? "\u201c" + g.prompt + "\u201d" : "";
    genPrompt.hidden = !g.prompt;
    cancelBtn.disabled = canceling;
    cancelBtn.lastChild.textContent = canceling ? "Canceling\u2026" : "Cancel this build";
    tickClock(g);
    renderNotify(g);
    if (!genTimer) genTimer = setInterval(function () { var c = genInfo(); if (c) tickClock(c); }, 1000);
    // the site bar's Re-imagine button says Building… (open or not)
    setPill("Building\u2026", false);
  }
  function cancelGen() {
    var g = genInfo();
    if (!g || canceling) return;
    canceling = true;
    applyGen();
    pStatus.textContent = "Canceling\u2026";
    pStatus.classList.remove("bm-error");
    api("/fe/" + encodeURIComponent(g.slug) + "/cancel", {}).then(function () {
      // streaming here: the stream ends with "canceled"; otherwise the list will show it stopped
      if (!busy) return refresh();
    }).catch(function (err) {
      canceling = false;
      applyGen();
      showNotice(err && err.message ? err.message : "Couldn't cancel it. Please try again.");
    });
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

  // the visitor's own choice of target, kept for this tab across reloads; until
  // they choose, a version on the site is what the prompt changes
  var MODE_KEY = "bm-mode", modeChosen = false;
  function chooseMode(m) {
    modeChosen = true;
    try { sessionStorage.setItem(MODE_KEY, JSON.stringify(m)); } catch (e) { /* storage off */ }
    setMode(m);
  }
  (function () {
    try {
      var m = JSON.parse(sessionStorage.getItem(MODE_KEY) || "null");
      if (m && (m.kind === "new" || (m.kind === "change" && typeof m.slug === "string"))) { mode = m; modeChosen = true; }
    } catch (e) { /* none */ }
  })();
  // changeCandidate: what "Change" means now: the version being changed, else
  // the one on the site, else the newest version of the newest creation
  function changeCandidate() {
    if (mode.kind === "change" && findFrontend(mode.slug)) return { slug: mode.slug, rev: mode.rev };
    var live = state && state.live;
    if (live && findFrontend(live.slug)) return { slug: live.slug, rev: live.revision };
    var fs = state ? state.frontends : [];
    for (var i = 0; i < fs.length; i++) if (fs[i].revisions.length) return { slug: fs[i].slug, rev: fs[i].revisions[0].id };
    return null;
  }

  function renderForm() {
    var enabled = !!(state && state.enabled);
    form.hidden = !enabled;
    if (state && state.maxPrompt) ta.maxLength = state.maxPrompt;
    var f = mode.kind === "change" ? findFrontend(mode.slug) : null;
    if (mode.kind === "change" && !f) mode = { kind: "new", slug: "", rev: "" };
    var rv = f ? findRevision(f, mode.rev) : null;
    if (mode.kind === "new") {
      label.textContent = "Describe a new version of the site";
      buildBtn.textContent = "Build a new site";
      ta.placeholder = "Describe the site you want \u2014 a mood, a reference, a rule.";
      hint.textContent = "This starts a new creation. Your existing ones stay as they are.";
    } else if (rv) {
      label.textContent = "What should change in version " + rv.number + " of \u201c" + f.title + "\u201d?";
      buildBtn.textContent = "Change version " + rv.number;
      ta.placeholder = "What should change? A colour, a feeling, a rule.";
      hint.textContent = "This makes version " + (f.revisions.length + 1) + ", starting from version " + rv.number + ". Older versions stay as they are.";
    } else {
      label.textContent = "Describe \u201c" + f.title + "\u201d";
      buildBtn.textContent = "Build \u201c" + f.title + "\u201d";
      ta.placeholder = "Describe it \u2014 a mood, a reference, a rule.";
      hint.textContent = "";
    }
    // the switch: what the prompt applies to
    var cand = changeCandidate();
    target.hidden = !cand;
    if (cand) {
      var cf = findFrontend(cand.slug), crv = findRevision(cf, cand.rev);
      tChange.name.textContent = "\u201c" + cf.title + "\u201d" + (crv ? " \u00b7 version " + crv.number : "");
    }
    tChange.btn.setAttribute("aria-checked", mode.kind === "change" ? "true" : "false");
    tNew.btn.setAttribute("aria-checked", mode.kind === "new" ? "true" : "false");
    form.classList.toggle("bm-form-new", mode.kind === "new");
    form.classList.toggle("bm-form-change", mode.kind !== "new"); // a longer label, set smaller
    var off = !!busy || !!genInfo();
    ta.disabled = buildBtn.disabled = newBtn.disabled = tChange.btn.disabled = tNew.btn.disabled = off;
    if (attach) attach.setDisabled(off);
    composer.classList.toggle("bm-composer-off", !enabled);
  }

  // the header's meta: which draft and version is on the site, and whether
  // Claude is working
  function renderHead() {
    var live = state && state.live;
    var lf = live ? findFrontend(live.slug) : null;
    var n = state ? state.frontends.length : 0;
    headMeta.textContent = live && lf ? "Draft " + two(n - state.frontends.indexOf(lf)) + " / v" + live.number : "";
    runState.textContent = busy ? "Working" : (state && state.paused ? "Closed" : "Idle");
    dialog.classList.toggle("bm-busy", !!busy);
    var empty = !n && !busy;
    intro.classList.toggle("bm-intro-empty", empty);
    starters.hidden = !empty || !(state && state.enabled);
  }
  function two(n) { return (n < 10 ? "0" : "") + n; }

  // ── building paused for the budget (v1.12) ──
  // pause: {limit: "day" | "month" | "later", reopens?: ISO time}. The words
  // say when it reopens in the visitor's own time; never why in money terms.
  function reopensText(p) {
    var t = p && p.reopens ? Date.parse(p.reopens) : NaN;
    if (isNaN(t)) return "";
    var ms = t - Date.now(), d = new Date(t);
    var time = d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
    if (ms <= 60 * 1000) return "any moment now";
    if (ms < 60 * 60 * 1000) return "in under an hour";
    if (ms < 24 * 60 * 60 * 1000) {
      var h = Math.round(ms / 3600000);
      return "in about " + h + (h === 1 ? " hour" : " hours") + " (" + time + " your time)";
    }
    // days are UTC: say the visitor's own date and time, so it's never a day off
    return "on " + d.toLocaleDateString([], { weekday: "long", day: "numeric", month: "long" }) + ", " + time + " your time";
  }
  function pausedWords(p) {
    var when = reopensText(p);
    if (p.limit === "day") {
      return { kicker: "Studio closed · until tomorrow", head: "The studio is closed for today.",
        text: "Everything here is built by Claude, and today's building time is used up. It opens again " + (when || "tomorrow") + ".",
        short: "The studio is closed for today." };
    }
    if (p.limit === "month") {
      return { kicker: "Studio closed · until next month", head: "The studio is closed for the rest of the month.",
        text: "Everything here is built by Claude, and this month's building time is used up. It opens again " + (when || "next month") + ".",
        short: "The studio is closed until next month." };
    }
    return { kicker: "Studio closed", head: "The studio is closed for now.",
      text: "Everything here is built by Claude, and building is taking a short break. " + (when ? "It opens again " + when + "." : "Check back a little later."),
      short: "The studio is closed for now." };
  }
  // the site bar's Re-imagine button hints at it: a still, hollow dot, and
  // words for screen readers and on hover; the label stays the same
  var pauseHint = null;
  function hintPaused(p) {
    pauseHint = p || null;
    root.classList.toggle("bm-paused", !!pauseHint);
    var btn = document.getElementById("make-own");
    if (!btn) return;
    if (pauseHint) btn.title = pausedWords(pauseHint).short + " Your versions are still here.";
    else btn.removeAttribute("title");
    if (genInfo() || root.classList.contains("bm-btn-status")) return; // the build's own words win
    btn.setAttribute("aria-label", pauseHint ? "Re-imagine this site. " + pausedWords(pauseHint).short + " You can still see your versions." : "Re-imagine this site");
  }
  function canShuffle() {
    var c = window.siteHost && window.siteHost.current ? window.siteHost.current() : null;
    return !!(c && typeof window.siteHost.shuffle === "function" && (c.choices > 1 || c.draft));
  }
  function shuffleOther() {
    // close, so the other visitor's version is what they see
    close();
    if (canShuffle()) window.siteHost.shuffle();
  }
  function openMine() {
    mine.open = true;
    mineSum.scrollIntoView({ block: "start", behavior: "smooth" });
    mineSum.focus({ preventScroll: true });
  }
  var minePausedOpened = false;
  function renderPaused() {
    var p = state && state.paused;
    pausedBox.hidden = !p;
    hintPaused(p);
    dialog.setAttribute("aria-describedby", p ? "bm-paused-text" : "bm-desc");
    intro.hidden = !!p;
    if (!p) {
      minePausedOpened = false;
      return;
    }
    var w = pausedWords(p);
    psKicker.textContent = w.kicker;
    psHead.textContent = w.head;
    psText.textContent = w.text;
    var hasVersions = state.frontends.some(function (f) { return f.revisions.length > 0; });
    rowMine.hidden = !hasVersions;
    rowShuffle.hidden = !canShuffle();
    if (state.maxPrompt) ideaTa.maxLength = state.maxPrompt;
    renderIdea();
    // with no prompt box, their creations are the thing to look at
    if (hasVersions && !minePausedOpened) {
      minePausedOpened = true;
      mine.open = true;
    }
    // the prompt box went away under the visitor (or they just opened it):
    // start them at the state's heading
    var a = document.activeElement;
    if (isOpen && (a === dialog || a === document.body || a === ta || !a || !dialog.contains(a) || (a.closest && a.closest("[hidden]")))) {
      psHead.focus({ preventScroll: true });
    }
  }

  // the idea kept for later: {text, savedAt} in localStorage (this browser only)
  function readIdea() {
    try {
      var j = JSON.parse(localStorage.getItem(IDEA_KEY) || "null");
      return j && typeof j.text === "string" && j.text.trim() ? j : null;
    } catch (e) { return null; }
  }
  function writeIdea(text) {
    try {
      if (text) localStorage.setItem(IDEA_KEY, JSON.stringify({ text: text, savedAt: Date.now() }));
      else localStorage.removeItem(IDEA_KEY);
      return true;
    } catch (e) { return false; }
  }
  var ideaEditing = false;
  function renderIdea() {
    var saved = readIdea();
    var editing = !saved || ideaEditing;
    ideaForm.hidden = !editing;
    ideaKept.hidden = editing;
    ideaSave.textContent = saved ? "Keep the new version" : "Keep this idea";
    if (saved) ideaQuote.textContent = "“" + saved.text + "”";
  }
  function saveIdea() {
    var text = ideaTa.value.trim();
    if (!text) {
      ideaNote.textContent = "Write your idea first.";
      ideaTa.focus();
      return;
    }
    if (!writeIdea(text)) {
      ideaNote.textContent = "This browser won't keep it (its storage is off). Copy it somewhere safe.";
      return;
    }
    ideaEditing = false;
    ideaTa.value = "";
    renderIdea();
    ideaNote.textContent = "Kept. It'll be in the prompt box when the studio reopens.";
    ideaEdit.focus();
  }
  function editIdea() {
    var saved = readIdea();
    ideaEditing = true;
    ideaTa.value = saved ? saved.text : "";
    renderIdea();
    ideaTa.focus();
    ideaTa.setSelectionRange(ideaTa.value.length, ideaTa.value.length);
  }
  function forgetIdea() {
    writeIdea("");
    ideaEditing = false;
    ideaTa.value = "";
    renderIdea();
    ideaNote.textContent = "Forgotten.";
    ideaTa.focus();
  }
  // when building is open again, a kept idea goes back into the prompt box
  // (once per page; it stays kept until it's built or forgotten)
  var ideaRestored = false, softNotice = "";
  function restoreIdea() {
    if (ideaRestored || busy || !state || !state.enabled || state.paused || genInfo()) return;
    var saved = readIdea();
    if (!saved || ta.value.trim()) return;
    ideaRestored = true;
    ta.value = saved.text;
    softNotice = "Your saved idea is back in the box. Build it when you're ready.";
  }
  // a build refused because the studio just closed (or the API's limit
  // stopped it): not an error. The prompt is kept as their idea, and the
  // paused state takes over.
  function pausedNow(prompt, p) {
    if (prompt) writeIdea(prompt);
    ta.value = "";
    if (attach) attach.clear();
    progress.hidden = true;
    busy = null;
    minBtn.hidden = true;
    clearPill();
    return refresh().then(function () {
      if (state && !state.paused) state.paused = p || { limit: "later" };
      if (state) state.enabled = false;
      ideaEditing = false;
      softNotice = prompt ? "The studio closed just before your build could start. Your idea is kept below." : "";
      render();
      if (isOpen) psHead.focus({ preventScroll: true });
      else setPill(pausedWords(state ? state.paused : { limit: "later" }).short, true);
    });
  }

  function render() {
    if (!state) return;
    renderHead();
    restoreIdea();
    if (!busy) showNotice(state.notice || softNotice || "");
    // what the site shows
    var live = state.live;
    var lf = live ? findFrontend(live.slug) : null;
    liveBox.hidden = !live;
    if (live) liveText.textContent = "On the site now: " + (lf ? "“" + lf.title + "”, " : "") + "version " + live.number + ". Only you can see it.";
    renderForm();
    renderList();
    renderPaused();
    minBtn.hidden = !busy;
    applyGen();
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
        if (state.enabled && !f.running && !(busy && busy.slug === f.slug)) {
          var again = el("p", "bm-fe-note", "Not built yet. ");
          again.append(keyed(button("bm-link", "Build it", function () {
            chooseMode({ kind: "change", slug: f.slug, rev: "" });
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
        var prompt = rv.prompt || "";
        if (rv.prompt) b.append(el("span", "bm-rev-prompt", "“" + rv.prompt + "”"));
        if (f.status.revision === rv.id && f.status.key !== "draft") b.append(el("span", "bm-chip bm-chip-" + f.status.key, f.status.label));
        item.append(b);
        // the full prompt (the list shows three lines of it)
        if (prompt) item.append(keyed(copyButton(function () { return prompt; }, "Copy the prompt for version " + rv.number), "copy:" + rv.id));
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
            chooseMode({ kind: "change", slug: f.slug, rev: selected.id });
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
      if (!busy) chooseMode({ kind: "change", slug: f.slug, rev: rv.id });
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
      // the target stays as chosen: the switch shows what the prompt will change
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

  // setPill: what the site bar's Re-imagine button says about a build: while
  // it runs, "Building…" on moving stripes (html.bm-generating); after,
  // briefly how it went ("Version 3 is ready"), then back to Re-imagine it
  var statusTimer = 0;
  function setPill(text, done) {
    var btn = document.getElementById("make-own");
    var st = btn && btn.querySelector(".make-own-status");
    text = String(text || "").replace(/\s*\u00b7\s*Open$/, "");
    if (!done) text = "Building\u2026";
    if (st) st.textContent = text;
    if (btn) btn.setAttribute("aria-label", done ? text + ". Open the builder" : "Building. Open the builder to follow it or cancel");
    clearTimeout(statusTimer);
    root.classList.toggle("bm-btn-status", !!done);
    if (done) statusTimer = setTimeout(clearPill, 12000);
  }
  function clearPill() {
    clearTimeout(statusTimer);
    root.classList.remove("bm-btn-status");
    if (!genInfo()) hintPaused(pauseHint);
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
    softNotice = "";
    showNotice("");
    var kept = readIdea();
    if (kept && kept.text.trim() === prompt) writeIdea(""); // the kept idea is being built
    resetProgress();
    var m = mode;
    busy = { slug: m.slug, title: "", prompt: prompt, startedAt: Date.now() };
    renderForm();
    renderHead();
    applyGen();
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
      // the studio closed under us (the budget, or the API's own limit): not an error
      if (res.outcome === "paused") return pausedNow(prompt, res.limit ? { limit: res.limit } : null);
      // the connection dropped mid-run: keep checking, and show the version when it lands
      if (res.outcome === "dropped") follow = { slug: res.slug, had: res.had };
      if (res.outcome !== "revision") {
        progress.classList.toggle("bm-failed", res.outcome === "error");
        if (res.outcome === "error") setPill("It didn't work · Open", true);
        if (res.outcome === "canceled") {
          pStatus.textContent = "Canceled. Nothing was changed.";
          setPill("Canceled · Open", true);
        }
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
        chooseMode({ kind: "change", slug: res.slug, rev: res.revision });
        pStatus.textContent = "Version " + res.number + " is on the site now. Want to change anything?";
        setPill("Version " + res.number + " is ready · Open", true);
        return finish(true);
      });
    }).catch(function (err) {
      if (err && err.key === "paused") return pausedNow(prompt, err.paused);
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
    if (!genInfo()) clearPill();
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
    // (paused: the state's heading, read first)
    var target = state && state.paused ? psHead : !form.hidden && !ta.disabled ? ta : dialog;
    target.focus({ preventScroll: true });
  }

  // hide closes the sheet; the exit is a short reverse of the entrance (CSS),
  // after which it leaves the page's layout
  var hideTimer = 0;
  function hide() {
    isOpen = false;
    softNotice = "";
    setTimeout(applyGen, 0); // a build still going: the pill keeps it in view
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
    var btn = document.getElementById("make-own");
    if (btn) btn.focus(); // the Re-imagine button now says Building…
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
    if (!state) hintPaused(d.paused || null); // once loaded, the builder's own state is fresher
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

  // a build was running before this page loaded: pick it back up
  try { if (localStorage.getItem(GEN_KEY)) refresh(); } catch (e) { /* storage off */ }

  try {
    var params = new URLSearchParams(location.search);
    if (params.has("build")) {
      var expired = params.get("build") === "expired";
      params.delete("build");
      var qs = params.toString();
      history.replaceState(history.state, "", location.pathname + (qs ? "?" + qs : "") + location.hash);
      open();
      // an email's link that's too old (v1.13)
      if (expired) { softNotice = "That link has expired. Anything you made in this browser is below."; showNotice(softNotice); }
    }
  } catch (e) { /* an old browser: the button still works */ }
})();
