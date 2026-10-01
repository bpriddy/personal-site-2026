package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// DemoModel is an offline stand-in for Claude, for local development and the
// end-to-end tests (cmd/server enables it only with APP_ENV=dev and
// BUILDER_DEMO_MODEL=1). Each run writes one small, valid front end whose
// colour depends on the prompt, then finishes. It never calls the network.
type DemoModel struct {
	Delay time.Duration // per turn, so the streaming UI is visible
	seq   atomic.Int64
}

func (m *DemoModel) Turn(ctx context.Context, params anthropic.BetaMessageNewParams, emit func(Event)) (*anthropic.BetaMessage, error) {
	if m.Delay > 0 {
		select {
		case <-time.After(m.Delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	n := m.seq.Add(1)
	last := params.Messages[len(params.Messages)-1]
	raw, _ := json.Marshal(last)
	var resp string
	if strings.Contains(string(raw), `"tool_result"`) {
		// the file is written: finish (again, if finish pointed out warnings)
		resp = ToolUse(fmt.Sprint(n), "finish", map[string]any{
			"summary": "Here's a first take (demo model): a calm page in a colour picked from your description, with the site's pages and experiments.",
		})
	} else {
		h := fnv.New32a()
		h.Write(raw)
		resp = ToolUse(fmt.Sprint(n), "write_file", map[string]any{
			"path": "index.html", "content": strings.ReplaceAll(demoIndex, "{{HUE}}", fmt.Sprint(h.Sum32()%360)),
		})
	}
	var msg anthropic.BetaMessage
	if err := json.Unmarshal([]byte(resp), &msg); err != nil {
		return nil, err
	}
	emit(Event{Type: "thinking", Text: "Sketching a layout… "})
	for _, b := range msg.Content {
		if b.Type == "text" {
			emit(Event{Type: "text", Text: b.Text + " "})
		}
	}
	return &msg, nil
}

const demoIndex = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<script src="/site-host.js"></script>
<title>Ben Priddy</title>
<style>
  :root { --hue: {{HUE}}; }
  body { margin: 0; min-height: 100vh; font: 18px/1.6 system-ui, sans-serif;
    background: hsl(var(--hue) 45% 14%); color: hsl(var(--hue) 30% 94%); }
  nav { display: flex; flex-wrap: wrap; gap: 1rem; padding: 1.25rem 1.5rem; }
  nav a { color: hsl(var(--hue) 80% 78%); }
  main { max-width: 40rem; padding: 1rem 1.5rem 4rem; }
  h1 { font-size: clamp(2rem, 7vw, 3.5rem); line-height: 1.1; }
</style>
</head>
<body data-demo="1">
<nav id="nav"></nav>
<main id="main"></main>
<script>
(async () => {
  try {
    await site.loaded;
    const nav = document.getElementById("nav");
    for (const p of site.pages()) nav.append(link(site.field(p, "title", {expect: "text", fallback: "Untitled"}), p.slug));
    if (site.experiments().length) nav.append(link("Experiments", "experiments"));
    render(site.route);
    site.onRoute((route) => { render(route); scrollTo(0, 0); });
    site.ready();
  } catch (err) {
    site.reportError(err);
  }
})();
function link(text, slug) {
  const a = document.createElement("a");
  a.href = "#"; a.textContent = text; a.dataset.slug = slug;
  a.addEventListener("click", (e) => { e.preventDefault(); site.navigate(slug); });
  return a;
}
function render(route) {
  const main = document.getElementById("main");
  main.replaceChildren();
  const h1 = document.createElement("h1");
  main.append(h1);
  if (route === "experiments") {
    h1.textContent = "Experiments";
    for (const e of site.experiments()) {
      const p = document.createElement("p");
      p.textContent = site.field(e, "title", {expect: "text"}) + ": " + site.field(e, "summary", {expect: "text"});
      main.append(p);
    }
    return;
  }
  const page = site.page(route);
  if (Object.keys(page).length === 0) { h1.textContent = "Not found"; main.append(link("Home", "")); return; }
  h1.textContent = site.field(page, "title", {expect: "text", fallback: "Ben Priddy"});
  for (const para of String(site.field(page, "body", {expect: "text"})).split(/\n\s*\n/)) {
    if (!para.trim()) continue;
    const p = document.createElement("p");
    p.textContent = para.trim();
    main.append(p);
  }
}
</script>
</body>
</html>
`
