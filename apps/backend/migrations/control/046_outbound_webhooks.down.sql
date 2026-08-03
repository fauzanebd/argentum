-- Reversing T-15 drops the subscriptions and the link from the delivery log.
-- The deliveries themselves stay: they are the record of what was sent, and a
-- rollback of the subscription model is not a decision to forget that.
ALTER TABLE webhook_deliveries DROP COLUMN IF EXISTS subscription_id;
DROP INDEX IF EXISTS idx_webhook_subscriptions_company;
DROP TABLE IF EXISTS webhook_subscriptions;
