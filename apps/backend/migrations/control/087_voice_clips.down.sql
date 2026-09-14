-- A real drop, not `SELECT 1;`. What it loses is at most SPEECH_RETENTION_DAYS of
-- transcripts, each of which the person who spoke either sent as a message —
-- where it survives — or chose not to.
--
-- What it strands is the audio: objects under `voice/<company_id>/` stay in the
-- bucket with no row left for the sweep to find them by. Run the sweep to
-- completion (or remove that prefix) before rolling this back, not after.
DROP TABLE IF EXISTS voice_clips;
