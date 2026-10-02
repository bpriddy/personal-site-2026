# Content contract and the site observer

Status (2026-09-30):

- **Built:**
  - layers 1–3: the contract, containment, and the observer with content
    generation and the admin section;
  - front-end revisions and the admin builder (`/admin/builder/`);
  - content drift: creative rebuilds of prompted front ends that don't show
    content the site has gained (revisions with author `observer`; see
    "Content drift" below).
- **Still to come:**
  - observer patches to prompted front ends that throw (frontend-error
    detections still need review);
  - (the public builder shipped 2026-10-01: `/build`, see frontend-protocol.md v1.2).

Two requirements from Ben:

1. Front ends must not be tightly coupled to the CMS schema. They must never
   fail on schema changes. Most changes will be additive, but the system can't
   start with a known edge-case bug.
2. When a front end (especially a prompted one with content-specific features)
   hits a type break or content that is missing or empty where it shouldn't
   be, the site must fail gracefully and **self-heal**. An **observer agent**
   catches the problem, plans the most straightforward fix, and decides
   whether it is a normal fix (apply it) or too disruptive (queue it for
   review). This shows up in the admin with notification dots.

The design has three layers. The first two make most breakage impossible or
invisible; the third handles what gets through.

## Layer 1: a tolerant content contract

The CMS schema and what front ends see are **decoupled by a contract**.

- **The contract** (`docs/content-contract.json`, JSON Schema) describes the
  shape of `site.content`. It is the only thing front ends may depend on,
  never the database schema.
- **Additive-only evolution.** A test compares the contract against the last
  released version and fails the build on any removal, rename or type change.
  New fields are allowed; breaking changes need a new major version and a
  migration period.
- **Server-side normalization.** `/api/site.json` is produced from the contract,
  not straight from the tables:
  - every field is always present, typed, and defaulted (`""`, `[]`, `false`),
    never `null` or missing;
  - it carries `contractVersion`.
- **Client-side normalization, a second net.** `site-host.js` normalizes
  again before handing content to the front end:
  - unknown fields pass through;
  - missing fields get defaults;
  - wrong types are coerced or defaulted.

  So even a version skew (a new server with an old cached front end, or the
  reverse) can't produce `undefined.foo`.
- **Safe accessors** in the host API:
  - `site.get("pages.0.title", "")`: path access with a fallback, never throws;
  - `site.pages()`, `site.page(slug)`: always return arrays or objects, never
    `undefined`.
- **Builder agent rule:** generated front ends must read content only through
  `site.content` and these accessors, render sensible fallbacks for empty
  values, and never assume a field is non-empty. This goes in the builder's
  system prompt next to the "always show the CMS content" rule, and is checked
  at submit time.

## Layer 2: runtime containment (already mostly built)

- Every front end runs in a sandboxed iframe. A crash can't touch the parent
  page, and the transcript is always there.
- The parent falls back to the default front end, then to the transcript, on
  failure. Visitors see content within 1.5s regardless.
- **New:** `site-host.js` catches errors during render and navigation, and the
  parent reports every `site:error` (including after `ready`) to the observer,
  not just to the console.

## Layer 3: the observer (self-healing)

**Principle: prioritize displaying the correct content.** The observer fixes
things so the page shows the right content, and applies its fixes itself.
Review is the exception, not the default.

### Coverage is explicit

Detection comes from guard points we place deliberately. There is no attempt at
catching everything. There are only a few cases to protect against:

| Case | Guard point | Example |
|---|---|---|
| **Content gap**: a field a front end needs is missing or empty | `site.field(item, "subtitle", {expect: "text"})` in the host API: returns a fallback immediately and reports the gap | a front end shows project subtitles; project X has none |
| **Type break**: a field has the wrong shape | the same call; normalization coerces what it safely can and reports the rest | expected text, got a list |
| **Front-end failure**: a prompted front end throws or never gets ready | `site-host.js` error capture and the parent's ready timeout | an unguarded `.length` on something undefined |

Front ends declare their expectations through `site.field` (the builder's
system prompt requires it for any content-specific feature). That call is the
tripwire, and the declared `expect` tells the observer what a correct value
looks like.

### Fixes

| Case | Fix | Applied |
|---|---|---|
| Content gap | **Generate the value from that item's other content** (Claude, structured output, `claude-opus-5-5`). A subtitle comes from the project's title, body and summary. | Immediately |
| Type break on a missing or empty value | Generate as for a gap | Immediately |
| Type break on a value you wrote | Propose the converted value; never overwrite yours | Review |
| Front-end failure (prompted front end) | Minimal patch, saved as a **new revision** | Immediately, if a headless render proves it loads, throws nothing, and shows the content |

Review is required only if:

- a patch fails that verification;
- a fix would **overwrite content you wrote**;
- the fix touches a built-in front end, which is code in the repo: the observer
  writes the patch and you merge it.

While a problem is unresolved, the failing front end is pulled from the
rotation (always safe and reversible) and the default front end serves.

### Generated content never overwrites yours

- Generated values live in their own table:
  - `generated_fields`, keyed by (collection, item, field);
  - each row stores the value, the model, and a hash of the source content it
    was generated from.
- The contract layer merges them in: **your value, then the generated value,
  then the default.** A generated field is marked in the payload
  (`_generated: ["subtitle"]`) so the admin and front ends can tell it apart.
- **Editing the item yourself** means your value wins from then on.
  Generated values whose source content changed are marked stale and
  regenerated the next time they're needed.
- Generation can create fields the CMS schema doesn't have yet (a `subtitle`
  on projects). This is how additive needs get met without a migration; if a
  field proves permanent, promote it into the schema.

### Detections and the admin

- The `detections` table records:
  - kind, front end, route, and the collection/item/field;
  - a deduplication signature, occurrence count, and first- and last-seen
    times;
  - a sample payload;
  - the fix applied or proposed;
  - status: `new`, `fixed`, `needs_review`, `reverted` or `dismissed`;
  - `seen_at`.
- **Observer** section in the admin:
  - a **notification dot** with a count of detections not yet seen;
  - dots on each new row, cleared when you view it.
- **Each detection** shows:
  - what happened and how often;
  - what the observer did, e.g. "generated a subtitle for Project X: '…'";
  - one-click **Accept** (promote a generated value into your content), **Edit**,
    **Regenerate**, **Revert** and **Dismiss**.

## Front-end revisions and reprompting

- Every front end is a series of **immutable revisions**:
  - `frontend_revisions`: ref, revision number, parent revision, files, the
    prompt conversation that produced it, author (you, a visitor, or the
    observer), and creation time;
  - a front end's **active revision** is what the rotation serves.
- **Reprompt** a front end from the admin:
  - the builder chat opens with the active revision's files and prompt
    history;
  - each result is a new revision;
  - the snapshot you started from is untouched.
- **Rollback** moves the active pointer to any earlier revision.
- Observer patches are revisions too (author: observer), so they show up in
  the same history and roll back the same way.

## Content drift

The data evolves; a prompted front end made before a collection or field
existed never shows it. Example: `projects` (v1.4) arrived after
`fe/a-set-of-webgpu-layers` v2 was made, so that front end never shows Ben's
work. The observer picks this up and has the builder rebuild the front end so
its creative concept accommodates the new content. Code:
`internal/contract/shape.go`, `internal/observer/{scan,drift}.go`,
`internal/server/observer_rebuild.go`.

### Detection

- **Published shape** (`contract.ShapeOf` over the `/api/site.json` payload):
  the collections with at least one published item, and per collection the
  fields non-empty in at least one item, with counts. Left out: `slug`, every
  `_`-prefixed key (`_generated`, `_collection`), and `contract.IgnoredFields`,
  the presentational-only fields: just `palette` today. A media item's own
  keys (kind, src, poster, width, height, alt) aren't fields; `media` counts
  as one. `Shape.Fingerprint()` hashes the collection and field names, not
  the counts.
- **Who is checked:** front ends **in the rotation** (`FRONTEND_ROTATION` if
  set, else the store's flag): prompted ones with an active revision, and
  built-ins.
- **Static scan** of the active revision's files (via `revfiles`): the code is
  the `.js`/`.mjs` files plus the inline `<script>`s of HTML files, without
  comments (so a CSS class or a heading named "projects" doesn't count). A
  collection is read if the code has its name as a string literal
  (`"projects"`, `'projects.0.title'`, `site.collection("projects")`), a
  property access (`.projects`, so `site.projects()`), a destructured name
  (`{ pages, projects }`), or its one-item accessor (`site.project(...)`,
  `site.page(...)`). For a collection it reads, each field non-empty in
  **at least 25%** of the items (`observer.FieldThreshold`) must appear as a
  string literal (`site.field(p, "client")`), property access or
  destructured name. The scan is deliberately lenient (a field named like
  one of another collection counts): it only rebuilds a front end that
  plainly never touches the content.
- **Built-ins** can't be scanned (their code is Rust in the repo):
  `builtinReads` lists what each renders (`builtin/site`: pages,
  experiments, projects). Others are taken to render nothing. Drift on a
  built-in is recorded as **needs review** and never rebuilt.
- A `content-drift` detection's signature is front end + active revision +
  the sorted missing set (`projects`, `experiments.link`, ...); repeats only
  bump its count. Its sample holds a headline ("A set of webgpu layers v2
  doesn't show projects"), what's missing with counts, and an example item.
- **When:** `DriftDelay` (30 s) after start, every `DriftEvery` (10 min), and
  `DriftDebounce` (3 s) after a content change. The hook
  (`Observer.ContentChanged`, non-blocking, coalesced) is called by page,
  experiment and project saves, the publish toggle, `POST
  /admin/import/projects`, rotation changes, revision activation and
  submission reviews.

### Healing: a creative rebuild

For a prompted front end, when rebuilding is enabled (`ANTHROPIC_API_KEY`
set; `cmd/server` passes the server as the `observer.Rebuilder`):

1. **Claim** in `observer_rebuilds` (migration 0006, atomic across
   instances): one attempt per (front end, missing-set fingerprint) **ever**;
   one rebuild running at a time; at most `OBSERVER_REBUILDS_PER_DAY` (default
   6) started in a rolling 24 hours. Over budget or busy, the detection stays
   `new` ("deferred") and the next check retries. An attempt left running
   past 30 min (its instance died) may be taken over.
2. **Rebuild** with the normal builder agent (same system prompt and
   `QualityBrief`, cache-stable): parent = the active revision, history = its
   conversation, plus a generated prompt: "The site's content has grown since
   this front end was made ... projects: 12 published items, which this front
   end doesn't show at all. Fields with values: ... Example item: {...}
   Update this front end so it presents this new content within its existing
   creative concept (the same idea, the same visual language), and keep
   everything that already works. Read it through the host API ..." The
   request is labelled as the site observer's, on Ben's behalf. The result
   is a new revision by author `observer` (the builder's history shows it,
   and the conversation shows "Site observer"). A chat run in progress on
   the same front end defers it.
3. **Verify:** the static scan runs on the new revision. Resolved means
   nothing of the original missing set is still missing (including the
   widely used fields of a collection that was missing whole). Then, if the
   active revision is still the parent, the new revision is **activated**:
   the detection is `fixed`, action `rebuild` (auto) with the revision, the
   previous one and their numbers ("Rebuilt v2 → v3 by the observer and
   activated it. <the model's summary>").
4. **Otherwise needs review**: unresolved (the revision is kept, not
   activated), a builder failure or refusal, or an active revision changed
   meanwhile. The detection says why and links the revision.

### Probation and rollback

For `Probation` (30 min) after an observer activation, that front end's
`frontend-error` reports (the existing ingestion) count toward a rollback
instead of the pull-from-rotation rule: at **3 errors** or **2 never-ready /
fallback reports** from the new revision, the previous revision is made
active again (`SetActiveRevision`); the detection goes to needs review with
action `rollback`, and the rebuilt revision stays in the history. Reports
from an older revision (a tab left open) don't count. Nothing is pulled
from the rotation during probation, and a built-in is never involved. The
counters are per instance (like the pull rule's); the probation itself is
on the detection, so every instance honours it.

### Admin

The Observer section lists content-drift detections with the headline,
what's missing (collection, fields, counts) and the outcome, with **Preview
v3 in the builder** (opens that revision), **Revert (activate v2)** (makes
the previous revision active; status reverted, and the observer won't
rebuild for the same missing set again) and **Dismiss**. They count toward
the notification dot like every detection.

### Deployment note

The checks and rebuilds run in the background. On Cloud Run with
request-based CPU allocation, background work only progresses while the
instance is serving requests; a rebuild takes minutes, so give the main
service CPU outside requests (`--no-cpu-throttling`) for rebuilds to run
promptly.

## Build order

1. **Contract.**
   - `content-contract.json`;
   - server normalization plus `contractVersion`;
   - host-API normalization, safe accessors and `site.field`;
   - the additive-only compatibility test;
   - update the built-in `site` front end;
   - e2e tests with malformed and missing content.
2. **Observer data and admin.**
   - `/api/observe` ingestion (rate limits, deduplication);
   - `detections` and `generated_fields` tables;
   - contract merge of generated values;
   - the admin section with notification dots.
3. **Observer fixes.**
   - content-gap generation and type-break handling (Claude);
   - Accept / Edit / Regenerate / Revert;
   - pull-from-rotation.
4. **Revisions and the admin builder.**
   - `frontend_revisions`;
   - Ben prompts and reprompts his own front ends;
   - rollback;
   - front ends served by revision from Cloud Storage.
5. **Observer front-end patches.** Patch revisions with the headless
   verification gate.
6. **Public builder.**
