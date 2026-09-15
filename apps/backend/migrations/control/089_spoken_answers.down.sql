-- A real drop, not `SELECT 1;`. What it loses is a cache: every row is an answer
-- that still exists as text, and the next press of its play button synthesises
-- it again — at the price of one synthesis, which is what the row was saving.
--
-- What it strands is the audio: objects under `voice/<company_id>/answers/` stay
-- in the bucket with no row left for the sweep to find them by. Remove that
-- prefix before rolling this back, not after. A company's erasure still removes
-- it, because it removes `voice/<company_id>/` whole.
DROP TABLE IF EXISTS spoken_answers;
