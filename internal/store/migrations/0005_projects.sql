-- Projects (client work at /work/<slug>) and media for experiments
-- (docs/frontend-protocol.md, "v1.4"). Media items are JSON objects
-- {kind, src, poster, width, height, alt}; src and poster are site paths
-- ("/media/projects/<slug>/..."): the files live in FRONTENDS_BUCKET under
-- media/ (MEDIA_DIR in dev), never in the database.

CREATE TABLE projects (
    slug          text PRIMARY KEY,
    title         text NOT NULL DEFAULT '',
    client        text NOT NULL DEFAULT '',
    agency        text NOT NULL DEFAULT '',
    year          text NOT NULL DEFAULT '',
    tags          jsonb NOT NULL DEFAULT '[]'::jsonb,
    roles         jsonb NOT NULL DEFAULT '[]'::jsonb,
    summary       text NOT NULL DEFAULT '',
    contribution  text NOT NULL DEFAULT '',
    body          text NOT NULL DEFAULT '',
    link          text NOT NULL DEFAULT '',
    palette       jsonb NOT NULL DEFAULT '[]'::jsonb,
    youtube       text NOT NULL DEFAULT '',
    media         jsonb NOT NULL DEFAULT '[]'::jsonb,
    sort_order    integer NOT NULL DEFAULT 0,
    published     boolean NOT NULL DEFAULT false,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE experiments
    ADD COLUMN link  text NOT NULL DEFAULT '',
    ADD COLUMN media jsonb NOT NULL DEFAULT '[]'::jsonb;
