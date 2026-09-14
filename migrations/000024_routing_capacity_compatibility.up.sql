-- The IR/API routing limit increases from 1000 to the SRS baseline of 10000
-- ordered rules. Fence older application images before larger values can be
-- stored: a schema-23 image must not pass the compatible-image rollback check.
-- Existing immutable resource bytes and revisions remain unchanged.
COMMENT ON TABLE public.resource_revisions IS
  'Immutable ProxyLoom IR revisions; database schema 24 requires application support for up to 10000 ordered routing rules.';
