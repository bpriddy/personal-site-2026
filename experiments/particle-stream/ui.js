// UI + dev tooling glue, extracted from index.html to keep the markup clean.
// Loaded as a classic <script> at end-of-body, so:
//   • the DOM is fully parsed (getElementById works);
//   • it runs BEFORE the wasm (Trunk injects that as a deferred type=module
//     script, which always executes after classic scripts) — so the GPU shim is
//     installed and window.__DIALS / __initDials / __initPhrases exist before
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
window.__PHRASES = [];
try {
  var sp = JSON.parse(localStorage.getItem("phrases") || "null");
  if (Array.isArray(sp) && sp.length) window.__PHRASES = sp;
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

  // ── phrase editor: window.__PHRASES (a JSON string array) read by the wasm ──
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
  });
  function fillPhrases() {
    if (document.activeElement !== phraseText) {
      phraseText.value = JSON.stringify(window.__PHRASES || [], null, 2);
    }
  }
  // wasm calls this with baked phrases.json; localStorage edits win
  window.__initPhrases = function (arr) {
    if (!window.__PHRASES || !window.__PHRASES.length) {
      window.__PHRASES = Array.prototype.slice.call(arr);
    }
    fillPhrases();
  };
  // edit the whole phrases array as JSON; only commit when it parses to an array of
  // strings, so a half-typed edit can't break the running viz.
  phraseText.addEventListener("input", function () {
    var parsed = null;
    try { parsed = JSON.parse(phraseText.value); } catch (e) {}
    var ok = Array.isArray(parsed) && parsed.every(function (p) { return typeof p === "string"; });
    phraseText.classList.toggle("invalid", !ok && phraseText.value.trim() !== "");
    if (ok) {
      window.__PHRASES = parsed;
      try { localStorage.setItem("phrases", JSON.stringify(parsed)); } catch (e) {}
    }
  });
  fillPhrases();
  // SAVE writes phrases.json IN PLACE via the File System Access API:
  // pick the repo's phrases.json once (handle persisted in IndexedDB),
  // then every later save overwrites it silently — no download dialog.
  // Falls back to a download where the API is unavailable.
  function idbHandle(method, val) {
    return new Promise(function (resolve) {
      var op = indexedDB.open("bp-fs", 1);
      op.onupgradeneeded = function () { op.result.createObjectStore("h"); };
      op.onsuccess = function () {
        var tx = op.result.transaction("h", method === "get" ? "readonly" : "readwrite");
        var st = tx.objectStore("h");
        var rq = method === "get" ? st.get("phrases") : st.put(val, "phrases");
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
  var phrasesHandle = null;
  async function savePhrases() {
    var json = JSON.stringify(window.__PHRASES || [], null, 2) + "\n";
    try { navigator.clipboard.writeText(json); } catch (e) {}
    if (window.showSaveFilePicker) {
      try {
        if (!phrasesHandle) phrasesHandle = await idbHandle("get");
        if (phrasesHandle) {
          var perm = await phrasesHandle.queryPermission({ mode: "readwrite" });
          if (perm !== "granted") perm = await phrasesHandle.requestPermission({ mode: "readwrite" });
          if (perm !== "granted") phrasesHandle = null;
        }
        if (!phrasesHandle) {
          phrasesHandle = await window.showSaveFilePicker({
            suggestedName: "phrases.json",
            types: [{ description: "JSON", accept: { "application/json": [".json"] } }]
          });
          await idbHandle("put", phrasesHandle);
        }
        var w = await phrasesHandle.createWritable();
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
    a.download = "phrases.json";
    a.click();
    flashSave("downloaded");
  }
  document.getElementById("phrase-save").addEventListener("click", savePhrases);
  document.getElementById("phrase-close").addEventListener("click", function () {
    phrasePanel.classList.add("hidden");
    if (isDev) phraseBtn.style.display = "block";
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
