-- Reversing 007 drops the document index. The files themselves are in object
-- storage and are not deleted here — which means reversing this orphans them:
-- nothing left in the database knows their keys. Empty the bucket separately if
-- that is what you meant.
DROP TABLE IF EXISTS documents;
