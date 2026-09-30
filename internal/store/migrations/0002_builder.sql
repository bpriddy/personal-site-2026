-- Prompted front ends and their immutable revisions (docs/frontend-protocol.md,
-- "Front ends vs. revisions"), plus builder runs (one chat turn each).
-- Existing rows stay valid: they are built-ins with no active revision.

ALTER TABLE frontends
    ADD COLUMN kind text NOT NULL DEFAULT 'builtin'
        CHECK (kind IN ('builtin', 'prompted')),
    ADD COLUMN active_revision text;  -- prompted only; NULL = not servable yet

CREATE TABLE frontend_revisions (
    id            text PRIMARY KEY CHECK (id ~ '^[a-z0-9]{8,40}$'),
    frontend_id   text NOT NULL REFERENCES frontends (ref) ON DELETE CASCADE,
    number        integer NOT NULL,          -- 1, 2, ... per front end
    parent_id     text REFERENCES frontend_revisions (id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    author        text NOT NULL,             -- 'ben', 'observer', ...
    summary       text NOT NULL DEFAULT '',
    conversation  jsonb NOT NULL DEFAULT '[]',  -- the prompt history that produced it
    files         jsonb NOT NULL DEFAULT '[]',  -- manifest: [{path, size, sha256}]
    UNIQUE (frontend_id, number)
);

ALTER TABLE frontends
    ADD CONSTRAINT frontends_active_revision_fkey
        FOREIGN KEY (active_revision) REFERENCES frontend_revisions (id);

CREATE TABLE builder_runs (
    id           bigserial PRIMARY KEY,
    frontend_id  text NOT NULL REFERENCES frontends (ref) ON DELETE CASCADE,
    parent_id    text REFERENCES frontend_revisions (id),
    prompt       text NOT NULL,
    status       text NOT NULL DEFAULT 'running'
                 CHECK (status IN ('running', 'done', 'failed')),
    revision_id  text REFERENCES frontend_revisions (id),
    error        text NOT NULL DEFAULT '',
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz
);

CREATE INDEX builder_runs_frontend ON builder_runs (frontend_id, started_at DESC);
