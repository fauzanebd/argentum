-- Reversible, for 082's reason rather than against 081's: this table holds only
-- what an admin granted on routes that did not exist before it. Dropping it
-- loses those grants and nothing else — no backfilled row, no tenant intent
-- recorded anywhere but here. And no route consults a capability on the day
-- this ships, so a company reverted to this state loses no access it had.
--
-- That last sentence stops being true the day a route asks for one. A revert
-- after that point takes the capability from everyone who was granted it, which
-- is fail-closed and is the direction a revert of an access table should fail.
DROP TABLE IF EXISTS user_capabilities;
