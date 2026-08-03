-- Reversing 012 drops every tenant's own LLM credentials. Those are encrypted
-- secrets the tenant typed in and we cannot reproduce, and a company that had
-- one becomes a company billed against the platform key — which is a billing
-- change, not only a schema one. Back the table up first if the rollback is
-- meant to be temporary.
DROP TABLE IF EXISTS company_llm_credentials;
