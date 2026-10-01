-- The public builder (docs/frontend-protocol.md, "v1.2 additions"): anonymous
-- visitor sessions, front-end ownership, submissions for review, and the
-- per-run bookkeeping the build limits count.

-- One row per visitor session that has made something. Only sha256(sid) is
-- stored, never the cookie value itself.
CREATE TABLE visitor_sessions (
    id_hash     bytea PRIMARY KEY CHECK (octet_length(id_hash) = 32),
    created_at  timestamptz NOT NULL DEFAULT now(),
    last_seen   timestamptz NOT NULL DEFAULT now()
);

-- A visitor-made front end's owner. NULL for Ben's own and the built-ins.
ALTER TABLE frontends
    ADD COLUMN owner_session bytea REFERENCES visitor_sessions (id_hash) ON DELETE SET NULL;
CREATE INDEX frontends_owner ON frontends (owner_session) WHERE owner_session IS NOT NULL;

-- Builder runs started by visitors carry their session and a hash of their
-- client IP, so the limits can count them (in the database, across instances).
-- Ben's runs leave both NULL and are never counted.
ALTER TABLE builder_runs
    ADD COLUMN session_hash bytea,
    ADD COLUMN ip_hash      bytea;
CREATE INDEX builder_runs_visitor_started ON builder_runs (started_at) WHERE session_hash IS NOT NULL;
CREATE INDEX builder_runs_session ON builder_runs (session_hash, started_at) WHERE session_hash IS NOT NULL;
CREATE INDEX builder_runs_ip ON builder_runs (ip_hash, started_at) WHERE ip_hash IS NOT NULL;

-- A revision submitted for Ben's review. A front end has at most one pending
-- submission; submitting again while pending replaces its revision.
CREATE TABLE frontend_submissions (
    id            bigserial PRIMARY KEY,
    frontend_id   text NOT NULL REFERENCES frontends (ref) ON DELETE CASCADE,
    revision_id   text NOT NULL REFERENCES frontend_revisions (id),
    status        text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'approved', 'rejected')),
    submitted_at  timestamptz NOT NULL DEFAULT now(),
    reviewed_at   timestamptz
);
CREATE UNIQUE INDEX frontend_submissions_one_pending ON frontend_submissions (frontend_id) WHERE status = 'pending';
CREATE INDEX frontend_submissions_frontend ON frontend_submissions (frontend_id, submitted_at DESC);
