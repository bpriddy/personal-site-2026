-- "Email me when it's ready" for visitor builds (protocol v1.13). One row per
-- run that asked. email and link are the only copies of the visitor's address
-- and of their open-link token: both are cleared the moment the email goes out
-- (or the run is canceled). address_hash (sha256 of the normalized address)
-- stays, for the per-address daily cap.
CREATE TABLE build_notifications (
    run_id        bigint PRIMARY KEY REFERENCES builder_runs (id) ON DELETE CASCADE,
    email         text,
    link          text,
    address_hash  bytea NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    sent_at       timestamptz,
    outcome       text  -- ready | failed | canceled, once taken
);

CREATE INDEX build_notifications_address ON build_notifications (address_hash, created_at);
