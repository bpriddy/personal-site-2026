# Content contract and the site observer

Status: design, 2026-09-30. Nothing here is built yet.

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

### Detection sources

| Source | Examples |
|---|---|
| Browser reports, via `POST /api/observe` from the parent page (rate-limited, deduplicated) | uncaught errors, `TypeError`s, WebGPU validation errors, a front end never reaching ready, `gpu-lost` |
| Content validators, server-side, on save and on a schedule | published page with empty title or body; home page missing; experiment without a summary; contract-normalization warnings (a field had to be defaulted) |
| Synthetic checks, periodic headless render of each front end in the rotation against current content | a front end that renders blank, throws, or doesn't show CMS content |

### Detections

Stored in a `detections` table, **deduplicated by signature**. The signature
is a hash of the kind, front end, normalized error message, and route
pattern. Each row holds:

- kind, front-end ref and route;
- the signature, an occurrence count, and first- and last-seen times;
- a sample payload (message, stack, content-contract version, user agent);
- status: `new`, `triaging`, `auto_fixed`, `needs_review`, `resolved` or
  `dismissed`;
- `seen_at`, which drives the notification dots.

### Triage agent

For each new signature, the observer runs one Claude call (`claude-opus-5-5`,
structured output):

- **Input:**
  - the detection;
  - the content contract;
  - the relevant content snapshot;
  - for generated front ends, their source files.
- **Output:** a plan with:
  - `diagnosis`;
  - `fix_kind`: `frontend_patch`, `content_issue`, `contract_default`,
    `pull_from_rotation` or `none`;
  - `disruption`: `normal` or `review`;
  - the patch or proposed change;
  - the rationale.
- **Prompt rule:** choose the most straightforward, least clever fix: add a
  guard or fallback, never redesign or remove features.

### Normal vs. review

**"Normal" (auto-applied) must be provably safe:**

- **Defensive patches to generated front ends.**
  - Only null/empty guards, defaults, or type coercion.
  - Diff within a size limit.
  - Rendered in the headless checker against current content, where it must
    reach ready with no errors and still show the CMS content.
  - Applied as a **new revision**, so it can be rolled back with one click.
- **Temporarily pulling a failing front end from the rotation.** Always safe
  and reversible; the default front end takes its place.

**Always "review":**

- anything that changes layout, features or visible behavior;
- any change to the built-in front ends (they're code in the repo; the observer
  proposes a patch, a human merges it);
- **any content edit.** The agent never invents or rewrites your content. For
  empty-content detections it tells you what's missing and where.

### Admin

A new **Observer** section:

- A **notification dot** on the admin nav with a count of detections not yet
  seen. Dots on each new row. Viewing a detection marks it seen.
- **Tabs:** Needs review, Auto-fixed (with the diff and a Revert button),
  Resolved, Dismissed.
- **Each detection shows:** what happened and how often, the agent's diagnosis
  and plan, the patch diff, and Approve / Reject / Dismiss buttons.

## Build order

1. **Contract.**
   - `content-contract.json`;
   - server normalization plus `contractVersion`;
   - host-API normalization and safe accessors;
   - the additive-only compatibility test;
   - update the built-in `site` front end to use the accessors;
   - e2e tests with deliberately malformed content.
2. **Observer data and admin.**
   - `/api/observe` ingestion (rate limits, deduplication);
   - content validators;
   - the `detections` table;
   - the admin section with notification dots.
3. **Triage agent.**
   - Claude call with structured output;
   - normal/review classification;
   - "pull from rotation" auto-action;
   - review flow.
4. **Auto-patching generated front ends.** Needs the builder, since generated
   front ends are what it patches:
   - revisions;
   - the headless verification gate;
   - revert.
5. **Builder (admin-only first).** Ben prompts his own front ends from the
   admin; they go live through the same review and rotation. The public builder
   comes later.
