-- builtin/stream: the site version of Particle Stream (frontends/stream), a
-- clone of the experiment adapted to present the site's content. Added out of
-- the rotation; Ben adds it once he's looked at it. The experiment itself
-- (builtin/particle-stream) is kept as it was.

INSERT INTO frontends (ref, title, in_rotation)
VALUES ('builtin/stream', 'Particle Stream (site)', false)
ON CONFLICT (ref) DO NOTHING;
