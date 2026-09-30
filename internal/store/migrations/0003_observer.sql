-- The site observer (docs/observer.md, "Layer 3"): detections reported by front
-- ends or found by the server's content checks, and observer-generated field
-- values that the content contract merges beneath human content.

CREATE TABLE detections (
    id          bigserial PRIMARY KEY,
    kind        text NOT NULL CHECK (kind IN ('content-gap', 'type-break', 'frontend-error', 'content-invalid')),
    frontend    text NOT NULL DEFAULT '',   -- front-end id; '' for server-side checks
    serve       text NOT NULL DEFAULT '',   -- servable ref
    route       text NOT NULL DEFAULT '',
    collection  text NOT NULL DEFAULT '',
    item        text NOT NULL DEFAULT '',
    field       text NOT NULL DEFAULT '',
    expect      text NOT NULL DEFAULT '',
    got         text NOT NULL DEFAULT '',
    signature   text NOT NULL UNIQUE,       -- dedupe key (see internal/observer)
    count       integer NOT NULL DEFAULT 1,
    first_seen  timestamptz NOT NULL DEFAULT now(),
    last_seen   timestamptz NOT NULL DEFAULT now(),
    sample      jsonb NOT NULL DEFAULT '{}', -- the latest report, as data (untrusted)
    status      text NOT NULL DEFAULT 'new'
                CHECK (status IN ('new', 'fixed', 'needs_review', 'reverted', 'dismissed')),
    action      jsonb NOT NULL DEFAULT '{}', -- what the observer did or proposes
    seen_at     timestamptz                  -- NULL until viewed in the admin
);

CREATE INDEX detections_status_idx ON detections (status, last_seen DESC);
CREATE INDEX detections_unseen_idx ON detections (id) WHERE seen_at IS NULL;

CREATE TABLE generated_fields (
    collection   text NOT NULL,              -- 'pages', 'experiments'
    item         text NOT NULL,              -- the item's slug
    field        text NOT NULL,
    value        text NOT NULL,              -- text as is; list/number/bool as JSON
    expect       text NOT NULL DEFAULT 'text' CHECK (expect IN ('text', 'list', 'number', 'bool')),
    model        text NOT NULL,              -- model id, or 'human' for admin edits
    source_hash  text NOT NULL,              -- hash of the item's human content it came from
    status       text NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active', 'accepted', 'cleared', 'stale')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (collection, item, field)
);
