//! `builtin/site`: the default site front end.
//!
//! It runs inside the site's sandboxed iframe (docs/frontend-protocol.md). The
//! host API script `/site-host.js` defines `window.site`: the CMS content and
//! current route arrive through `site.loaded`, route changes through
//! `site.onRoute`, and navigation goes back out through `site.navigate`. The
//! parent page owns the URL and the accessible HTML transcript.
//!
//! The design is "Instrument" (docs/design-pov.md): the content is real DOM
//! text in the house fonts, set in the same composition as the transcript (a
//! meta row, the name as the one big gesture, the bio, a numbered index of
//! experiments, Ben's work), so it reads, selects and scales like a page and works
//! without WebGPU. `site.ready()` is called as soon as that DOM is up. Then,
//! where WebGPU exists, a quiet dot field is drawn behind it: a fixed grid
//! of fine dots that swell into a halftone echo of the name and lean toward
//! the pointer. It depicts the subject (the name), stays monochrome, and goes
//! still under prefers-reduced-motion.
//!
//! Routes: "" (home: the name, the bio, selected work, experiments), "work"
//! (the index of projects), "work/<slug>" (a project), "experiments", and
//! any page slug. Media (stills and loops) comes from the content's /media/
//! paths on this origin; loops play muted while on screen, never under
//! prefers-reduced-motion. Links that leave the site (a project's link, its
//! YouTube film) go through `site.openExternal`, since the sandbox can't
//! open tabs.
//!
//! Content is read only through the host API's contract accessors
//! (`site.pages()`, `site.experiments()`, `site.projects()`, `site.field`), never by decoding the
//! payload's shape, so missing, extra or mistyped fields render as empty and
//! are reported to the observer instead of breaking the page.
//!
//! Without `window.site` (standalone, e.g. `trunk serve`) it uses placeholder
//! content and navigates locally.

use std::cell::{Cell, RefCell};
use std::rc::Rc;
use std::sync::Arc;

use serde::Deserialize;
use wasm_bindgen::prelude::*;
use wasm_bindgen::JsCast;
use wasm_bindgen_futures::JsFuture;
use web_sys::{Document, Element, HtmlCanvasElement, Window};

#[derive(Debug, Default, Clone, Deserialize)]
#[serde(default)]
pub struct SiteData {
    pub pages: Vec<Page>,
    pub experiments: Vec<Experiment>,
    pub projects: Vec<Project>,
}

#[derive(Debug, Default, Clone, Deserialize)]
#[serde(default)]
pub struct Page {
    pub slug: String,
    pub title: String,
    pub body: String,
}

#[derive(Debug, Default, Clone, Deserialize)]
#[serde(default)]
pub struct Experiment {
    pub slug: String,
    pub title: String,
    pub summary: String,
    pub link: String,
    pub media: Vec<MediaItem>,
}

/// A project (client work), shown at the route "work/<slug>".
#[derive(Debug, Default, Clone, Deserialize)]
#[serde(default)]
pub struct Project {
    pub slug: String,
    pub title: String,
    pub client: String,
    pub agency: String,
    pub year: String,
    pub tags: Vec<String>,
    pub roles: Vec<String>,
    pub summary: String,
    pub contribution: String,
    pub body: String,
    pub link: String,
    pub palette: Vec<String>,
    pub youtube: String,
    pub media: Vec<MediaItem>,
}

/// One still ("image") or silent loop ("loop"); src and poster are /media/
/// paths on this origin.
#[derive(Debug, Default, Clone, Deserialize)]
#[serde(default)]
pub struct MediaItem {
    pub kind: String,
    pub src: String,
    pub poster: String,
    pub width: u32,
    pub height: u32,
    pub alt: String,
}

/// What `site.loaded` resolves with.
#[derive(Debug, Default, Deserialize)]
#[serde(default)]
struct Loaded {
    content: SiteData,
    route: String,
}

// ── host API (window.site) ──────────────────────────────────────────────────

fn host() -> Option<JsValue> {
    let w = web_sys::window()?;
    let site = js_sys::Reflect::get(&w, &"site".into()).ok()?;
    site.is_object().then_some(site)
}

fn host_fn(name: &str) -> Option<(JsValue, js_sys::Function)> {
    let site = host()?;
    let f = js_sys::Reflect::get(&site, &name.into()).ok()?;
    let f = f.dyn_into::<js_sys::Function>().ok()?;
    Some((site, f))
}

fn host_call(name: &str, args: &[JsValue]) -> Option<JsValue> {
    let (site, f) = host_fn(name)?;
    let args: js_sys::Array = args.iter().collect();
    f.apply(&site, &args).ok()
}

/// console + `site.reportError(err, kind)`. Before `site.ready()` any report
/// makes the parent fall back; `gpu-lost` always does.
fn report(msg: &str, kind: &str) {
    web_sys::console::error_1(&msg.into());
    host_call("reportError", &[js_sys::Error::new(msg).into(), kind.into()]);
}

/// The route from an onRoute callback argument (a slug string, or an object
/// with `route`), falling back to `site.route`.
fn route_from(arg: &JsValue) -> Option<String> {
    arg.as_string()
        .or_else(|| {
            js_sys::Reflect::get(arg, &"route".into())
                .ok()
                .and_then(|v| v.as_string())
        })
        .or_else(|| {
            host().and_then(|s| js_sys::Reflect::get(&s, &"route".into()).ok()?.as_string())
        })
}

async fn load_content() -> Loaded {
    let Some(site) = host() else {
        web_sys::console::info_1(&"site: no window.site (standalone) — placeholder content".into());
        return placeholder();
    };
    let loaded = async {
        let p = js_sys::Reflect::get(&site, &"loaded".into())?.dyn_into::<js_sys::Promise>()?;
        let v = JsFuture::from(p).await?;
        let route = js_sys::Reflect::get(&v, &"route".into())
            .ok()
            .and_then(|r| r.as_string())
            .unwrap_or_default();
        if host_fn("field").is_some() && host_fn("pages").is_some() {
            return Ok(Loaded { content: read_content(), route });
        }
        // an older host without the contract accessors: whole-payload decode
        let json: String = js_sys::JSON::stringify(&v)?.into();
        serde_json::from_str::<Loaded>(&json).map_err(|e| JsValue::from_str(&e.to_string()))
    };
    match loaded.await {
        Ok(l) => l,
        Err(e) => {
            web_sys::console::error_2(&"site.loaded failed; using placeholder content:".into(), &e);
            placeholder()
        }
    }
}

/// The content, read only through the host API's contract accessors
/// (`site.pages()`, `site.experiments()`, `site.field`), so missing, extra
/// or mistyped fields degrade to "" and get reported instead of failing.
/// Every field the design shows is read up front, for every item, so gaps
/// anywhere on the site are reported from any route.
fn read_content() -> SiteData {
    let items = |name: &str| -> Vec<JsValue> {
        host_call(name, &[])
            .filter(js_sys::Array::is_array)
            .map(|a| js_sys::Array::from(&a).iter().filter(JsValue::is_object).collect())
            .unwrap_or_default()
    };
    SiteData {
        pages: items("pages")
            .iter()
            .map(|p| Page {
                slug: slug_of(p),
                title: text_field(p, "title"),
                body: text_field(p, "body"),
            })
            .collect(),
        experiments: items("experiments")
            .iter()
            .map(|e| Experiment {
                slug: slug_of(e),
                title: text_field(e, "title"),
                summary: text_field(e, "summary"),
                link: optional_text(e, "link"),
                media: media_field(e, "media"),
            })
            .collect(),
        projects: items("projects")
            .iter()
            .map(|p| Project {
                slug: slug_of(p),
                title: text_field(p, "title"),
                client: optional_text(p, "client"),
                agency: optional_text(p, "agency"),
                year: optional_text(p, "year"),
                tags: list_field(p, "tags"),
                roles: list_field(p, "roles"),
                summary: text_field(p, "summary"),
                contribution: optional_text(p, "contribution"),
                body: optional_text(p, "body"),
                link: optional_text(p, "link"),
                palette: list_field(p, "palette"),
                youtube: optional_text(p, "youtube"),
                media: media_field(p, "media"),
            })
            .collect(),
    }
}

/// `site.field(item, name, {expect, fallback, optional})`.
fn field(item: &JsValue, name: &str, expect: &str, fallback: JsValue, optional: bool) -> Option<JsValue> {
    let opts = js_sys::Object::new();
    let _ = js_sys::Reflect::set(&opts, &"expect".into(), &expect.into());
    let _ = js_sys::Reflect::set(&opts, &"fallback".into(), &fallback);
    if optional {
        let _ = js_sys::Reflect::set(&opts, &"optional".into(), &JsValue::TRUE);
    }
    host_call("field", &[item.clone(), name.into(), opts.into()])
}

/// A text field that may legitimately be empty (no link, no agency): read
/// through `site.field` with `optional`, so an empty value isn't a gap.
fn optional_text(item: &JsValue, name: &str) -> String {
    field(item, name, "text", "".into(), true)
        .and_then(|v| v.as_string())
        .unwrap_or_default()
}

/// A list-of-text field (`expect: "list"`): its strings.
fn list_field(item: &JsValue, name: &str) -> Vec<String> {
    field(item, name, "list", js_sys::Array::new().into(), true)
        .filter(js_sys::Array::is_array)
        .map(|a| js_sys::Array::from(&a).iter().filter_map(|s| s.as_string()).filter(|s| !s.trim().is_empty()).collect())
        .unwrap_or_default()
}

/// A media list (`expect: "list"`), already normalized by the host API to
/// same-origin /media/ items.
fn media_field(item: &JsValue, name: &str) -> Vec<MediaItem> {
    let Some(list) = field(item, name, "list", js_sys::Array::new().into(), true).filter(js_sys::Array::is_array) else {
        return vec![];
    };
    let get = |o: &JsValue, k: &str| js_sys::Reflect::get(o, &k.into()).ok();
    let text = |o: &JsValue, k: &str| get(o, k).and_then(|v| v.as_string()).unwrap_or_default();
    let num = |o: &JsValue, k: &str| get(o, k).and_then(|v| v.as_f64()).filter(|n| n.is_finite() && *n > 0.0).unwrap_or(0.0) as u32;
    js_sys::Array::from(&list)
        .iter()
        .filter(JsValue::is_object)
        .map(|m| MediaItem {
            kind: text(&m, "kind"),
            src: text(&m, "src"),
            poster: text(&m, "poster"),
            width: num(&m, "width"),
            height: num(&m, "height"),
            alt: text(&m, "alt"),
        })
        .filter(|m| m.src.starts_with("/media/") && (m.kind == "image" || m.kind == "loop"))
        .collect()
}

/// An item's slug. Not read through `site.field`: "" is the home page's
/// slug, not a gap.
fn slug_of(item: &JsValue) -> String {
    js_sys::Reflect::get(item, &"slug".into())
        .ok()
        .and_then(|s| s.as_string())
        .unwrap_or_default()
}

/// `site.field(item, name, {expect: "text", fallback: ""})`: the field's text,
/// or "" (which the host reports as a content gap).
fn text_field(item: &JsValue, name: &str) -> String {
    let opts = js_sys::Object::new();
    let _ = js_sys::Reflect::set(&opts, &"expect".into(), &"text".into());
    let _ = js_sys::Reflect::set(&opts, &"fallback".into(), &"".into());
    host_call("field", &[item.clone(), name.into(), opts.into()])
        .and_then(|v| v.as_string())
        .unwrap_or_default()
}

fn placeholder() -> Loaded {
    let page = |slug: &str, title: &str, body: &str| Page {
        slug: slug.into(),
        title: title.into(),
        body: body.into(),
    };
    Loaded {
        content: SiteData {
            pages: vec![
                page("", "Ben Priddy", "Standalone.\n\nPlaceholder content: this front end is running without the site host. Inside the site, the CMS content arrives through window.site."),
                page("about", "About", "A second placeholder page, to exercise navigation."),
            ],
            experiments: vec![Experiment {
                slug: "particle-stream".into(),
                title: "Particle Stream".into(),
                summary: "Words as rocks in a stream.".into(),
                ..Default::default()
            }],
            projects: vec![Project {
                slug: "placeholder".into(),
                title: "A placeholder project".into(),
                client: "Client".into(),
                year: "2026".into(),
                summary: "Projects arrive through window.site inside the site.".into(),
                ..Default::default()
            }],
        },
        route: String::new(),
    }
}

// ── the page: DOM ────────────────────────────────────────────────────────────

const ROLE: &str = "Creative technology / AI";

/// `tag.class` with optional text.
fn el(doc: &Document, tag: &str, class: &str, text: Option<&str>) -> Result<Element, JsValue> {
    let e = doc.create_element(tag)?;
    if !class.is_empty() {
        e.set_class_name(class);
    }
    if let Some(t) = text {
        e.set_text_content(Some(t));
    }
    Ok(e)
}

/// Appends `child` to `parent`.
fn add(parent: &Element, child: &Element) -> Result<(), JsValue> {
    parent.append_child(child).map(|_| ())
}

/// Splits a plain-text body on blank lines, like the transcript does.
fn paragraphs(body: &str) -> Vec<String> {
    body.replace("\r\n", "\n")
        .split("\n\n")
        .map(str::trim)
        .filter(|p| !p.is_empty())
        .map(String::from)
        .collect()
}

/// The type-scale role of each paragraph (docs/design-pov.md, 5.1 "Bio"): a
/// short first line is an italic aside, the first real paragraph is the
/// lede, the rest is body. The transcript uses the same rule.
fn prose(body: &str) -> Vec<(String, &'static str)> {
    let mut lede = false;
    paragraphs(body)
        .into_iter()
        .enumerate()
        .map(|(i, p)| {
            let kind = if i == 0 && p.chars().count() <= 40 {
                "aside"
            } else if !lede {
                lede = true;
                "lede"
            } else {
                "body"
            };
            (p, kind)
        })
        .collect()
}

/// "Ben Priddy" → ["Ben", "Priddy"]: first word, then the rest.
fn name_lines(title: &str) -> Vec<String> {
    let words: Vec<&str> = title.split_whitespace().collect();
    match words.len() {
        0 => vec![],
        1 => vec![words[0].to_string()],
        _ => vec![words[0].to_string(), words[1..].join(" ")],
    }
}

fn two(n: usize) -> String {
    format!("{n:02}")
}

/// A link to a route: navigation goes through the parent (`site.navigate`).
fn route_link(doc: &Document, slug: &str, class: &str) -> Result<Element, JsValue> {
    let a = el(doc, "a", class, None)?;
    a.set_attribute("href", &format!("#/{slug}"))?;
    a.set_attribute("data-slug", slug)?;
    Ok(a)
}

/// The navigable pages: each published page (home first, called "Index"),
/// then "Experiments" when there are any.
fn nav_items(data: &SiteData) -> Vec<(String, String)> {
    let mut out: Vec<(String, String)> = Vec::new();
    let mut pages: Vec<&Page> = data.pages.iter().collect();
    pages.sort_by(|a, b| a.slug.cmp(&b.slug));
    let mut seen = std::collections::HashSet::new();
    for p in pages {
        if !seen.insert(p.slug.clone()) {
            continue;
        }
        let label = if p.slug.is_empty() {
            "Index".to_string()
        } else if p.title.trim().is_empty() {
            p.slug.clone()
        } else {
            p.title.trim().to_string()
        };
        out.push((p.slug.clone(), label));
    }
    if !data.projects.is_empty() {
        out.push(("work".into(), "Work".into()));
    }
    if !data.experiments.is_empty() {
        out.push(("experiments".into(), "Experiments".into()));
    }
    out
}

fn home_title(data: &SiteData) -> String {
    data.pages
        .iter()
        .find(|p| p.slug.is_empty())
        .map(|p| p.title.trim().to_string())
        .filter(|t| !t.is_empty())
        .unwrap_or_else(|| "Ben Priddy".into())
}

/// The meta row: name (home), role, numbered pages nav.
fn build_header(doc: &Document, data: &SiteData) -> Result<Element, JsValue> {
    let header = el(doc, "header", "site-header", None)?;
    let brand = route_link(doc, "", "brand")?;
    brand.set_text_content(Some(&home_title(data)));
    let role = el(doc, "p", "role", Some(ROLE))?;
    let nav = el(doc, "nav", "", None)?;
    nav.set_attribute("aria-label", "Pages")?;
    for (i, (slug, label)) in nav_items(data).iter().enumerate() {
        let a = route_link(doc, slug, "")?;
        add(&a, &el(doc, "span", "idx", Some(&two(i + 1)))?)?;
        a.append_child(&doc.create_text_node(&format!(" {label}")))?;
        nav.append_child(&a)?;
    }
    header.append_child(&brand)?;
    header.append_child(&role)?;
    header.append_child(&nav)?;
    Ok(header)
}

fn build_footer(doc: &Document, data: &SiteData) -> Result<Element, JsValue> {
    let footer = el(doc, "footer", "site-footer", None)?;
    let year = js_sys::Date::new_0().get_full_year();
    add(&footer, &el(doc, "p", "", Some(&format!("\u{a9} {year} {}", home_title(data))))?)?;
    Ok(footer)
}

fn append_prose(doc: &Document, parent: &Element, body: &str) -> Result<(), JsValue> {
    for (text, kind) in prose(body) {
        add(&parent, &el(doc, "p", &format!("prose-{kind}"), Some(&text))?)?;
    }
    Ok(())
}

fn append_index(doc: &Document, parent: &Element, data: &SiteData) -> Result<(), JsValue> {
    let list = el(doc, "ol", "index-list", None)?;
    for (i, e) in data.experiments.iter().enumerate() {
        let row = el(doc, "li", "index-row", None)?;
        row.set_attribute("style", &format!("--i:{i}"))?;
        let idx = el(doc, "span", "idx", Some(&two(i + 1)))?;
        idx.set_attribute("aria-hidden", "true")?;
        row.append_child(&idx)?;
        let title = if e.title.trim().is_empty() { e.slug.as_str() } else { e.title.trim() };
        let h = el(doc, "h3", "index-title", None)?;
        if e.link.is_empty() {
            h.set_text_content(Some(title));
        } else {
            let a = ext_link(doc, &e.link, "")?;
            a.set_text_content(Some(title));
            h.append_child(&a)?;
        }
        row.append_child(&h)?;
        if !e.summary.trim().is_empty() {
            add(&row, &el(doc, "p", "index-summary", Some(e.summary.trim()))?)?;
        }
        if let Some(m) = e.media.iter().find(|m| m.kind == "loop") {
            row.class_list().add_1("has-media")?;
            let thumb = el(doc, "span", "index-thumb", None)?;
            thumb.set_attribute("aria-hidden", "true")?;
            add(&thumb, &media_el(doc, m, "", false)?)?;
            row.append_child(&thumb)?;
        }
        list.append_child(&row)?;
    }
    parent.append_child(&list)?;
    Ok(())
}

/// How many projects the home page features.
const SELECTED_WORK: usize = 6;

/// A link out of the site: the parent opens it (`site.openExternal`).
fn ext_link(doc: &Document, url: &str, class: &str) -> Result<Element, JsValue> {
    let a = el(doc, "a", class, None)?;
    a.set_attribute("href", url)?;
    a.set_attribute("data-external", url)?;
    a.set_attribute("rel", "noopener noreferrer")?;
    Ok(a)
}

/// A still (`<img>`) or a loop (`<video>`, muted, looping, inline, with its
/// poster; played by the app's observer while on screen). width/height are
/// set so nothing shifts as media loads; images below the fold load lazily.
fn media_el(doc: &Document, m: &MediaItem, alt: &str, eager: bool) -> Result<Element, JsValue> {
    let e: Element = if m.kind == "loop" {
        let v = doc.create_element("video")?.dyn_into::<web_sys::HtmlVideoElement>()?;
        v.set_muted(true); // browsers only start muted video by themselves
        v.set_loop(true);
        for a in ["muted", "loop", "playsinline", "data-loop"] {
            v.set_attribute(a, "")?;
        }
        v.set_attribute("preload", "none")?;
        if !m.poster.is_empty() {
            v.set_poster(&m.poster);
        }
        v.set_src(&m.src);
        if alt.is_empty() {
            v.set_attribute("aria-hidden", "true")?;
        } else {
            v.set_attribute("aria-label", alt)?;
        }
        v.into()
    } else {
        let i = doc.create_element("img")?;
        i.set_attribute("src", &m.src)?;
        i.set_attribute("alt", if m.alt.is_empty() { alt } else { &m.alt })?;
        i.set_attribute("decoding", "async")?;
        if !eager {
            i.set_attribute("loading", "lazy")?;
        }
        i
    };
    if m.width > 0 && m.height > 0 {
        e.set_attribute("width", &m.width.to_string())?;
        e.set_attribute("height", &m.height.to_string())?;
    }
    Ok(e)
}

/// "strip" (3:1 and wider), "wide" or "box": how a loop sits in the column.
fn shape(m: &MediaItem) -> &'static str {
    match (m.width, m.height) {
        (w, h) if w == 0 || h == 0 => "wide",
        (w, h) if w >= 3 * h => "strip",
        (w, h) if 2 * w >= 3 * h => "wide",
        _ => "box",
    }
}

fn project_title(p: &Project) -> &str {
    if p.title.trim().is_empty() { p.slug.as_str() } else { p.title.trim() }
}

/// "Samsung · 2020"
fn project_meta(p: &Project) -> String {
    [p.client.trim(), p.year.trim()].iter().filter(|s| !s.is_empty()).copied().collect::<Vec<_>>().join(" \u{b7} ")
}

/// The work index: numbered hairline rows, each a link to "work/<slug>",
/// with the first loop (or the still) as a strip. At most `limit` rows.
fn append_work(doc: &Document, parent: &Element, projects: &[Project], limit: usize) -> Result<(), JsValue> {
    let list = el(doc, "ol", "index-list work-list", None)?;
    for (i, p) in projects.iter().take(limit).enumerate() {
        let row = el(doc, "li", "work-row", None)?;
        row.set_attribute("style", &format!("--i:{i}"))?;
        let a = route_link(doc, &format!("work/{}", p.slug), "work-link")?;
        let idx = el(doc, "span", "idx", Some(&two(i + 1)))?;
        idx.set_attribute("aria-hidden", "true")?;
        a.append_child(&idx)?;
        add(&a, &el(doc, "h3", "work-title", Some(project_title(p)))?)?;
        let meta = project_meta(p);
        if !meta.is_empty() {
            add(&a, &el(doc, "span", "work-meta", Some(&meta))?)?;
        }
        let thumb = p.media.iter().find(|m| m.kind == "loop").or_else(|| p.media.iter().find(|m| m.kind == "image"));
        if let Some(m) = thumb {
            let t = el(doc, "span", "work-thumb", None)?;
            t.set_attribute("aria-hidden", "true")?;
            add(&t, &media_el(doc, m, "", false)?)?;
            a.append_child(&t)?;
        }
        row.append_child(&a)?;
        list.append_child(&row)?;
    }
    parent.append_child(&list)?;
    Ok(())
}

/// One labelled text section ("( Brief )", ...) with paragraphs.
fn text_section(doc: &Document, parent: &Element, label: &str, text: &str, quiet: bool) -> Result<(), JsValue> {
    let paras = paragraphs(text);
    if paras.is_empty() {
        return Ok(());
    }
    let sec = el(doc, "section", "project-text", None)?;
    add(&sec, &el(doc, "h2", "label", Some(label))?)?;
    let prose = el(doc, "div", if quiet { "project-prose project-prose-2" } else { "project-prose" }, None)?;
    for p in paras {
        add(&prose, &el(doc, "p", "", Some(&p))?)?;
    }
    sec.append_child(&prose)?;
    parent.append_child(&sec)?;
    Ok(())
}

/// The project page (route "work/<slug>"): the same composition as the
/// transcript's /work/<slug>.
fn render_project(doc: &Document, main: &Element, data: &SiteData, i: usize) -> Result<(), JsValue> {
    let p = &data.projects[i];
    let title = project_title(p);
    let art = el(doc, "article", "project", None)?;
    let head = el(doc, "section", "page-head project-head", None)?;
    add(&head, &el(doc, "h1", "display", Some(title))?)?;
    add(&head, &el(doc, "p", "label", Some(&format!("( {} / {} )", two(i + 1), two(data.projects.len()))))?)?;
    art.append_child(&head)?;

    let dl = el(doc, "dl", "project-meta", None)?;
    let tags = p.tags.join(", ");
    let roles = p.roles.join(", ");
    for (class, label, value) in [
        ("pm-client", "Client", p.client.trim()),
        ("pm-agency", "Agency", p.agency.trim()),
        ("pm-year", "Year", p.year.trim()),
        ("pm-roles", "Role", roles.as_str()),
        ("pm-tags", "Tags", tags.as_str()),
    ] {
        if value.is_empty() {
            continue;
        }
        let d = el(doc, "div", class, None)?;
        add(&d, &el(doc, "dt", "", Some(label))?)?;
        add(&d, &el(doc, "dd", "", Some(value))?)?;
        dl.append_child(&d)?;
    }
    let colours: Vec<&String> = p.palette.iter().filter(|c| c.len() == 7 && c.starts_with('#') && c[1..].chars().all(|h| h.is_ascii_hexdigit())).collect();
    if !colours.is_empty() {
        let d = el(doc, "div", "pm-palette", None)?;
        add(&d, &el(doc, "dt", "", Some("Palette"))?)?;
        let dd = el(doc, "dd", "swatches", None)?;
        dd.set_attribute("aria-label", &p.palette.join(", "))?;
        for c in colours {
            let sw = el(doc, "span", "sw", None)?;
            sw.set_attribute("style", &format!("background:{c}"))?;
            dd.append_child(&sw)?;
        }
        d.append_child(&dd)?;
        dl.append_child(&d)?;
    }
    art.append_child(&dl)?;

    if let Some(hero) = p.media.iter().find(|m| m.kind == "image") {
        let fig = el(doc, "figure", "project-hero", None)?;
        add(&fig, &media_el(doc, hero, title, true)?)?;
        art.append_child(&fig)?;
    }
    text_section(doc, &art, "( Brief )", &p.summary, false)?;
    text_section(doc, &art, "( Role )", &p.contribution, true)?;
    text_section(doc, &art, "( Notes )", &p.body, true)?;

    let film = !p.youtube.is_empty();
    if !p.link.is_empty() || film {
        let links = el(doc, "p", "project-links", None)?;
        if !p.link.is_empty() {
            let a = ext_link(doc, &p.link, "")?;
            a.set_text_content(Some("( Visit the work )"));
            links.append_child(&a)?;
        }
        if film {
            let a = ext_link(doc, &format!("https://www.youtube.com/watch?v={}", p.youtube), "")?;
            a.set_text_content(Some("( Watch the film )"));
            links.append_child(&a)?;
        }
        art.append_child(&links)?;
    }

    let rest: Vec<&MediaItem> = p.media.iter().filter(|m| m.kind == "loop")
        .chain(p.media.iter().filter(|m| m.kind == "image").skip(1))
        .collect();
    if !rest.is_empty() {
        let n = p.media.iter().filter(|m| m.kind == "loop").count();
        let sec = el(doc, "section", "project-media", None)?;
        let label = if n > 0 { format!("( Loops \u{2014} {} )", two(n)) } else { "( Images )".into() };
        add(&sec, &el(doc, "h2", "label", Some(&label))?)?;
        let wrap = el(doc, "div", "project-loops", None)?;
        for (k, m) in rest.iter().enumerate() {
            let fig = el(doc, "figure", &format!("loop loop-{}", shape(m)), None)?;
            add(&fig, &media_el(doc, m, &format!("{title}, {} {}", if m.kind == "loop" { "loop" } else { "image" }, k + 1), false)?)?;
            wrap.append_child(&fig)?;
        }
        sec.append_child(&wrap)?;
        art.append_child(&sec)?;
    }
    main.append_child(&art)?;

    if data.projects.len() > 1 {
        let next = &data.projects[(i + 1) % data.projects.len()];
        let nav = el(doc, "nav", "project-next", None)?;
        nav.set_attribute("aria-label", "More work")?;
        add(&nav, &el(doc, "p", "label", Some("( Next )"))?)?;
        let a = route_link(doc, &format!("work/{}", next.slug), "next-title")?;
        a.set_text_content(Some(project_title(next)));
        nav.append_child(&a)?;
        let all = route_link(doc, "work", "label all-work")?;
        all.set_text_content(Some("( All work )"));
        nav.append_child(&all)?;
        main.append_child(&nav)?;
    }
    Ok(())
}

/// The current route's content into `main`.
fn render_route(doc: &Document, main: &Element, data: &SiteData, route: &str) -> Result<(), JsValue> {
    main.set_inner_html("");
    if route.is_empty() {
        let home = data.pages.iter().find(|p| p.slug.is_empty());
        let hero = el(doc, "section", "hero", None)?;
        // Ben's portrait in characters, drawn and animated by portrait.js
        let portrait = el(doc, "canvas", "portrait", None)?;
        portrait.set_attribute("aria-hidden", "true")?;
        hero.append_child(&portrait)?;
        let name = el(doc, "h1", "name", None)?;
        for line in name_lines(&home_title(data)) {
            let l = el(doc, "span", "line", None)?;
            add(&l, &el(doc, "span", "", Some(&line))?)?;
            name.append_child(&l)?;
            name.append_child(&doc.create_text_node(" "))?;
        }
        hero.append_child(&name)?;
        let cue = el(doc, "p", "cue", Some("( Scroll )"))?;
        cue.set_attribute("aria-hidden", "true")?;
        hero.append_child(&cue)?;
        add(&hero, &el(doc, "p", "meta", Some(ROLE))?)?;
        main.append_child(&hero)?;
        if let Some(h) = home.filter(|h| !paragraphs(&h.body).is_empty()) {
            let bio = el(doc, "section", "bio", None)?;
            add(&bio, &el(doc, "h2", "label", Some("( About )"))?)?;
            let text = el(doc, "div", "bio-text", None)?;
            append_prose(doc, &text, &h.body)?;
            bio.append_child(&text)?;
            main.append_child(&bio)?;
        }
        if !data.projects.is_empty() {
            let idx = el(doc, "section", "index work-index", None)?;
            let label = format!("( Selected work \u{2014} {} )", two(data.projects.len()));
            add(&idx, &el(doc, "h2", "label", Some(&label))?)?;
            append_work(doc, &idx, &data.projects, SELECTED_WORK)?;
            let more = el(doc, "p", "index-more", None)?;
            let all = route_link(doc, "work", "label")?;
            all.set_text_content(Some("( All work )"));
            more.append_child(&all)?;
            idx.append_child(&more)?;
            main.append_child(&idx)?;
        }
        if !data.experiments.is_empty() {
            let idx = el(doc, "section", "index", None)?;
            let label = format!("( Experiments \u{2014} {} )", two(data.experiments.len()));
            add(&idx, &el(doc, "h2", "label", Some(&label))?)?;
            append_index(doc, &idx, data)?;
            main.append_child(&idx)?;
        }
        return Ok(());
    }
    if let Some(slug) = route.strip_prefix("work/") {
        if let Some(i) = data.projects.iter().position(|p| p.slug == slug) {
            return render_project(doc, main, data, i);
        }
    }
    let head = el(doc, "section", "page-head", None)?;
    main.append_child(&head)?;
    if route == "work" {
        add(&head, &el(doc, "h1", "display", Some("Work"))?)?;
        let n = format!("( {} projects )", two(data.projects.len()));
        add(&head, &el(doc, "p", "label", Some(&n))?)?;
        let idx = el(doc, "section", "index index-page work-index", None)?;
        if data.projects.is_empty() {
            add(&idx, &el(doc, "p", "prose-aside", Some("Nothing published yet."))?)?;
        } else {
            append_work(doc, &idx, &data.projects, usize::MAX)?;
        }
        main.append_child(&idx)?;
    } else if route == "experiments" {
        add(&head, &el(doc, "h1", "display", Some("Experiments"))?)?;
        let n = format!("( {} published )", two(data.experiments.len()));
        add(&head, &el(doc, "p", "label", Some(&n))?)?;
        let idx = el(doc, "section", "index index-page", None)?;
        if data.experiments.is_empty() {
            add(&idx, &el(doc, "p", "prose-aside", Some("Nothing published yet."))?)?;
        } else {
            append_index(doc, &idx, data)?;
        }
        main.append_child(&idx)?;
    } else if let Some(p) = data.pages.iter().find(|p| p.slug == route) {
        let title = if p.title.trim().is_empty() { p.slug.as_str() } else { p.title.trim() };
        add(&head, &el(doc, "h1", "display", Some(title))?)?;
        let bio = el(doc, "section", "bio bio-page", None)?;
        let text = el(doc, "div", "bio-text", None)?;
        append_prose(doc, &text, &p.body)?;
        bio.append_child(&text)?;
        main.append_child(&bio)?;
    } else {
        add(&head, &el(doc, "h1", "display", Some("Not found."))?)?;
        let p = el(doc, "p", "label", Some("There's no page here. "))?;
        let home = route_link(doc, "", "")?;
        home.set_text_content(Some("( Home )"));
        p.append_child(&home)?;
        head.append_child(&p)?;
    }
    Ok(())
}

/// Marks the nav entry for `route` as the current page.
fn mark_current(doc: &Document, route: &str) {
    let Ok(links) = doc.query_selector_all(".site-header nav a") else { return };
    for i in 0..links.length() {
        let Some(a) = links.get(i).and_then(|n| n.dyn_into::<Element>().ok()) else { continue };
        let slug = a.get_attribute("data-slug").unwrap_or_default();
        let on = slug == route || (!slug.is_empty() && route.starts_with(&format!("{slug}/")));
        let _ = if on { a.set_attribute("aria-current", "page") } else { a.remove_attribute("aria-current") };
    }
}

// ── app ──────────────────────────────────────────────────────────────────────

struct App {
    doc: Document,
    main: Element,
    data: SiteData,
    route: RefCell<String>,
    /// bumped on every layout change, so the field redraws its mask
    layout: Cell<u32>,
    /// plays loops while on screen (none without IntersectionObserver)
    loops: Option<web_sys::IntersectionObserver>,
}

impl App {
    fn show(&self, route: &str, scroll_top: bool) -> Result<(), JsValue> {
        *self.route.borrow_mut() = route.to_string();
        if let Some(io) = &self.loops {
            io.disconnect();
        }
        render_route(&self.doc, &self.main, &self.data, route)?;
        if let Some(io) = &self.loops {
            let videos = self.main.query_selector_all("video[data-loop]")?;
            for i in 0..videos.length() {
                if let Some(v) = videos.get(i).and_then(|n| n.dyn_into::<Element>().ok()) {
                    io.observe(&v);
                }
            }
        }
        mark_current(&self.doc, route);
        // replay the route's arrival
        let list = self.main.class_list();
        list.remove_1("route-in")?;
        let _ = self.main.get_bounding_client_rect();
        list.add_1("route-in")?;
        if scroll_top {
            if let Some(w) = web_sys::window() {
                w.scroll_to_with_x_and_y(0.0, 0.0);
            }
        }
        self.layout.set(self.layout.get().wrapping_add(1));
        Ok(())
    }
}

/// An IntersectionObserver that plays each loop while it is on screen and
/// pauses it otherwise; loops never play under prefers-reduced-motion (the
/// poster shows).
fn loop_observer() -> Option<web_sys::IntersectionObserver> {
    let cb = Closure::<dyn FnMut(js_sys::Array)>::new(|entries: js_sys::Array| {
        let still = web_sys::window()
            .and_then(|w| w.match_media("(prefers-reduced-motion: reduce)").ok().flatten())
            .map(|m| m.matches())
            .unwrap_or(false);
        for e in entries.iter() {
            let Ok(e) = e.dyn_into::<web_sys::IntersectionObserverEntry>() else { continue };
            let Ok(v) = e.target().dyn_into::<web_sys::HtmlVideoElement>() else { continue };
            if e.is_intersecting() && !still {
                v.set_muted(true);
                let _ = v.set_attribute("preload", "auto");
                if let Ok(p) = v.play() {
                    // a refused play just leaves the poster
                    let ignore = Closure::<dyn FnMut(JsValue)>::new(|_| {});
                    let _ = p.catch(&ignore);
                    ignore.forget();
                }
            } else if !v.paused() {
                let _ = v.pause();
            }
        }
    });
    let opts = web_sys::IntersectionObserverInit::new();
    opts.set_root_margin("120px 0px");
    let io = web_sys::IntersectionObserver::new_with_options(cb.as_ref().unchecked_ref(), &opts).ok();
    cb.forget();
    io
}

#[wasm_bindgen(start)]
pub fn start() {
    console_error_panic_hook::set_once();
    wasm_bindgen_futures::spawn_local(async {
        match run().await {
            Ok(app) => {
                // the content is up; the field is an enhancement
                if let Err(e) = field::start(app).await {
                    // no WebGPU is a normal case, not an error
                    web_sys::console::info_1(&format!("site: no WebGPU field ({e})").into());
                }
            }
            Err(e) => {
                let msg = e
                    .dyn_ref::<js_sys::Error>()
                    .map(|e| String::from(e.message()))
                    .or_else(|| e.as_string())
                    .unwrap_or_else(|| format!("{e:?}"));
                report(&format!("site: {msg}"), "error");
            }
        }
    });
}

/// Renders the content as DOM and signals ready. Returns the app for the field.
async fn run() -> Result<Rc<App>, JsValue> {
    let window = web_sys::window().ok_or("no window")?;
    let doc = window.document().ok_or("no document")?;
    let root = doc.get_element_by_id("app").ok_or("no #app")?;

    // content: waits for the parent's site:init (via site.loaded)
    let loaded = load_content().await;
    root.set_inner_html("");
    let header = build_header(&doc, &loaded.content)?;
    let main = el(&doc, "main", "", None)?;
    let footer = build_footer(&doc, &loaded.content)?;
    root.append_child(&header)?;
    root.append_child(&main)?;
    root.append_child(&footer)?;
    let app = Rc::new(App {
        doc: doc.clone(),
        main,
        data: loaded.content,
        route: RefCell::new(String::new()),
        layout: Cell::new(0),
        loops: loop_observer(),
    });
    app.show(&loaded.route, false)?;

    // route changes from the parent (back/forward, or after our navigate)
    {
        let a = app.clone();
        let cb = Closure::<dyn FnMut(JsValue)>::new(move |arg: JsValue| {
            if let Some(r) = route_from(&arg) {
                if *a.route.borrow() != r {
                    if let Err(e) = a.show(&r, true) {
                        report(&format!("site: rendering /{r} failed: {e:?}"), "error");
                    }
                }
            }
        });
        if host_call("onRoute", &[cb.as_ref().clone()]).is_some() {
            // the route may have moved between site.loaded and subscribing
            if let Some(r) = host().and_then(|s| js_sys::Reflect::get(&s, &"route".into()).ok()?.as_string()) {
                if *app.route.borrow() != r {
                    app.show(&r, false)?;
                }
            }
        }
        cb.forget();
    }

    // links: navigation goes through the parent, which owns history
    {
        let a = app.clone();
        let cb = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |e: web_sys::MouseEvent| {
            let Some(t) = e.target().and_then(|t| t.dyn_into::<Element>().ok()) else { return };
            if let Ok(Some(ext)) = t.closest("a[data-external]") {
                // the sandbox can't open tabs: the parent does (http(s) only)
                e.prevent_default();
                let url = ext.get_attribute("data-external").unwrap_or_default();
                if host_call("openExternal", &[url.as_str().into()]).is_none() {
                    if let Some(w) = web_sys::window() {
                        let _ = w.open_with_url_and_target_and_features(&url, "_blank", "noopener,noreferrer");
                    }
                }
                return;
            }
            let Ok(Some(link)) = t.closest("a[data-slug]") else { return };
            e.prevent_default();
            let slug = link.get_attribute("data-slug").unwrap_or_default();
            if host_call("navigate", &[slug.as_str().into()]).is_none() {
                // standalone: no parent to own history, so switch locally
                let _ = a.show(&slug, true);
            }
        });
        doc.add_event_listener_with_callback("click", cb.as_ref().unchecked_ref())?;
        cb.forget();
    }

    // relayout when the window resizes or the house fonts land
    {
        let a = app.clone();
        let cb = Closure::<dyn FnMut()>::new(move || a.layout.set(a.layout.get().wrapping_add(1)));
        window.add_event_listener_with_callback("resize", cb.as_ref().unchecked_ref())?;
        cb.forget();
        let a = app.clone();
        let fonts_cb = Closure::<dyn FnMut(JsValue)>::new(move |_| a.layout.set(a.layout.get().wrapping_add(1)));
        if let Ok(fonts) = js_sys::Reflect::get(&doc, &"fonts".into()) {
            if let Ok(p) = js_sys::Reflect::get(&fonts, &"ready".into()).and_then(|p| p.dyn_into::<js_sys::Promise>()) {
                let _ = p.then(&fonts_cb);
            }
        }
        fonts_cb.forget();
    }

    host_call("ready", &[]);
    Ok(app)
}

// ── the field: WebGPU, behind the content ────────────────────────────────────

mod field {
    use super::*;
    use std::sync::atomic::{AtomicBool, Ordering};

    static LOST: AtomicBool = AtomicBool::new(false);

    // A fixed grid of fine dots in the ink colour. A mask of the name (R:
    // the letterforms, G: a wide blur of them) swells the dots into a
    // halftone echo of the name; they also lean toward the pointer. On load
    // the dots appear outward from the letters (intro 0 → 1).
    const SHADER: &str = r#"
struct U {
    view: vec2<f32>,      // viewport, CSS px
    pointer: vec2<f32>,   // CSS px; far away when there is none
    rect: vec4<f32>,      // the mask's rectangle in document CSS px (x, y, w, h)
    ink: vec4<f32>,       // rgb, then dpr
    time: f32,
    scroll: f32,
    intro: f32,
    opaque: f32,          // 1 when the canvas can't be transparent
    ground: vec4<f32>,
};
@group(0) @binding(0) var<uniform> u: U;
@group(0) @binding(1) var mask: texture_2d<f32>;
@group(0) @binding(2) var samp: sampler;

@vertex
fn vs(@builtin(vertex_index) i: u32) -> @builtin(position) vec4<f32> {
    let p = vec2<f32>(f32((i << 1u) & 2u), f32(i & 2u));
    return vec4<f32>(p * 2.0 - 1.0, 0.0, 1.0);
}

@fragment
fn fs(@builtin(position) pos: vec4<f32>) -> @location(0) vec4<f32> {
    let dpr = u.ink.w;
    let p = pos.xy / dpr;
    let pitch = 9.0;
    let c = (floor(p / pitch) + 0.5) * pitch;

    // the name, under this dot
    let uv = (c + vec2<f32>(0.0, u.scroll) - u.rect.xy) / u.rect.zw;
    var m = vec2<f32>(0.0);
    if (all(uv >= vec2<f32>(0.0)) && all(uv <= vec2<f32>(1.0))) {
        m = textureSampleLevel(mask, samp, uv, 0.0).rg;
    }
    let sharp = m.x;
    let glow = m.y;

    // a slow band of light drifting across the halo
    let wave = 0.5 + 0.5 * sin(dot(c, vec2<f32>(0.010, 0.006)) - u.time * 0.45);
    let d = length(c - u.pointer);
    let near = exp(-(d * d) / (2.0 * 130.0 * 130.0));

    var level = 0.05 + glow * (0.30 + 0.12 * wave) + sharp * 0.10 + near * (0.10 + 0.25 * glow);
    // the load sequence: dots appear from the letters outward
    let reach = clamp(u.intro * 1.6 - (1.0 - glow) * 0.9, 0.0, 1.0);
    level = level * reach;

    let r = 0.45 + 1.35 * level;
    let edge = 0.75 / dpr;
    let a = (1.0 - smoothstep(r - edge, r + edge, length(p - c))) * clamp(level * 2.4, 0.0, 0.9);
    if (u.opaque > 0.5) {
        return vec4<f32>(mix(u.ground.rgb, u.ink.rgb, a), 1.0);
    }
    return vec4<f32>(u.ink.rgb * a, a);
}
"#;

    fn media(win: &Window, q: &str) -> bool {
        win.match_media(q).ok().flatten().map(|m| m.matches()).unwrap_or(false)
    }

    /// The ink and ground colours for the current scheme (base.css tokens).
    fn palette(win: &Window) -> ([f32; 3], [f32; 3]) {
        if media(win, "(prefers-color-scheme: light)") {
            ([0x15 as f32 / 255.0, 0x15 as f32 / 255.0, 0x13 as f32 / 255.0], [0xF1 as f32 / 255.0, 0xEE as f32 / 255.0, 0xE7 as f32 / 255.0])
        } else {
            ([0xEC as f32 / 255.0, 0xE8 as f32 / 255.0, 0xDF as f32 / 255.0], [0x0F as f32 / 255.0, 0x0F as f32 / 255.0, 0x0D as f32 / 255.0])
        }
    }

    /// Draws the name's mask into a 2D canvas: R the letterforms, G a wide
    /// blur. Returns the canvas and its rectangle in document CSS px.
    fn draw_mask(doc: &Document, win: &Window, scale: f64) -> Result<Option<(HtmlCanvasElement, [f32; 4])>, JsValue> {
        // the .line wrappers, not the spans inside: those are mid-animation
        // (translated) during the load sequence
        let lines = doc.query_selector_all(".name .line")?;
        if lines.length() == 0 {
            return Ok(None);
        }
        let scroll_y = win.scroll_y()?;
        let name = doc.query_selector(".name")?.ok_or("no .name")?;
        let r = name.get_bounding_client_rect();
        let pad = 120.0;
        let (x0, y0) = (r.left() - pad, r.top() + scroll_y - pad);
        let (w, h) = (r.width() + 2.0 * pad, r.height() + 2.0 * pad);
        let c: HtmlCanvasElement = doc.create_element("canvas")?.dyn_into()?;
        c.set_width(((w * scale).ceil() as u32).max(1));
        c.set_height(((h * scale).ceil() as u32).max(1));
        let ctx: web_sys::CanvasRenderingContext2d = c.get_context("2d")?.ok_or("no 2d")?.dyn_into()?;
        ctx.set_fill_style_str("#000");
        ctx.fill_rect(0.0, 0.0, c.width() as f64, c.height() as f64);
        ctx.scale(scale, scale)?;
        ctx.set_text_baseline("alphabetic");
        let style_of = |e: &Element, k: &str| -> String {
            win.get_computed_style(e)
                .ok()
                .flatten()
                .and_then(|s| s.get_property_value(k).ok())
                .unwrap_or_default()
        };
        let mut texts = Vec::new();
        for i in 0..lines.length() {
            let Some(e) = lines.get(i).and_then(|n| n.dyn_into::<Element>().ok()) else { continue };
            let lr = e.get_bounding_client_rect();
            let text = e.text_content().unwrap_or_default();
            let font = format!(
                "{} {} {} {}",
                style_of(&e, "font-style"),
                style_of(&e, "font-weight"),
                style_of(&e, "font-size"),
                style_of(&e, "font-family")
            );
            let px: f64 = style_of(&e, "font-size").trim_end_matches("px").parse().unwrap_or(160.0);
            ctx.set_font(&font);
            // the baseline: the glyphs' box is centred in the line's content
            // box (the line's height less its .06em bottom padding)
            let tm = ctx.measure_text(&text)?;
            let (asc, desc) = (tm.font_bounding_box_ascent(), tm.font_bounding_box_descent());
            let content = lr.height() - 0.06 * px;
            let base = lr.top() + scroll_y - y0 + (content - (asc + desc)) / 2.0 + asc;
            texts.push((text, font, lr.left() - x0, base));
        }
        ctx.set_global_composite_operation("lighter")?;
        ctx.set_fill_style_str("#ff0000");
        for (t, f, x, y) in &texts {
            ctx.set_font(f);
            ctx.fill_text(t, *x, *y)?;
        }
        ctx.set_filter("blur(28px)");
        ctx.set_fill_style_str("#00ff00");
        for (t, f, x, y) in &texts {
            ctx.set_font(f);
            ctx.fill_text(t, *x, *y)?;
            ctx.fill_text(t, *x, *y)?;
        }
        ctx.set_filter("none");
        Ok(Some((c, [x0 as f32, y0 as f32, w as f32, h as f32])))
    }

    struct Gpu {
        device: wgpu::Device,
        queue: wgpu::Queue,
        bgl: wgpu::BindGroupLayout,
        uniform: wgpu::Buffer,
        sampler: wgpu::Sampler,
        max_dim: u32,
    }

    struct Mask {
        _tex: wgpu::Texture,
        bind: wgpu::BindGroup,
        rect: [f32; 4],
    }

    pub async fn start(app: Rc<App>) -> Result<(), String> {
        let win = web_sys::window().ok_or("no window")?;
        let doc = app.doc.clone();
        let canvas: HtmlCanvasElement = doc
            .get_element_by_id("field")
            .ok_or("no #field")?
            .dyn_into()
            .map_err(|_| "#field is not a canvas")?;
        let has_gpu = js_sys::Reflect::get(&win.navigator(), &"gpu".into())
            .map(|g| !g.is_undefined() && !g.is_null())
            .unwrap_or(false);
        if !has_gpu {
            return Err("navigator.gpu is missing".into());
        }
        let dpr = win.device_pixel_ratio().clamp(1.0, 2.0);
        let size = move |c: &HtmlCanvasElement| {
            (
                ((c.client_width() as f64 * dpr) as u32).max(1),
                ((c.client_height() as f64 * dpr) as u32).max(1),
            )
        };
        let (width, height) = size(&canvas);
        canvas.set_width(width);
        canvas.set_height(height);

        let instance = wgpu::Instance::default();
        let surface = instance
            .create_surface(wgpu::SurfaceTarget::Canvas(canvas.clone()))
            .map_err(|e| format!("surface: {e}"))?;
        let adapter = instance
            .request_adapter(&wgpu::RequestAdapterOptions {
                power_preference: wgpu::PowerPreference::HighPerformance,
                force_fallback_adapter: false,
                compatible_surface: Some(&surface),
            })
            .await
            .map_err(|e| format!("adapter: {e}"))?;
        let (device, queue) = adapter
            .request_device(&wgpu::DeviceDescriptor {
                label: None,
                required_features: wgpu::Features::empty(),
                required_limits: wgpu::Limits::default(),
                memory_hints: wgpu::MemoryHints::default(),
                experimental_features: wgpu::ExperimentalFeatures::default(),
                trace: wgpu::Trace::Off,
            })
            .await
            .map_err(|e| format!("device: {e}"))?;
        // the content is DOM, so losing the GPU only loses the field: hide it
        // and tell the observer, without asking the parent to replace us
        device.set_device_lost_callback(|reason, msg| {
            LOST.store(true, Ordering::Relaxed);
            if let Some(c) = web_sys::window().and_then(|w| w.document()).and_then(|d| d.get_element_by_id("field")) {
                let _ = c.class_list().remove_1("on");
            }
            if reason != wgpu::DeviceLostReason::Destroyed {
                report(&format!("site: WebGPU device lost ({msg}); the field is off, the content is fine"), "error");
            }
        });
        device.on_uncaptured_error(Arc::new(|e: wgpu::Error| {
            web_sys::console::warn_1(&format!("site: WebGPU error: {e}").into());
        }));
        let max_dim = device.limits().max_texture_dimension_2d;

        let caps = surface.get_capabilities(&adapter);
        let format = *caps.formats.first().ok_or("no surface formats")?;
        let alpha = if caps.alpha_modes.contains(&wgpu::CompositeAlphaMode::PreMultiplied) {
            wgpu::CompositeAlphaMode::PreMultiplied
        } else {
            caps.alpha_modes[0]
        };
        let opaque = alpha != wgpu::CompositeAlphaMode::PreMultiplied;
        let mut config = wgpu::SurfaceConfiguration {
            usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
            format,
            width: width.min(max_dim),
            height: height.min(max_dim),
            present_mode: wgpu::PresentMode::Fifo,
            desired_maximum_frame_latency: 2,
            alpha_mode: alpha,
            view_formats: vec![],
        };
        surface.configure(&device, &config);

        let uniform = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("u"),
            size: 96,
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        let sampler = device.create_sampler(&wgpu::SamplerDescriptor {
            mag_filter: wgpu::FilterMode::Linear,
            min_filter: wgpu::FilterMode::Linear,
            ..Default::default()
        });
        let bgl = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: None,
            entries: &[
                wgpu::BindGroupLayoutEntry {
                    binding: 0,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Buffer {
                        ty: wgpu::BufferBindingType::Uniform,
                        has_dynamic_offset: false,
                        min_binding_size: None,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 1,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Float { filterable: true },
                        view_dimension: wgpu::TextureViewDimension::D2,
                        multisampled: false,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 2,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                    count: None,
                },
            ],
        });
        let layout = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
            label: None,
            bind_group_layouts: &[Some(&bgl)],
            immediate_size: 0,
        });
        let module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("field"),
            source: wgpu::ShaderSource::Wgsl(SHADER.into()),
        });
        let pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
            label: Some("field"),
            layout: Some(&layout),
            vertex: wgpu::VertexState {
                module: &module,
                entry_point: Some("vs"),
                buffers: &[],
                compilation_options: Default::default(),
            },
            fragment: Some(wgpu::FragmentState {
                module: &module,
                entry_point: Some("fs"),
                targets: &[Some(format.into())],
                compilation_options: Default::default(),
            }),
            primitive: wgpu::PrimitiveState::default(),
            depth_stencil: None,
            multisample: wgpu::MultisampleState::default(),
            multiview_mask: None,
            cache: None,
        });

        // an empty mask until the name is laid out (and on routes without it)
        let gpu = Gpu { device: device.clone(), queue: queue.clone(), bgl, uniform: uniform.clone(), sampler, max_dim };
        fn make_mask(g: &Gpu, source: Option<&HtmlCanvasElement>, rect: [f32; 4]) -> Mask {
            let Gpu { device, queue, bgl, uniform, sampler, max_dim } = g;
            let max_dim = *max_dim;
            let (w, h) = source.map(|c| (c.width().clamp(1, max_dim), c.height().clamp(1, max_dim))).unwrap_or((1, 1));
            let tex = device.create_texture(&wgpu::TextureDescriptor {
                label: Some("mask"),
                size: wgpu::Extent3d { width: w, height: h, depth_or_array_layers: 1 },
                mip_level_count: 1,
                sample_count: 1,
                dimension: wgpu::TextureDimension::D2,
                format: wgpu::TextureFormat::Rgba8Unorm,
                usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST | wgpu::TextureUsages::RENDER_ATTACHMENT,
                view_formats: &[],
            });
            if let Some(c) = source {
                queue.copy_external_image_to_texture(
                    &wgpu::CopyExternalImageSourceInfo {
                        source: wgpu::ExternalImageSource::HTMLCanvasElement(c.clone()),
                        origin: wgpu::Origin2d::ZERO,
                        flip_y: false,
                    },
                    wgpu::CopyExternalImageDestInfo {
                        texture: &tex,
                        mip_level: 0,
                        origin: wgpu::Origin3d::ZERO,
                        aspect: wgpu::TextureAspect::All,
                        color_space: wgpu::PredefinedColorSpace::Srgb,
                        premultiplied_alpha: false,
                    },
                    wgpu::Extent3d { width: w, height: h, depth_or_array_layers: 1 },
                );
            }
            let view = tex.create_view(&wgpu::TextureViewDescriptor::default());
            let bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
                label: None,
                layout: bgl,
                entries: &[
                    wgpu::BindGroupEntry { binding: 0, resource: uniform.as_entire_binding() },
                    wgpu::BindGroupEntry { binding: 1, resource: wgpu::BindingResource::TextureView(&view) },
                    wgpu::BindGroupEntry { binding: 2, resource: wgpu::BindingResource::Sampler(sampler) },
                ],
            });
            Mask { _tex: tex, bind, rect }
        }

        // pointer, in viewport CSS px (fine pointers only)
        let pointer = Rc::new(Cell::new((-1e5_f64, -1e5_f64)));
        if media(&win, "(hover: hover) and (pointer: fine)") {
            let p = pointer.clone();
            let mv = Closure::<dyn FnMut(web_sys::MouseEvent)>::new(move |e: web_sys::MouseEvent| {
                p.set((e.client_x() as f64, e.client_y() as f64));
            });
            let p = pointer.clone();
            let out = Closure::<dyn FnMut()>::new(move || p.set((-1e5, -1e5)));
            let _ = doc.add_event_listener_with_callback("mousemove", mv.as_ref().unchecked_ref());
            let _ = doc.add_event_listener_with_callback("mouseleave", out.as_ref().unchecked_ref());
            mv.forget();
            out.forget();
        }

        let still = media(&win, "(prefers-reduced-motion: reduce)");
        let perf = win.performance().ok_or("no performance")?;
        let t0 = perf.now();
        let mut mask: Option<Mask> = None;
        let mut mask_layout = u32::MAX;
        let mut shown = false;
        // smoothed pointer, so the lean eases instead of snapping
        let mut sp = (-1e5_f64, -1e5_f64);

        let f: Rc<RefCell<Option<Closure<dyn FnMut()>>>> = Rc::new(RefCell::new(None));
        let g = f.clone();
        let w2 = win.clone();
        *g.borrow_mut() = Some(Closure::new(move || {
            if LOST.load(Ordering::Relaxed) {
                return; // the field is gone; the content isn't
            }
            let (w, h) = size(&canvas);
            let (w, h) = (w.min(max_dim), h.min(max_dim));
            if w != config.width || h != config.height {
                canvas.set_width(w);
                canvas.set_height(h);
                config.width = w;
                config.height = h;
                surface.configure(&device, &config);
                app.layout.set(app.layout.get().wrapping_add(1));
            }
            if app.layout.get() != mask_layout || mask.is_none() {
                mask_layout = app.layout.get();
                let drawn = draw_mask(&doc, &w2, 0.5).ok().flatten();
                mask = Some(match drawn {
                    Some((c, rect)) => make_mask(&gpu, Some(&c), rect),
                    None => make_mask(&gpu, None, [0.0, -1e6, 1.0, 1.0]),
                });
            }
            let elapsed = ((perf.now() - t0) / 1000.0) as f32;
            let (t, intro) = if still { (0.0, 1.0) } else { (elapsed, ((elapsed - 0.25) / 1.4).clamp(0.0, 1.0)) };
            let target = pointer.get();
            if still || target.0 < -1e4 || sp.0 < -1e4 {
                sp = target;
            } else {
                sp.0 += (target.0 - sp.0) * 0.12;
                sp.1 += (target.1 - sp.1) * 0.12;
            }
            let (ink, ground) = palette(&w2);
            let rect = mask.as_ref().map(|m| m.rect).unwrap_or([0.0, -1e6, 1.0, 1.0]);
            let scroll = w2.scroll_y().unwrap_or(0.0) as f32;
            let u: [f32; 24] = [
                (w as f64 / dpr) as f32, (h as f64 / dpr) as f32, sp.0 as f32, sp.1 as f32,
                rect[0], rect[1], rect[2], rect[3],
                ink[0], ink[1], ink[2], dpr as f32,
                t, scroll, intro, if opaque { 1.0 } else { 0.0 },
                ground[0], ground[1], ground[2], 1.0,
                0.0, 0.0, 0.0, 0.0,
            ];
            queue.write_buffer(&uniform, 0, bytemuck::cast_slice(&u));

            let frame = match surface.get_current_texture() {
                wgpu::CurrentSurfaceTexture::Success(t) | wgpu::CurrentSurfaceTexture::Suboptimal(t) => Some(t),
                _ => {
                    surface.configure(&device, &config);
                    None
                }
            };
            if let (Some(frame), Some(m)) = (frame, mask.as_ref()) {
                let view = frame.texture.create_view(&wgpu::TextureViewDescriptor::default());
                let mut enc = device.create_command_encoder(&wgpu::CommandEncoderDescriptor { label: None });
                {
                    let mut rp = enc.begin_render_pass(&wgpu::RenderPassDescriptor {
                        label: None,
                        color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                            view: &view,
                            depth_slice: None,
                            resolve_target: None,
                            ops: wgpu::Operations {
                                load: wgpu::LoadOp::Clear(wgpu::Color::TRANSPARENT),
                                store: wgpu::StoreOp::Store,
                            },
                        })],
                        depth_stencil_attachment: None,
                        timestamp_writes: None,
                        occlusion_query_set: None,
                        multiview_mask: None,
                    });
                    rp.set_pipeline(&pipeline);
                    rp.set_bind_group(0, &m.bind, &[]);
                    rp.draw(0..3, 0..1);
                }
                queue.submit([enc.finish()]);
                frame.present();
                if !shown {
                    shown = true;
                    let _ = canvas.class_list().add_1("on");
                }
            }
            if let Some(cb) = f.borrow().as_ref() {
                let _ = w2.request_animation_frame(cb.as_ref().unchecked_ref());
            }
        }));
        if let Some(cb) = g.borrow().as_ref() {
            win.request_animation_frame(cb.as_ref().unchecked_ref())
                .map_err(|e| format!("requestAnimationFrame: {e:?}"))?;
        }
        Ok(())
    }
}
