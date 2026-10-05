// attach.js: images attached to a builder prompt, shared by the admin chat
// (builder.js) and the public builder modal (build-modal.js).
//
// BuilderAttach.mount({ textarea, bar, max, onChange, onError }) adds an
// "Add image" button and a row of thumbnails to `bar`, and takes images
// pasted into or dropped on the textarea. Big images are downscaled in the
// browser (longest side 2048 px) so they stay under the server's 5 MB limit;
// small PNG, JPEG, WebP and GIF files go as they are (a GIF keeps its frames).
// It returns { images() → [base64...], clear(), setDisabled(bool) }.
(function () {
  "use strict";

  var MAX_SIDE = 2048;
  var MAX_BYTES = 4 << 20; // under the server's 5 MB, with room
  var TYPES = ["image/png", "image/jpeg", "image/webp", "image/gif"];

  function readDataURL(blob) {
    return new Promise(function (resolve, reject) {
      var r = new FileReader();
      r.onload = function () { resolve(r.result); };
      r.onerror = function () { reject(r.error); };
      r.readAsDataURL(blob);
    });
  }

  function loadImage(src) {
    return new Promise(function (resolve, reject) {
      var img = new Image();
      img.onload = function () { resolve(img); };
      img.onerror = function () { reject(new Error("unreadable")); };
      img.src = src;
    });
  }

  // prepare resolves to a data: URL the server accepts: the file itself when
  // it's small enough, else a downscaled PNG (if it may have transparency)
  // or JPEG
  function prepare(file) {
    if (TYPES.indexOf(file.type) < 0) return Promise.reject(new Error("type"));
    return readDataURL(file).then(function (url) {
      return loadImage(url).then(function (img) {
        var w = img.naturalWidth, h = img.naturalHeight;
        if (file.size <= MAX_BYTES && Math.max(w, h) <= MAX_SIDE) return url;
        var k = Math.min(1, MAX_SIDE / Math.max(w, h));
        var c = document.createElement("canvas");
        c.width = Math.max(1, Math.round(w * k));
        c.height = Math.max(1, Math.round(h * k));
        c.getContext("2d").drawImage(img, 0, 0, c.width, c.height);
        var png = file.type === "image/png" || file.type === "image/webp" || file.type === "image/gif";
        var out = c.toDataURL(png ? "image/png" : "image/jpeg", 0.9);
        // a PNG that's still too big becomes a JPEG
        if (out.length * 0.75 > MAX_BYTES) out = c.toDataURL("image/jpeg", 0.85);
        return out;
      });
    });
  }

  function mount(o) {
    var max = o.max || 1;
    var items = []; // { url: "data:..." }
    var disabled = false;
    var onError = o.onError || function () {};
    var onChange = o.onChange || function () {};

    var input = document.createElement("input");
    input.type = "file";
    input.accept = TYPES.join(",");
    input.multiple = max > 1;
    input.hidden = true;
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "attach-btn";
    btn.setAttribute("aria-label", "Add an image");
    btn.title = "Add an image (or paste or drop one)";
    btn.innerHTML = '<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="10" r="1.8"/><path d="M21 16l-5.5-5.5L6 20"/></svg><span>Image</span>';
    var thumbs = document.createElement("ul");
    thumbs.className = "attach-thumbs";
    thumbs.hidden = true;
    o.bar.append(input, btn, thumbs);

    function render() {
      thumbs.replaceChildren();
      items.forEach(function (it, i) {
        var li = document.createElement("li");
        var img = document.createElement("img");
        img.src = it.url;
        img.alt = "Attached image " + (i + 1);
        var x = document.createElement("button");
        x.type = "button";
        x.className = "attach-remove";
        x.setAttribute("aria-label", "Remove image " + (i + 1));
        x.textContent = "×";
        x.disabled = disabled;
        x.addEventListener("click", function () { items.splice(i, 1); render(); });
        li.append(img, x);
        thumbs.appendChild(li);
      });
      thumbs.hidden = items.length === 0;
      btn.disabled = disabled || items.length >= max;
      onChange(items.length);
    }

    function add(files) {
      files = Array.prototype.filter.call(files || [], function (f) { return f && /^image\//.test(f.type); });
      if (!files.length || disabled) return;
      var room = max - items.length;
      if (room <= 0) { onError(max === 1 ? "You can attach one image." : "You can attach up to " + max + " images."); return; }
      if (files.length > room) onError("Only the first " + room + " image" + (room === 1 ? "" : "s") + " were added.");
      files.slice(0, room).forEach(function (f) {
        prepare(f).then(function (url) {
          if (items.length < max) { items.push({ url: url }); render(); }
        }, function (err) {
          onError(err && err.message === "type" ? "Use a PNG, JPEG, WebP or GIF image." : "That image couldn't be read.");
        });
      });
    }

    btn.addEventListener("click", function () { input.click(); });
    input.addEventListener("change", function () { add(input.files); input.value = ""; });
    o.textarea.addEventListener("paste", function (e) {
      var files = e.clipboardData && e.clipboardData.files;
      if (files && files.length) { e.preventDefault(); add(files); }
    });
    o.textarea.addEventListener("dragover", function (e) {
      if (e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types, "Files") >= 0) {
        e.preventDefault();
        o.textarea.classList.add("attach-over");
      }
    });
    o.textarea.addEventListener("dragleave", function () { o.textarea.classList.remove("attach-over"); });
    o.textarea.addEventListener("drop", function (e) {
      o.textarea.classList.remove("attach-over");
      if (e.dataTransfer && e.dataTransfer.files.length) { e.preventDefault(); add(e.dataTransfer.files); }
    });
    render();

    return {
      // the attached images as base64 (no data: prefix), for the chat request
      images: function () { return items.map(function (it) { return it.url.slice(it.url.indexOf(",") + 1); }); },
      // their data: URLs, to show them in the conversation
      urls: function () { return items.map(function (it) { return it.url; }); },
      clear: function () { items = []; render(); },
      setDisabled: function (d) { disabled = !!d; render(); }
    };
  }

  window.BuilderAttach = { mount: mount };
})();
