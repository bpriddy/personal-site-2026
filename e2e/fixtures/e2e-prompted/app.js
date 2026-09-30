(async () => {
  try {
    await site.loaded;
    renderNav();
    render(site.route);
    site.onRoute((route) => render(route));
    document.body.setAttribute("data-e2e", "rendered");
    site.ready();
  } catch (err) {
    site.reportError(err);
  }
})();

function link(text, slug) {
  const a = document.createElement("a");
  a.href = "#";
  a.textContent = text;
  a.dataset.slug = slug;
  a.addEventListener("click", (e) => { e.preventDefault(); site.navigate(slug); });
  return a;
}

function renderNav() {
  const nav = document.getElementById("nav");
  for (const p of site.pages()) nav.append(link(site.field(p, "title", { expect: "text", fallback: "Untitled" }), p.slug));
  if (site.experiments().length) nav.append(link("Experiments", "experiments"));
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
      p.textContent = site.field(e, "title", { expect: "text" }) + ": " + site.field(e, "summary", { expect: "text" });
      main.append(p);
    }
    return;
  }
  const page = site.page(route);
  if (Object.keys(page).length === 0) { h1.textContent = "Not found"; return; }
  h1.textContent = site.field(page, "title", { expect: "text", fallback: "Ben Priddy" });
  for (const para of String(site.field(page, "body", { expect: "text" })).split(/\n\s*\n/)) {
    if (!para.trim()) continue;
    const p = document.createElement("p");
    p.textContent = para.trim();
    main.append(p);
  }
}
