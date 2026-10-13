-- /resume (protocol v1.14): what the resume says beyond the roles and
-- projects (content.Resume, as JSON). One row; none means the defaults.
CREATE TABLE site_resume (
    id          boolean PRIMARY KEY DEFAULT true CHECK (id),
    data        jsonb NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);
