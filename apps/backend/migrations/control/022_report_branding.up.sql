-- Per-tenant report branding: the mark a customer's own board sees.
--
-- Filed as T-R5's `030_report_branding`, renumbered to 022 for the reason 021
-- carries: golang-migrate only applies versions above the schema's current one,
-- so a number reserved for a ticket that has not landed yet is a number that
-- can never be applied. 021 took T-05's slot, this takes T-06's; both of those
-- tickets renumber when they land.
--
-- One jsonb column rather than seven typed ones. The fields here are a
-- presentation record read as a unit by exactly one consumer (the renderer),
-- never queried by, filtered on, or joined against — and the set will grow
-- (a dark-cover logo variant, a second accent) as the report track does. A
-- column per field would mean a migration per addition for no query benefit.
--
-- The default is an empty object, not NULL: the renderer resolves per field,
-- so `{}` and "no row" have to mean the same thing, and a NOT NULL default
-- means no caller has to spell that.

ALTER TABLE companies
    ADD COLUMN IF NOT EXISTS report_branding JSONB NOT NULL DEFAULT '{}'::jsonb;
