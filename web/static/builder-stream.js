// builder-stream.js: one builder chat run, shared by the admin builder
// (builder.js) and the public builder modal (build-modal.js). It posts the
// prompt to a chat URL, reads the run's server-sent events (status, thinking,
// text, tool, warning, error, revision, done; see internal/builder.Event) and
// reports them through callbacks in words fit for the caller: Ben's raw ones,
// or plain language for visitors (friendly).
//
//   BuilderStream.run({
//     url, prompt, parent,          // POST url, JSON {prompt, parent}
//     extra,                        // more JSON fields (e.g. Ben's {overBudget: true})
//     friendly, assistantLabel,     // visitors: plain words; default "Claude"
//     onStatus(text, isError), onThinking(text), onText(text), onTool(text, cls)
//   }) → Promise<{outcome: "revision" | "error" | "dropped" | "canceled" | "paused", revision, number, summary, message, limit}>
//
// The promise never rejects; every failure ends in onStatus(text, true).
// "paused" (v1.12): building is paused for the budget, refused up front (an
// X-Build-Paused header, its value the limit) or by the API mid-run (a
// "paused" event); for visitors it isn't an error, so onStatus isn't told.
(function () {
  "use strict";

  var UNAVAILABLE = "The builder is unavailable right now. Please try again in a little while.";
  var DONE_VERBS = { write_file: "Wrote", str_replace: "Edited", delete_file: "Removed", read_file: "Read" };

  function noop() {}

  function run(o) {
    var friendly = !!o.friendly;
    var who = o.assistantLabel || "Claude";
    var onStatus = o.onStatus || noop, onThinking = o.onThinking || noop;
    var onText = o.onText || noop, onTool = o.onTool || noop;
    var result = { outcome: "", revision: "", number: 0, summary: "", message: "" };

    function failText(msg) {
      if (friendly) return msg || UNAVAILABLE;
      return "Failed: " + (msg || "unknown error");
    }

    function handle(ev) {
      switch (ev.type) {
        case "status":
          if (friendly) {
            onStatus(ev.tool ? who + " is writing your front end…" : who + " is working on it. This usually takes a minute or two…", false);
          } else {
            onStatus(ev.text || "", false);
          }
          break;
        case "thinking":
          onThinking(ev.text || "");
          break;
        case "text":
          onText(ev.text || "");
          break;
        case "tool":
          if (friendly) {
            if (DONE_VERBS[ev.tool] && ev.path) onTool(DONE_VERBS[ev.tool] + " " + ev.path, "");
          } else {
            onTool((ev.tool || "") + (ev.path ? " " + ev.path : "") + (ev.text ? " (" + ev.text + ")" : ""), "");
          }
          break;
        case "warning":
          if (!friendly) onTool("⚠ " + (ev.text || ""), "b-warn");
          break;
        case "canceled":
          result.outcome = "canceled";
          result.message = ev.text || "Stopped. Nothing was changed.";
          onStatus(result.message, false);
          break;
        case "paused":
          result.outcome = "paused";
          result.limit = "later";
          result.message = ev.text || "";
          if (!friendly) onStatus(result.message, true);
          break;
        case "error":
          result.outcome = "error";
          result.message = failText(ev.text);
          onStatus(result.message, true);
          break;
        case "revision":
          result.outcome = "revision";
          result.revision = String(ev.revision || "");
          result.number = ev.number || 0;
          result.summary = ev.text || "";
          onStatus(friendly ? "Version " + ev.number + " is ready." : "Saved r" + ev.number + ".", false);
          break;
      }
    }

    var streaming = false; // the run started: losing the connection now doesn't stop it
    function dropped() {
      result.outcome = "dropped";
      result.message = friendly
        ? "The connection dropped, but " + who + " is still working on it. It will show up here when it's done."
        : "The connection ended before the run finished. It may still complete: reload in a minute.";
      onStatus(result.message, false);
    }
    onStatus("Starting…", false);
    return fetch(o.url, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(Object.assign({}, o.extra || {}, { prompt: o.prompt, parent: o.parent || "", images: o.images || [], attached: o.attached || [] }))
    }).then(function (r) {
      if (r.ok) streaming = true;
      var paused = !r.ok && r.headers.get("X-Build-Paused");
      if (paused) {
        return r.text().then(function (t) {
          result.outcome = "paused";
          result.limit = paused;
          result.message = (t || "").trim();
          if (!friendly) onStatus(result.message, true);
        });
      }
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
              var ev;
              try { ev = JSON.parse(line.slice(6)); } catch (err) { return; } // ignore a malformed frame
              handle(ev);
            });
          }
          return pump();
        });
      }
      return pump();
    }).then(function () {
      if (!result.outcome) dropped();
      return result;
    }).catch(function (err) {
      // a network failure mid-stream (the phone slept, the tab went to the
      // background, the server restarted): the run goes on without us
      if (streaming && !result.outcome && err instanceof TypeError) { dropped(); return result; }
      var msg = err && typeof err.message === "string" ? err.message : String(err);
      // a network failure's message is the browser's, not ours
      if (friendly && err instanceof TypeError) msg = "";
      result.outcome = "error";
      result.message = failText(msg);
      onStatus(result.message, true);
      return result;
    });
  }

  window.BuilderStream = { run: run };
})();
