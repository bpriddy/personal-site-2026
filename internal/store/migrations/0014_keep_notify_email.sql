-- From 2026-10-12 the visitor's address is kept after the email goes out
-- (Ben asked to keep them; the modal says Ben will see it). Only the link
-- token is cleared once sent. A request is pending while sent_at is NULL.
COMMENT ON TABLE build_notifications IS
  'Visitor build email requests. email is kept; link is cleared once sent; pending while sent_at is NULL.';
