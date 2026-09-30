# Content contract and the site observer

Status: layers 1–3 built 2026-09-30 (the contract, containment, and the
observer with content generation and the admin section). Front-end patching
and revisions are still to come.

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
