// builder.js: the admin builder's chat (templates/admin/builder_frontend.html).
// The run itself (POST, server-sent events, wording) is builder-stream.js,
// shared with the public builder modal; this file draws it into the page and,
// when the run saves a revision, reloads onto it so the preview shows it.
//
// #chat-form attributes: data-action (the chat URL), data-parent (the
// revision to change), data-user-label / data-assistant-label (default "Ben"
// / "Claude").
(function () {
  "use strict";

  var form = document.getElementById("chat-form");
  if (!form || !window.BuilderStream) return;
  var streamBox = document.getElementById("chat-stream");
  var statusEl = document.getElementById("chat-status");
  var thinkingEl = document.getElementById("chat-thinking");
  var textEl = document.getElementById("chat-text");
  var toolsEl = document.getElementById("chat-tools");
  var chat = document.getElementById("chat");
  var userLabel = form.getAttribute("data-user-label") || "Ben";
  var assistantLabel = form.getAttribute("data-assistant-label") || "Claude";
  var statusClass = statusEl.className;
  var ta0 = form.querySelector("textarea");
  var attachBar = document.createElement("div");
  attachBar.className = "attach-bar";
  ta0.closest("label").after(attachBar);
  var attach = window.BuilderAttach ? window.BuilderAttach.mount({
    textarea: ta0, bar: attachBar, max: 4,
    onError: function (msg) { statusEl.textContent = msg; statusEl.className = "b-error"; streamBox.hidden = false; }
  }) : null;

  var startImages = []; // /media/ paths from the create form (#start=...&images=...)

  function addTurn(role, text, images) {
    var li = document.createElement("li");
    li.className = "b-turn b-" + role;
    var who = document.createElement("div");
    who.className = "b-who";
    who.textContent = role === "user" ? userLabel : assistantLabel;
    var p = document.createElement("p");
    p.textContent = text;
    li.append(who, p);
    if (images && images.length) {
      var box = document.createElement("div");
      box.className = "b-attached";
      images.forEach(function (u) { var img = document.createElement("img"); img.src = u; img.alt = ""; box.appendChild(img); });
      li.appendChild(box);
    }
    chat.appendChild(li);
  }

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    var ta = form.querySelector("textarea");
    var btn = form.querySelector("button:not([type=button])");
    var prompt = ta.value.trim();
    if (!prompt) return;
    ta.disabled = btn.disabled = true;
    var images = attach ? attach.images() : [];
    var attached = startImages;
    startImages = [];
    addTurn("user", prompt, (attach ? attach.urls() : []).concat(attached));
    if (attach) attach.setDisabled(true);
    streamBox.hidden = false;
    thinkingEl.textContent = textEl.textContent = "";
    toolsEl.replaceChildren();

    window.BuilderStream.run({
      url: form.getAttribute("data-action"),
      prompt: prompt,
      parent: form.getAttribute("data-parent") || "",
      images: images,
      attached: attached,
      assistantLabel: assistantLabel,
      onStatus: function (text, isError) {
        statusEl.textContent = text;
        statusEl.className = isError ? "b-error" : statusClass;
      },
      onThinking: function (t) {
        thinkingEl.textContent += t;
        thinkingEl.scrollTop = thinkingEl.scrollHeight;
      },
      onText: function (t) { textEl.textContent += t; },
      onTool: function (text, cls) {
        var li = document.createElement("li");
        if (cls) li.className = cls;
        li.textContent = text;
        toolsEl.appendChild(li);
      }
    }).then(function (res) {
      if (res.outcome === "revision") {
        statusEl.textContent = "Saved r" + res.number + ". Loading the preview…";
        addTurn("assistant", res.summary);
        location.href = location.pathname + "?rev=" + encodeURIComponent(res.revision);
        return;
      }
      ta.disabled = btn.disabled = false;
      if (attach) attach.setDisabled(false);
    });
  });

  // prompt-first creation lands here with #start=<prompt>: send it once
  (function () {
    var m = /^#start=([^&]*)(?:&images=(.*))?$/.exec(location.hash);
    if (!m) return;
    history.replaceState(null, "", location.pathname + location.search);
    var ta = form.querySelector("textarea");
    if (!ta || ta.disabled) return;
    try {
      ta.value = decodeURIComponent(m[1].replace(/\+/g, " "));
      if (m[2]) startImages = decodeURIComponent(m[2].replace(/\+/g, " ")).split(",").filter(function (p) { return /^\/media\/assets\/uploads\/[0-9a-f]+\.(png|jpg|webp|gif)$/.test(p); });
    } catch (e) { return; }
    if (ta.value.trim()) form.requestSubmit ? form.requestSubmit() : form.dispatchEvent(new Event("submit", { cancelable: true }));
  })();
})();
