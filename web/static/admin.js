// admin.js: admin-page behavior (the observer notification dot). Owned by the observer work.
// Fills #observer-dot in the admin nav from GET /admin/observer/unseen.json:
// a count badge of detections not yet viewed, hidden at 0. Per-row "new" dots
// on the observer page are rendered by the server.
(function () {
  "use strict";
  var dot = document.getElementById("observer-dot");
  if (!dot || !window.fetch) return;

  function show(n) {
    n = Number(n) || 0;
    dot.textContent = n > 99 ? "99+" : String(n);
    dot.hidden = n <= 0;
    dot.setAttribute("aria-label", n + " new observer " + (n === 1 ? "detection" : "detections"));
  }

  function refresh() {
    fetch("/admin/observer/unseen.json", { credentials: "same-origin", cache: "no-store" })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) { if (d) show(d.unseen); })
      .catch(function () {});
  }

  refresh();
  setInterval(function () { if (!document.hidden) refresh(); }, 60000);
})();

// mark the current admin nav tab
(function () {
  var path = location.pathname;
  var links = document.querySelectorAll(".site-header nav a");
  var best = null;
  for (var i = 0; i < links.length; i++) {
    var href = links[i].getAttribute("href");
    if (href && href !== "/" && path.indexOf(href) === 0 && (!best || href.length > best.getAttribute("href").length)) best = links[i];
  }
  if (best) best.setAttribute("aria-current", "page");
})();
