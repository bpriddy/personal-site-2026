-- CMS content and the front-end registry. Seed rows match store.NewMemory.

CREATE TABLE pages (
    slug        text PRIMARY KEY,          -- '' is the home page
    title       text NOT NULL,
    body        text NOT NULL DEFAULT '',
    published   boolean NOT NULL DEFAULT false,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE experiments (
    slug        text PRIMARY KEY,
    title       text NOT NULL,
    summary     text NOT NULL DEFAULT '',
    published   boolean NOT NULL DEFAULT false,
    sort_order  integer NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE frontends (
    ref          text PRIMARY KEY,         -- validated by internal/frontend.Valid
    title        text NOT NULL,
    in_rotation  boolean NOT NULL DEFAULT false,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

INSERT INTO pages (slug, title, body, published)
VALUES ('', 'Ben Priddy', 'Home page copy goes here.', true);

INSERT INTO experiments (slug, title, summary, published)
VALUES ('particle-stream', 'Particle Stream',
        'Words as rocks in a stream: 500k WebGPU particles part around a cycling phrase.', true);

INSERT INTO frontends (ref, title, in_rotation) VALUES
    ('builtin/site', 'Site', true),
    ('builtin/particle-stream', 'Particle Stream', true);
