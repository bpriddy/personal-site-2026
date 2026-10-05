-- Ben's career roles for the home page's experience list
-- (docs/frontend-protocol.md, "v1.8"): minimal by design. start and end are
-- "YYYY-MM" or "YYYY"; end is '' for the current role.

CREATE TABLE experience (
    slug        text PRIMARY KEY,
    role        text NOT NULL DEFAULT '',
    company     text NOT NULL DEFAULT '',
    start_month text NOT NULL DEFAULT '',
    end_month   text NOT NULL DEFAULT '',
    current     boolean NOT NULL DEFAULT false,
    note        text NOT NULL DEFAULT '',
    sort_order  integer NOT NULL DEFAULT 0,
    published   boolean NOT NULL DEFAULT false,
    updated_at  timestamptz NOT NULL DEFAULT now()
);
