// builder-create.js: images on the admin builder's "Describe a new front end"
// form (templates/admin/builder.html). The form posts to /admin/builder/new,
// which redirects to the new front end's chat with #start=<prompt>; images
// can't ride in that URL, so they are uploaded first (/admin/builder/uploads)
// and their /media/ paths go along instead (builder.js sends them by path).
(function () {
  "use strict";

  var form = document.querySelector("form.b-create");
  var bar = document.getElementById("new-attach");
  if (!form || !bar || !window.BuilderAttach) return;
  var ta = form.querySelector("textarea");
  var hidden = form.querySelector("input[name=images]");
  var btn = form.querySelector("button.b-primary");
  var err = document.createElement("p");
  err.className = "b-error";
  err.hidden = true;
  bar.after(err);
  var attach = window.BuilderAttach.mount({
    textarea: ta, bar: bar, max: 4,
    onError: function (msg) { err.textContent = msg; err.hidden = false; }
  });
  var uploading = false;

  form.addEventListener("submit", function (e) {
    var images = attach.images();
    if (!images.length || hidden.value) return; // nothing to upload, or done: post it
    e.preventDefault();
    if (uploading || !form.reportValidity()) return;
    uploading = true;
    btn.disabled = true;
    err.hidden = true;
    var label = btn.textContent;
    btn.textContent = "Uploading images…";
    fetch("/admin/builder/uploads", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ images: images })
    }).then(function (r) {
      if (!r.ok) return r.text().then(function (t) { throw new Error(t.trim() || "HTTP " + r.status); });
      return r.json();
    }).then(function (res) {
      hidden.value = (res.paths || []).join(",");
      form.submit();
    }).catch(function (e2) {
      uploading = false;
      btn.disabled = false;
      btn.textContent = label;
      err.textContent = "The images didn't upload: " + e2.message;
      err.hidden = false;
    });
  });
})();
