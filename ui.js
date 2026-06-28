// UI + dev tooling glue, extracted from index.html to keep the markup clean.
// Loaded as a classic <script> at end-of-body, so:
//   • the DOM is fully parsed (getElementById works);
//   • it runs BEFORE the wasm (Trunk injects that as a deferred type=module
//     script, which always executes after classic scripts) — so the GPU shim is
//     installed and window.__DIALS / __initDials / __initSections exist before
//     the wasm's init touches them.

// ── debug shim: surface WebGPU uncaptured errors through console.error ──
(function () {
  if (!navigator.gpu) return;
  const ra = navigator.gpu.requestAdapter.bind(navigator.gpu);
  navigator.gpu.requestAdapter = function () {
    return ra.apply(null, arguments).then((ad) => {
      if (!ad) return ad;
      const rd = ad.requestDevice.bind(ad);
      ad.requestDevice = function () {
        return rd.apply(null, arguments).then((dev) => {
          dev.addEventListener("uncapturederror", (e) =>
            console.error("[gpu-uncaptured]", e.error && e.error.message)
          );
          dev.lost.then((i) => console.error("[gpu-lost]", i.reason, i.message));
          return dev;
        });
      };
      return ad;
    });
  };
})();

// tuning dials: window.__DIALS is read by the wasm every frame.
// values come from dials.json (embedded in the wasm, pushed here via
// __initDials) overlaid with this browser's localStorage tweaks. The
// SAVE button downloads dials.json — drop it in the repo root and the
// next build ships exactly these values.
window.__DIALS = {};
window.__SECTIONS = [];
try {
  var sp = JSON.parse(localStorage.getItem("sections") || "null");
  if (Array.isArray(sp) && sp.length) window.__SECTIONS = sp;
} catch (e) {}
(function () {
  try {
    var saved = JSON.parse(localStorage.getItem("dials") || "{}");
    for (var k in saved) window.__DIALS[k] = saved[k];
  } catch (e) {}
  var panel = document.getElementById("dials");
  var gear = document.getElementById("gear");
  var panel2 = document.getElementById("dials2");
  var gear2 = document.getElementById("gear2");
  var fps = document.getElementById("fps");
  // dev tooling is visible locally only; on the deployed site the 'd'/'f'
  // keys remain as a hidden door to the dials + fps readout
  var isDev = ["localhost", "127.0.0.1"].indexOf(location.hostname) !== -1;
  if (!isDev) {
    fps.style.display = "none";
    gear.style.display = "none";
    gear2.style.display = "none";
  }
  // toggle for one [panel, gear] pair; opening one closes its sibling so
  // the two dial menus never overlap
  function makeToggle(pnl, gr, other, otherGr) {
    return function () {
      var opening = pnl.classList.contains("hidden");
      if (opening) {
        other.classList.add("hidden");
        if (isDev) otherGr.style.display = "block";
        pnl.classList.remove("hidden");
      } else {
        pnl.classList.add("hidden");
      }
      if (isDev) {
        gr.style.display = opening ? "none" : "block";
      } else {
        var anyOpen = !pnl.classList.contains("hidden") || !other.classList.contains("hidden");
        fps.style.display = anyOpen ? "block" : "none";
      }
    };
  }
  var toggle = makeToggle(panel, gear, panel2, gear2);
  var toggle2 = makeToggle(panel2, gear2, panel, gear);
  gear.addEventListener("click", toggle);
  gear2.addEventListener("click", toggle2);
  document.getElementById("dial-close").addEventListener("click", toggle);
  document.getElementById("dial-close2").addEventListener("click", toggle2);
  document.addEventListener("keydown", function (e) {
    var t = e.target && e.target.tagName;
    if (t === "TEXTAREA" || t === "INPUT") return;
    if (e.key === "d" && !e.metaKey && !e.ctrlKey) toggle();
    if (e.key === "f" && !e.metaKey && !e.ctrlKey) toggle2();
  });

  // ── section editor: window.__SECTIONS (JSON) read by the wasm each cycle ──
  var phrasePanel = document.getElementById("phrases");
  var phraseBtn = document.getElementById("phrase-btn");
  var phraseText = document.getElementById("phrase-text");
  if (!isDev) phraseBtn.style.display = "none";
  function togglePhrases() {
    phrasePanel.classList.toggle("hidden");
    var open = !phrasePanel.classList.contains("hidden");
    if (isDev) phraseBtn.style.display = open ? "none" : "block";
  }
  phraseBtn.addEventListener("click", togglePhrases);
  document.addEventListener("keydown", function (e) {
    var t = e.target && e.target.tagName;
    if (t === "TEXTAREA" || t === "INPUT") return;
    if (e.key === "p" && !e.metaKey && !e.ctrlKey) togglePhrases();
    // dev: 't' toggles the section-title scrub view (until swipe-entry, stage D)
    if (e.key === "t" && !e.metaKey && !e.ctrlKey) {
      window.__DIALS = window.__DIALS || {};
      window.__DIALS.title_on = window.__DIALS.title_on > 0.5 ? 0 : 1;
    }
  });
  function fillSections() {
    if (document.activeElement !== phraseText) {
      phraseText.value = JSON.stringify(window.__SECTIONS || [], null, 2);
    }
  }
  // wasm calls this with baked sections.json; localStorage edits win
  window.__initSections = function (arr) {
    if (!window.__SECTIONS || !window.__SECTIONS.length) {
      window.__SECTIONS = Array.prototype.slice.call(arr);
    }
    fillSections();
  };
  // edit the whole sections array as JSON; only commit when it parses to an
  // array, so a half-typed edit can't break the running viz.
  phraseText.addEventListener("input", function () {
    var parsed = null;
    try { parsed = JSON.parse(phraseText.value); } catch (e) {}
    var ok = Array.isArray(parsed);
    phraseText.classList.toggle("invalid", !ok && phraseText.value.trim() !== "");
    if (ok) {
      window.__SECTIONS = parsed;
      try { localStorage.setItem("sections", JSON.stringify(parsed)); } catch (e) {}
    }
  });
  fillSections();
  // SAVE writes sections.json IN PLACE via the File System Access API:
  // pick the repo's sections.json once (handle persisted in IndexedDB),
  // then every later save overwrites it silently — no download dialog.
  // Falls back to a download where the API is unavailable.
  function idbHandle(method, val) {
    return new Promise(function (resolve) {
      var op = indexedDB.open("bp-fs", 1);
      op.onupgradeneeded = function () { op.result.createObjectStore("h"); };
      op.onsuccess = function () {
        var tx = op.result.transaction("h", method === "get" ? "readonly" : "readwrite");
        var st = tx.objectStore("h");
        var rq = method === "get" ? st.get("sections") : st.put(val, "sections");
        rq.onsuccess = function () { resolve(method === "get" ? rq.result : true); };
        rq.onerror = function () { resolve(null); };
      };
      op.onerror = function () { resolve(null); };
    });
  }
  function flashSave(msg) {
    var b = document.getElementById("phrase-save");
    if (!b.dataset.label) b.dataset.label = b.textContent;
    b.textContent = msg;
    setTimeout(function () { b.textContent = b.dataset.label; }, 1300);
  }
  var sectionsHandle = null;
  async function savePhrases() {
    var json = JSON.stringify(window.__SECTIONS || [], null, 2) + "\n";
    try { navigator.clipboard.writeText(json); } catch (e) {}
    if (window.showSaveFilePicker) {
      try {
        if (!sectionsHandle) sectionsHandle = await idbHandle("get");
        if (sectionsHandle) {
          var perm = await sectionsHandle.queryPermission({ mode: "readwrite" });
          if (perm !== "granted") perm = await sectionsHandle.requestPermission({ mode: "readwrite" });
          if (perm !== "granted") sectionsHandle = null;
        }
        if (!sectionsHandle) {
          sectionsHandle = await window.showSaveFilePicker({
            suggestedName: "sections.json",
            types: [{ description: "JSON", accept: { "application/json": [".json"] } }]
          });
          await idbHandle("put", sectionsHandle);
        }
        var w = await sectionsHandle.createWritable();
        await w.write(json);
        await w.close();
        flashSave("saved ✓");
        return;
      } catch (e) {
        if (e && e.name === "AbortError") return;
      }
    }
    var a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([json], { type: "application/json" }));
    a.download = "sections.json";
    a.click();
    flashSave("downloaded");
  }
  document.getElementById("phrase-save").addEventListener("click", savePhrases);
  document.getElementById("phrase-close").addEventListener("click", function () {
    phrasePanel.classList.add("hidden");
    if (isDev) phraseBtn.style.display = "block";
  });

  // ── camera-path editor: window.__PATH (nested [[x,y,w],…]) read LIVE by the wasm ──
  // The wasm pushes a simplified seed (window.__PATH_SEED, flat) plus the baked
  // paths.json (via __initPath). Editing sets window.__PATH and bumps __PATH_VER so
  // the frame loop re-reads it; SAVE downloads paths.json for the repo. Empty/no
  // override → the renderer keeps the dense derived skeleton.
  var pathPanel = document.getElementById("paths");
  var pathBtn = document.getElementById("path-btn");
  var pathText = document.getElementById("path-text");
  if (!isDev) pathBtn.style.display = "none";
  // one waypoint per line, 3-decimal precision — editable, still valid JSON
  function fmtPath(arr) {
    return "[\n" + arr.map(function (p) {
      return "  [" + p.map(function (n) { return Number(n).toFixed(3); }).join(", ") + "]";
    }).join(",\n") + "\n]";
  }
  function seedPath() {
    var s = window.__PATH_SEED, out = [];
    if (s) for (var i = 0; i + 2 < s.length; i += 3) out.push([s[i], s[i + 1], s[i + 2]]);
    return out;
  }
  function applyPath(arr) { // live override + version bump so the wasm re-reads
    window.__PATH = arr;
    window.__PATH_VER = (window.__PATH_VER || 0) + 1;
  }
  function fillPath(arr) {
    if (document.activeElement !== pathText) pathText.value = fmtPath(arr);
  }
  // wasm calls this with the baked paths.json (may be empty); localStorage wins.
  window.__initPath = function (baked) {
    var saved = null;
    try { saved = JSON.parse(localStorage.getItem("path") || "null"); } catch (e) {}
    var chosen = (Array.isArray(saved) && saved.length) ? saved
      : (Array.isArray(baked) && baked.length) ? Array.prototype.slice.call(baked) : null;
    if (chosen) { applyPath(chosen); fillPath(chosen); }
    else fillPath(seedPath()); // no override → show the seed for editing, but leave
    // __PATH unset so the renderer keeps the full-resolution derived skeleton
  };
  function togglePaths() {
    pathPanel.classList.toggle("hidden");
    var open = !pathPanel.classList.contains("hidden");
    if (isDev) pathBtn.style.display = open ? "none" : "block";
    if (open && !pathText.value.trim()) fillPath(seedPath());
  }
  pathBtn.addEventListener("click", togglePaths);
  document.addEventListener("keydown", function (e) {
    var t = e.target && e.target.tagName;
    if (t === "TEXTAREA" || t === "INPUT") return;
    if (e.key === "w" && !e.metaKey && !e.ctrlKey) togglePaths();
  });
  pathText.addEventListener("input", function () {
    var parsed = null;
    try { parsed = JSON.parse(pathText.value); } catch (e) {}
    var ok = Array.isArray(parsed) && parsed.length > 0 && parsed.every(function (p) {
      return Array.isArray(p) && p.length >= 3 &&
        typeof p[0] === "number" && typeof p[1] === "number" && typeof p[2] === "number";
    });
    pathText.classList.toggle("invalid", !ok && pathText.value.trim() !== "");
    if (ok) {
      applyPath(parsed);
      try { localStorage.setItem("path", JSON.stringify(parsed)); } catch (e) {}
    }
  });
  document.getElementById("path-reseed").addEventListener("click", function () {
    var s = seedPath();
    pathText.value = fmtPath(s);
    applyPath(s);
    try { localStorage.setItem("path", JSON.stringify(s)); } catch (e) {}
  });
  document.getElementById("path-close").addEventListener("click", function () {
    pathPanel.classList.add("hidden");
    if (isDev) pathBtn.style.display = "block";
  });
  document.getElementById("path-save").addEventListener("click", function () {
    var arr = (Array.isArray(window.__PATH) && window.__PATH.length) ? window.__PATH : seedPath();
    var json = fmtPath(arr) + "\n";
    try { navigator.clipboard.writeText(json); } catch (e) {}
    var a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([json], { type: "application/json" }));
    a.download = "paths.json";
    a.click();
  });

  // both dial menus share one __DIALS object + one dials.json
  function allSliders() {
    return document.querySelectorAll(
      "#dials input[type=range], #dials2 input[type=range]");
  }
  function syncSliders() {
    allSliders().forEach(function (el) {
      var key = el.dataset.dial;
      if (key in window.__DIALS) {
        el.value = window.__DIALS[key];
        el.parentElement.querySelector(".val").textContent =
          Number(el.value).toFixed(2);
      }
    });
  }
  // the wasm calls this once with the baked dials.json; localStorage wins
  window.__initDials = function (defaults) {
    for (var k in defaults)
      if (!(k in window.__DIALS)) window.__DIALS[k] = defaults[k];
    syncSliders();
  };
  allSliders().forEach(function (el) {
    el.addEventListener("input", function () {
      window.__DIALS[el.dataset.dial] = Number(el.value);
      el.parentElement.querySelector(".val").textContent =
        Number(el.value).toFixed(2);
      try { localStorage.setItem("dials", JSON.stringify(window.__DIALS)); } catch (e) {}
    });
  });
  syncSliders();
  function saveDials() {
    var json = JSON.stringify(window.__DIALS, null, 2) + "\n";
    try { navigator.clipboard.writeText(json); } catch (e) {}
    var a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([json], { type: "application/json" }));
    a.download = "dials.json";
    a.click();
  }
  document.getElementById("dial-save").addEventListener("click", saveDials);
  document.getElementById("dial-save2").addEventListener("click", saveDials);
})();
(function () {
  var backdrop = document.getElementById("modal-backdrop");
  var btn = document.getElementById("info-btn");
  function open() {
    backdrop.classList.add("open");
    requestAnimationFrame(function () { backdrop.classList.add("show"); });
  }
  function close() {
    backdrop.classList.remove("show");
    setTimeout(function () { backdrop.classList.remove("open"); }, 220);
  }
  btn.addEventListener("click", open);
  document.getElementById("modal-x").addEventListener("click", close);
  backdrop.addEventListener("click", function (e) {
    if (e.target === backdrop) close();
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") close();
  });
})();
