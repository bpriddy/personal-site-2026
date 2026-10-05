-- Builder attribution: how a front end's maker is credited in the site bar
-- while it's in the rotation ('' = none). A visitor asks for a credit when
-- submitting (credit_requested); approving the submission makes it the
-- credit, so nothing a visitor types goes public unreviewed. Ben can edit
-- the credit directly.

ALTER TABLE frontends
    ADD COLUMN credit text NOT NULL DEFAULT ''
        CHECK (char_length(credit) <= 80),
    ADD COLUMN credit_requested text NOT NULL DEFAULT ''
        CHECK (char_length(credit_requested) <= 80);
