-- Reversing this drops every tenant's widget greeting, prompts and theme. The
-- widget keeps working — it falls back to the Go defaults, which is exactly
-- what a company that never configured one already gets — so what is lost is
-- the customisation and not the feature.

ALTER TABLE companies DROP COLUMN IF EXISTS widget_config;
