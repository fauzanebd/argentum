-- Deliberately not reversed, for 043's reason: nothing records which rows the up
-- migration touched, so removing the two names would also take them from the
-- agents an administrator ticked by hand. Narrowing one agent is a checkbox in
-- Settings → Agents.
SELECT 1;
