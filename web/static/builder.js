// builder.js: the builder chat, shared by the admin builder and the public one
// (/build). Posts the prompt, streams the run's server-sent events (text,
// thinking, file operations) into the page, and when the run saves a
// revision, reloads onto it so the preview shows it.
//
// #chat-form attributes: data-action (the chat URL), data-parent (the
// revision to change), data-user-label / data-assistant-label (default "Ben"
// / "Claude"), data-friendly="1" (visitors: plain-language progress, server
// messages shown as they are, no internal warnings).
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
  var friendly = form.getAttribute("data-friendly") === "1";
  var userLabel = form.getAttribute("data-user-label") || "Ben";
  var assistantLabel = form.getAttribute("data-assistant-label") || "Claude";
  var UNAVAILABLE = "The builder is unavailable right now. Please try again in a little while.";
  var DONE_VERBS = { write_file: "Wrote", str_replace: "Edited", delete_file: "Removed", read_file: "Read" };
  var statusClass = statusEl.className;

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
    who.textContent = role === "user" ? userLabel : assistantLabel;
    var p = document.createElement("p");
    p.textContent = text;
    li.append(who, p);
    chat.appendChild(li);
  }

  function setStatus(text, isError) {
    statusEl.textContent = text;
    statusEl.className = isError ? "b-error" : statusClass;
  }

  function failText(msg) {
    if (friendly) return msg || UNAVAILABLE;
    return "Failed: " + (msg || "unknown error");
  }

  function handle(ev) {
    switch (ev.type) {
      case "status":
        if (friendly) {
          setStatus(ev.tool ? assistantLabel + " is writing your front end…" : assistantLabel + " is working on it. This usually takes a minute or two…");
        } else {
          setStatus(ev.text || "");
        }
        break;
      case "thinking":
        thinkingEl.textContent += ev.text || "";
        thinkingEl.scrollTop = thinkingEl.scrollHeight;
        break;
      case "text":
        textEl.textContent += ev.text || "";
        break;
      case "tool":
        if (friendly) {
          if (DONE_VERBS[ev.tool] && ev.path) addTool(DONE_VERBS[ev.tool] + " " + ev.path);
        } else {
          addTool((ev.tool || "") + (ev.path ? " " + ev.path : "") + (ev.text ? " (" + ev.text + ")" : ""));
        }
        break;
      case "warning":
        if (!friendly) addTool("⚠ " + (ev.text || ""), "b-warn");
        break;
      case "error":
        setStatus(failText(ev.text), true);
        return "error";
      case "revision":
        setStatus(friendly ? "Version " + ev.number + " is ready. Loading it…" : "Saved r" + ev.number + ". Loading the preview…");
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
    setStatus("Starting…");
    thinkingEl.textContent = textEl.textContent = "";
    toolsEl.replaceChildren();
    var outcome = "";

    fetch(form.getAttribute("data-action"), {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ prompt: prompt, parent: form.getAttribute("data-parent") || "" })
    }).then(function (r) {
      if (!r.ok) {
        return r.text().then(function (t) {
          // for visitors, only these carry a message written for them
          if (friendly && [400, 409, 429, 503].indexOf(r.status) < 0) t = "";
          throw new Error((t || "").trim() || (friendly ? "" : "HTTP " + r.status));
        });
      }
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
        setStatus(friendly
          ? "The connection dropped, but " + assistantLabel + " may still be working on it. Reload the page in a minute."
          : "The connection ended before the run finished. It may still complete: reload in a minute.");
      }
      ta.disabled = btn.disabled = false;
    }).catch(function (err) {
      var msg = err && typeof err.message === "string" ? err.message : String(err);
      // a network failure's message is the browser's, not ours
      if (friendly && err instanceof TypeError) msg = "";
      setStatus(failText(msg), true);
      ta.disabled = btn.disabled = false;
    });
  });
  // prompt-first creation lands here with #start=<prompt>: send it once
  (function () {
    var m = /^#start=(.*)$/.exec(location.hash);
    if (!m) return;
    history.replaceState(null, "", location.pathname + location.search);
    var ta = form.querySelector("textarea");
    if (!ta || ta.disabled) return;
    try { ta.value = decodeURIComponent(m[1].replace(/\+/g, " ")); } catch (e) { return; }
    if (ta.value.trim()) form.requestSubmit ? form.requestSubmit() : form.dispatchEvent(new Event("submit", { cancelable: true }));
  })();
})();
