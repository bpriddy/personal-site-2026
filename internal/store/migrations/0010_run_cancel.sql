-- Cancelling a builder run (protocol v1.11): the cancel request is recorded on
-- the run, so whichever server instance runs it sees it (it checks every few
-- seconds) and stops; the run then finishes as failed with error "canceled".

ALTER TABLE builder_runs ADD COLUMN cancel_requested boolean NOT NULL DEFAULT false;
