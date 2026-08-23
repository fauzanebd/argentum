-- Migration: Generate fact_sales data
-- 6 months of sample sales transactions (July 2024 - December 2024)
--
-- **Every draw in this file is deterministic, and that is the point.**
-- The first version of this fixture called `random()`, so each fresh volume
-- produced a different warehouse while `testdata/eval/golden.yaml` pinned 22
-- numeric expectations to one of them. The row count is fixed by the date range
-- and reproduced; the money did not, and by 2026-08-23 the two had drifted three
-- orders of magnitude apart — the agent answering 126,284,100 to a case
-- expecting 21,231,619,600 and being scored wrong for it
-- (docs/coverage/live-gate-backlog.md §2b).
--
-- `setseed()` would not have been enough. It makes one session's `random()`
-- sequence repeatable, but the values still land on rows in whatever order the
-- planner produces them, so a plan change silently re-rolls the warehouse.
-- `demo_rand()` below hashes a key built from the row's own identity instead:
-- the same row gets the same draw regardless of plan, parallelism or Postgres
-- version. Reproducibility is a property of the data, not of the execution.
--
-- The second defect fixed here is the LATERAL pick. It read
-- `SELECT * FROM customer_products ORDER BY random() LIMIT 1` with no reference
-- to the outer row, so it was uncorrelated and Postgres evaluated it **once per
-- statement**: all 1,104 rows of the first INSERT shared one customer and one
-- product, and all 244 of the second shared another. A 30-product catalogue
-- reached the eval as a 2-product warehouse, which made every top-N, grouping
-- and category case in the golden set vacuous. The pick below names `d` and
-- `seq`, so it is correlated and re-drawn per row.

\c demo_analytics;

-- A repeatable draw in [0,1) from an arbitrary key. IMMUTABLE and hash-based:
-- no session state, no dependence on row order.
CREATE OR REPLACE FUNCTION demo_rand(seed text) RETURNS double precision AS $$
    SELECT (('x' || substr(md5(seed), 1, 7))::bit(28)::int)::double precision / 268435456.0;
$$ LANGUAGE sql IMMUTABLE;

-- Generate sales transactions
-- 184 days x 6 = 1,104 transactions
WITH customer_products AS (
    SELECT
        c.customer_id,
        p.product_id,
        p.unit_price,
        p.unit_cost,
        p.category
    FROM dim_customers c
    CROSS JOIN dim_products p
    WHERE c.is_active = TRUE AND p.is_active = TRUE
),
dates AS (
    SELECT date_id, full_date
    FROM dim_date
    WHERE full_date BETWEEN '2024-07-01' AND '2024-12-31'
),
transactions AS (
    SELECT
        'TXN' || TO_CHAR(d.full_date, 'YYYYMMDD') || LPAD(seq::text, 4, '0') as transaction_id,
        d.date_id,
        cp.customer_id,
        cp.product_id,
        d.full_date + (demo_rand(d.date_id::text || ':' || seq::text || ':hour') * INTERVAL '23 hours') as created_at,
        -- Quantity, weighted toward lower numbers
        CASE
            WHEN demo_rand(d.date_id::text || ':' || seq::text || ':qty') < 0.5 THEN 1
            WHEN demo_rand(d.date_id::text || ':' || seq::text || ':qty') < 0.8 THEN 2
            WHEN demo_rand(d.date_id::text || ':' || seq::text || ':qty') < 0.95 THEN 3
            ELSE 4 + floor(demo_rand(d.date_id::text || ':' || seq::text || ':qty2') * 5)::int
        END as quantity,
        cp.unit_price,
        cp.unit_cost,
        -- Discount: 0%, 5%, 10% or 15%
        CASE
            WHEN demo_rand(d.date_id::text || ':' || seq::text || ':disc') < 0.6 THEN 0
            WHEN demo_rand(d.date_id::text || ':' || seq::text || ':disc') < 0.8 THEN 0.05
            WHEN demo_rand(d.date_id::text || ':' || seq::text || ':disc') < 0.95 THEN 0.10
            ELSE 0.15
        END as discount_rate,
        CASE (floor(demo_rand(d.date_id::text || ':' || seq::text || ':pay') * 5)::int)
            WHEN 0 THEN 'Cash'
            WHEN 1 THEN 'Credit Card'
            WHEN 2 THEN 'Debit Card'
            WHEN 3 THEN 'E-Wallet'
            ELSE 'Bank Transfer'
        END as payment_method,
        CASE
            WHEN demo_rand(d.date_id::text || ':' || seq::text || ':chan') < 0.6 THEN 'In-Store'
            WHEN demo_rand(d.date_id::text || ':' || seq::text || ':chan') < 0.85 THEN 'Online'
            ELSE 'Marketplace'
        END as sales_channel
    FROM dates d
    CROSS JOIN generate_series(1, 6) seq
    -- Correlated on d and seq, so the pick is re-drawn for every transaction.
    JOIN LATERAL (
        SELECT x.*
        FROM customer_products x
        ORDER BY demo_rand(d.date_id::text || ':' || seq::text || ':pick:'
                           || x.customer_id::text || ':' || x.product_id::text)
        LIMIT 1
    ) cp ON true
    ORDER BY d.full_date, seq
)
INSERT INTO fact_sales (
    transaction_id, date_id, customer_id, product_id, quantity,
    unit_price, discount_amount, sales_amount, cost_amount, profit_amount,
    payment_method, sales_channel, created_at
)
SELECT
    transaction_id,
    date_id,
    customer_id,
    product_id,
    quantity,
    unit_price,
    ROUND((unit_price * quantity * discount_rate)::numeric, 2) as discount_amount,
    ROUND((unit_price * quantity * (1 - discount_rate))::numeric, 2) as sales_amount,
    ROUND((unit_cost * quantity)::numeric, 2) as cost_amount,
    ROUND((unit_price * quantity * (1 - discount_rate) - unit_cost * quantity)::numeric, 2) as profit_amount,
    payment_method,
    sales_channel,
    created_at
FROM transactions;

-- Add more transactions for the holiday season (Nov-Dec)
-- 61 days x 4 = 244 transactions
WITH customer_products AS (
    SELECT
        c.customer_id,
        p.product_id,
        p.unit_price,
        p.unit_cost
    FROM dim_customers c
    CROSS JOIN dim_products p
    WHERE c.is_active = TRUE AND p.is_active = TRUE
),
dates AS (
    SELECT date_id, full_date
    FROM dim_date
    WHERE full_date BETWEEN '2024-11-01' AND '2024-12-31'
),
additional_transactions AS (
    SELECT
        'TXN' || TO_CHAR(d.full_date, 'YYYYMMDD') || LPAD((seq + 1000)::text, 4, '0') as transaction_id,
        d.date_id,
        cp.customer_id,
        cp.product_id,
        d.full_date + (demo_rand(d.date_id::text || ':h' || seq::text || ':hour') * INTERVAL '23 hours') as created_at,
        CASE
            WHEN demo_rand(d.date_id::text || ':h' || seq::text || ':qty') < 0.5 THEN 1
            WHEN demo_rand(d.date_id::text || ':h' || seq::text || ':qty') < 0.8 THEN 2
            WHEN demo_rand(d.date_id::text || ':h' || seq::text || ':qty') < 0.95 THEN 3
            ELSE 4 + floor(demo_rand(d.date_id::text || ':h' || seq::text || ':qty2') * 5)::int
        END as quantity,
        cp.unit_price,
        cp.unit_cost,
        CASE
            WHEN demo_rand(d.date_id::text || ':h' || seq::text || ':disc') < 0.5 THEN 0
            WHEN demo_rand(d.date_id::text || ':h' || seq::text || ':disc') < 0.75 THEN 0.05
            WHEN demo_rand(d.date_id::text || ':h' || seq::text || ':disc') < 0.90 THEN 0.10
            ELSE 0.20
        END as discount_rate,
        CASE (floor(demo_rand(d.date_id::text || ':h' || seq::text || ':pay') * 5)::int)
            WHEN 0 THEN 'Cash'
            WHEN 1 THEN 'Credit Card'
            WHEN 2 THEN 'Debit Card'
            WHEN 3 THEN 'E-Wallet'
            ELSE 'Bank Transfer'
        END as payment_method,
        CASE
            WHEN demo_rand(d.date_id::text || ':h' || seq::text || ':chan') < 0.5 THEN 'Online'
            WHEN demo_rand(d.date_id::text || ':h' || seq::text || ':chan') < 0.8 THEN 'In-Store'
            ELSE 'Marketplace'
        END as sales_channel
    FROM dates d
    CROSS JOIN generate_series(1, 4) seq
    JOIN LATERAL (
        SELECT x.*
        FROM customer_products x
        ORDER BY demo_rand(d.date_id::text || ':h' || seq::text || ':pick:'
                           || x.customer_id::text || ':' || x.product_id::text)
        LIMIT 1
    ) cp ON true
    ORDER BY d.full_date, seq
)
INSERT INTO fact_sales (
    transaction_id, date_id, customer_id, product_id, quantity,
    unit_price, discount_amount, sales_amount, cost_amount, profit_amount,
    payment_method, sales_channel, created_at
)
SELECT
    transaction_id,
    date_id,
    customer_id,
    product_id,
    quantity,
    unit_price,
    ROUND((unit_price * quantity * discount_rate)::numeric, 2) as discount_amount,
    ROUND((unit_price * quantity * (1 - discount_rate))::numeric, 2) as sales_amount,
    ROUND((unit_cost * quantity)::numeric, 2) as cost_amount,
    ROUND((unit_price * quantity * (1 - discount_rate) - unit_cost * quantity)::numeric, 2) as profit_amount,
    payment_method,
    sales_channel,
    created_at
FROM additional_transactions;

-- Verify data counts
SELECT
    'dim_date' as table_name, COUNT(*) as row_count FROM dim_date
UNION ALL
SELECT 'dim_customers', COUNT(*) FROM dim_customers
UNION ALL
SELECT 'dim_products', COUNT(*) FROM dim_products
UNION ALL
SELECT 'fact_sales', COUNT(*) FROM fact_sales;
