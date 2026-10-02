-- Content drift (docs/observer.md, "Content drift"): the observer detects
-- prompted front ends in the rotation that don't read content the site now
-- has, and rebuilds them. Each rebuild attempt is recorded here: one attempt
-- per (front end, missing-set fingerprint) ever, and a daily budget counted
-- across instances.

ALTER TABLE detections DROP CONSTRAINT detections_kind_check;
ALTER TABLE detections ADD CONSTRAINT detections_kind_check
    CHECK (kind IN ('content-gap', 'type-break', 'frontend-error', 'content-invalid', 'content-drift'));

CREATE TABLE observer_rebuilds (
    id            bigserial PRIMARY KEY,
    frontend      text NOT NULL,             -- 'fe/<slug>'
    fingerprint   text NOT NULL,             -- the missing set it tried to fix
    detection_id  bigint REFERENCES detections (id) ON DELETE SET NULL,
    parent        text NOT NULL DEFAULT '',  -- the revision it started from
    revision      text NOT NULL DEFAULT '',  -- the revision it made, if any
    status        text NOT NULL DEFAULT 'running'
                  CHECK (status IN ('running', 'done', 'failed')),
    error         text NOT NULL DEFAULT '',
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    UNIQUE (frontend, fingerprint)
);

CREATE INDEX observer_rebuilds_started ON observer_rebuilds (started_at DESC);
