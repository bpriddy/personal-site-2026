// builder.js: the builder chat. Posts Ben's prompt, streams the run's
// server-sent events (text, thinking, file operations) into the page, and
// when the run saves a revision, reloads onto it so the preview shows it.
(function () {
  "use strict";

  var form = document.getElementById("chat-form");
  if (!form) return;
  var streamBox = document.getElementById("chat-stream");
  var statusEl = document.getElementById("chat-status");
  var thinkingEl = document.getElementById("chat-thinking");
  var textEl = document.getElementById("chat-text");
  var toolsEl = document.getElementById("chat-tools");
  var chat = document.getElementById("chat");

  function addTool(text, cls) {
    var li = document.createElement("li");
    if (cls) li.className = cls;
    li.textContent = text;
    toolsEl.appendChild(li);
  }

  function addTurn(role, text) {
    var li = document.createElement("li");
    li.className = "b-turn b-" + role;
    var who = document.createElement("div");
    who.className = "b-who";
    who.textContent = role === "user" ? "Ben" : "Claude";
    var p = document.createElement("p");
    p.textContent = text;
    li.append(who, p);
    chat.appendChild(li);
  }

  function handle(ev) {
    switch (ev.type) {
      case "status":
        statusEl.textContent = ev.text || "";
        break;
      case "thinking":
        thinkingEl.textContent += ev.text || "";
        thinkingEl.scrollTop = thinkingEl.scrollHeight;
        break;
      case "text":
        textEl.textContent += ev.text || "";
        break;
      case "tool":
        addTool((ev.tool || "") + (ev.path ? " " + ev.path : "") + (ev.text ? " (" + ev.text + ")" : ""));
        break;
      case "warning":
        addTool("⚠ " + (ev.text || ""), "b-warn");
        break;
      case "error":
        statusEl.textContent = "Failed: " + (ev.text || "unknown error");
        statusEl.className = "b-error";
        return "error";
      case "revision":
        statusEl.textContent = "Saved r" + ev.number + ". Loading the preview…";
        addTurn("assistant", ev.text || "");
        form.setAttribute("data-new-rev", ev.revision);
        return "revision";
    }
    return "";
  }

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    var ta = form.querySelector("textarea");
    var btn = form.querySelector("button");
    var prompt = ta.value.trim();
    if (!prompt) return;
    ta.disabled = btn.disabled = true;
    addTurn("user", prompt);
    streamBox.hidden = false;
    statusEl.className = "b-muted";
    statusEl.textContent = "Starting…";
    thinkingEl.textContent = textEl.textContent = "";
    toolsEl.replaceChildren();
    var outcome = "";

    fetch(form.getAttribute("data-action"), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ prompt: prompt, parent: form.getAttribute("data-parent") || "" })
    }).then(function (r) {
      if (!r.ok) return r.text().then(function (t) { throw new Error(t || "HTTP " + r.status); });
      var reader = r.body.getReader();
      var dec = new TextDecoder();
      var buf = "";
      function pump() {
        return reader.read().then(function (chunk) {
          if (chunk.done) return;
          buf += dec.decode(chunk.value, { stream: true });
          var i;
          while ((i = buf.indexOf("\n\n")) >= 0) {
            var frame = buf.slice(0, i);
            buf = buf.slice(i + 2);
            frame.split("\n").forEach(function (line) {
              if (line.indexOf("data: ") !== 0) return;
              try {
                var o = handle(JSON.parse(line.slice(6)));
                if (o) outcome = o;
              } catch (err) { /* ignore a malformed frame */ }
            });
          }
          return pump();
        });
      }
      return pump();
    }).then(function () {
      if (outcome === "revision") {
        location.href = location.pathname + "?rev=" + encodeURIComponent(form.getAttribute("data-new-rev"));
        return;
      }
      if (!outcome) {
        statusEl.textContent = "The connection ended before the run finished. It may still complete: reload in a minute.";
      }
      ta.disabled = btn.disabled = false;
    }).catch(function (err) {
      statusEl.className = "b-error";
      statusEl.textContent = "Failed: " + (err && err.message ? err.message : String(err));
      ta.disabled = btn.disabled = false;
    });
  });
})();
