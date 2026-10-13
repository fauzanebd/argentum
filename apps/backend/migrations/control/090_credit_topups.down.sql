-- Loses the top-up history. Credit that was approved is already in
-- company_credits.balance_micro_usd and stays there.
DROP TABLE IF EXISTS credit_topups;
ALTER TABLE company_credits DROP COLUMN IF EXISTS last_topup_at;
