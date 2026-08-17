-- Drops the table and every pick in it.
--
-- What that costs is the only evidence this product has about whether next-step
-- suggestions earn the space under an answer (T-U13). The suggestions themselves
-- survive — they live in messages.metadata and are unaffected — so reversing
-- this leaves the feature running and unmeasured, which is the state it was
-- built to end. Switch NEXT_STEPS_ENABLED off as well if the intent is to
-- withdraw the feature rather than only its instrument.
DROP TABLE IF EXISTS suggestion_picks;
