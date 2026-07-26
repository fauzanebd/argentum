-- Add default_currency (ISO 4217) to companies.
-- Defaults to 'USD' so existing rows get a safe value.
ALTER TABLE companies
  ADD COLUMN IF NOT EXISTS default_currency TEXT NOT NULL DEFAULT 'USD';
