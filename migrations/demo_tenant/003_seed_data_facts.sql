-- Migration: Generate fact_sales data
-- 6 months of sample sales transactions (July 2024 - December 2024)

\c demo_analytics;

-- Generate sales transactions
-- ~1000 transactions distributed across 6 months

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
        d.full_date + (random() * INTERVAL '23 hours') as created_at,
        -- Random quantity weighted toward lower numbers
        CASE 
            WHEN random() < 0.5 THEN 1
            WHEN random() < 0.8 THEN 2
            WHEN random() < 0.95 THEN 3
            ELSE 4 + floor(random() * 5)::int
        END as quantity,
        cp.unit_price,
        cp.unit_cost,
        -- Random discount: 0%, 5%, 10%, or 15%
        CASE 
            WHEN random() < 0.6 THEN 0
            WHEN random() < 0.8 THEN 0.05
            WHEN random() < 0.95 THEN 0.10
            ELSE 0.15
        END as discount_rate,
        -- Payment method
        CASE (floor(random() * 5)::int)
            WHEN 0 THEN 'Cash'
            WHEN 1 THEN 'Credit Card'
            WHEN 2 THEN 'Debit Card'
            WHEN 3 THEN 'E-Wallet'
            ELSE 'Bank Transfer'
        END as payment_method,
        -- Sales channel
        CASE 
            WHEN random() < 0.6 THEN 'In-Store'
            WHEN random() < 0.85 THEN 'Online'
            ELSE 'Marketplace'
        END as sales_channel
    FROM dates d
    CROSS JOIN generate_series(1, 6) seq
    JOIN LATERAL (
        SELECT * FROM customer_products
        ORDER BY random()
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

-- Add more transactions for higher volume months (Nov-Dec holiday season)
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
        d.full_date + (random() * INTERVAL '23 hours') as created_at,
        CASE 
            WHEN random() < 0.5 THEN 1
            WHEN random() < 0.8 THEN 2
            WHEN random() < 0.95 THEN 3
            ELSE 4 + floor(random() * 5)::int
        END as quantity,
        cp.unit_price,
        cp.unit_cost,
        CASE 
            WHEN random() < 0.5 THEN 0
            WHEN random() < 0.75 THEN 0.05
            WHEN random() < 0.90 THEN 0.10
            ELSE 0.20
        END as discount_rate,
        CASE (floor(random() * 5)::int)
            WHEN 0 THEN 'Cash'
            WHEN 1 THEN 'Credit Card'
            WHEN 2 THEN 'Debit Card'
            WHEN 3 THEN 'E-Wallet'
            ELSE 'Bank Transfer'
        END as payment_method,
        CASE 
            WHEN random() < 0.5 THEN 'Online'
            WHEN random() < 0.8 THEN 'In-Store'
            ELSE 'Marketplace'
        END as sales_channel
    FROM dates d
    CROSS JOIN generate_series(1, 4) seq
    JOIN LATERAL (
        SELECT * FROM customer_products
        ORDER BY random()
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
