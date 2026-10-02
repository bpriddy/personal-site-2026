// media.js: plays the transcript's loops (docs/frontend-protocol.md, v1.4).
//
// The transcript's <video data-loop> elements carry no autoplay: the
// transcript is in every page, hidden while a front end runs, and must not
// download or play video nobody sees. This plays a loop only while it is on
// screen, the transcript is the visible site (no html.fe-loading / fe-live),
// and the visitor hasn't asked for reduced motion; otherwise the poster
// shows. data-loop="hover" loops (the work index) play on hover or focus
// with a fine pointer, and while on screen on touch screens. Without
// JavaScript every loop is its poster.
(function () {
  "use strict";

  if (typeof IntersectionObserver !== "function") return; // posters only

  var root = document.documentElement;
  var reduce = matchMedia("(prefers-reduced-motion: reduce)");
  var fine = matchMedia("(hover: hover) and (pointer: fine)");
  var videos = [];
  var state = typeof WeakMap === "function" ? new WeakMap() : null;
  if (!state) return;

  function transcriptShown() {
    return !root.classList.contains("fe-live") && !root.classList.contains("fe-loading");
  }

  function wanted(v) {
    var s = state.get(v);
    if (!s || !s.visible || reduce.matches || !transcriptShown() || !v.isConnected) return false;
    if (v.getAttribute("data-loop") === "hover" && fine.matches) return s.hover;
    return true;
  }

  function update(v) {
    if (wanted(v)) {
      if (v.paused) {
        v.muted = true; // autoplay policy: only muted video may start itself
        v.preload = "auto";
        var p = v.play();
        if (p && typeof p.catch === "function") p.catch(function () { /* the poster stays */ });
      }
    } else if (!v.paused) {
      v.pause();
    }
  }

  function updateAll() {
    videos = videos.filter(function (v) { return v.isConnected; });
    videos.forEach(update);
  }

  var io = new IntersectionObserver(function (entries) {
    entries.forEach(function (e) {
      var s = state.get(e.target);
      if (s) {
        s.visible = e.isIntersecting;
        update(e.target);
      }
    });
  }, { rootMargin: "120px 0px" });

  function attach(v) {
    if (state.has(v)) return;
    var s = { visible: false, hover: false };
    state.set(v, s);
    v.muted = true;
    videos.push(v);
    io.observe(v);
    if (v.getAttribute("data-loop") === "hover") {
      var host = v.closest("[data-hover-root]") || v;
      var on = function () { s.hover = true; update(v); };
      var off = function () { s.hover = false; update(v); };
      host.addEventListener("pointerenter", on);
      host.addEventListener("pointerleave", off);
      host.addEventListener("focusin", on);
      host.addEventListener("focusout", off);
    }
  }

  function scan(node) {
    if (!node || node.nodeType !== 1) return;
    if (node.matches("video[data-loop]")) attach(node);
    node.querySelectorAll("video[data-loop]").forEach(attach);
  }

  // the film facade (project pages): swap in YouTube's privacy-enhanced
  // player on click; the link (to youtube.com) is the no-JavaScript path
  var EMBED = /^https:\/\/www\.youtube-nocookie\.com\/embed\/[A-Za-z0-9_-]{11}(\?[A-Za-z0-9=&_-]*)?$/;
  document.addEventListener("click", function (e) {
    var a = e.target && e.target.closest ? e.target.closest(".film[data-embed] .film-play") : null;
    if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var film = a.parentNode;
    var src = film.getAttribute("data-embed") || "";
    if (!EMBED.test(src)) return;
    e.preventDefault();
    var f = document.createElement("iframe");
    f.src = src + (src.indexOf("?") < 0 ? "?" : "&") + "autoplay=1";
    f.title = film.getAttribute("data-title") || "Film";
    f.setAttribute("allow", "autoplay; encrypted-media; picture-in-picture; fullscreen");
    f.setAttribute("referrerpolicy", "strict-origin-when-cross-origin");
    film.replaceChildren(f);
    f.focus();
  });

  scan(document.body);
  // the parent swaps the transcript's <main> on navigation (frontend-host.js)
  new MutationObserver(function (records) {
    records.forEach(function (r) { r.addedNodes.forEach(scan); });
  }).observe(document.body, { childList: true, subtree: true });
  // a front end taking over (or falling back to the transcript)
  new MutationObserver(updateAll).observe(root, { attributes: true, attributeFilter: ["class"] });
  if (reduce.addEventListener) reduce.addEventListener("change", updateAll);
  if (fine.addEventListener) fine.addEventListener("change", updateAll);
})();
