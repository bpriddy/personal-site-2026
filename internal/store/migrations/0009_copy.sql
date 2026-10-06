-- Editable copy (protocol v1.10). site_copy holds the site-wide lines every
-- front end can use (tagline, the site bar's concept line, the empty
-- Experiments text); frontends.copy holds Ben's edits to one front end's own
-- wording (the keys its copy.json declares). Missing or empty = the default.

CREATE TABLE site_copy (
    key        text PRIMARY KEY CHECK (key ~ '^[a-zA-Z][a-zA-Z0-9_]{0,47}$'),
    value      text NOT NULL CHECK (char_length(value) <= 2000),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE frontends ADD COLUMN copy jsonb NOT NULL DEFAULT '{}';
