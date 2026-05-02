-- Migration: Create star schema for retail analytics
-- Tables: dim_date, dim_customers, dim_products, fact_sales

-- Create metabase_app database for Metabase internal use
CREATE DATABASE metabase_app;

-- Connect to demo_analytics for creating tables
\c demo_analytics;

-- Dimension Table: Date/Time
CREATE TABLE IF NOT EXISTS dim_date (
    date_id SERIAL PRIMARY KEY,
    full_date DATE NOT NULL UNIQUE,
    day_of_week INTEGER NOT NULL,
    day_name VARCHAR(10) NOT NULL,
    day_of_month INTEGER NOT NULL,
    day_of_year INTEGER NOT NULL,
    week_of_year INTEGER NOT NULL,
    month_number INTEGER NOT NULL,
    month_name VARCHAR(10) NOT NULL,
    quarter INTEGER NOT NULL,
    year INTEGER NOT NULL,
    fiscal_quarter INTEGER NOT NULL,
    is_weekend BOOLEAN NOT NULL,
    is_holiday BOOLEAN DEFAULT FALSE
);

-- Dimension Table: Customers
CREATE TABLE IF NOT EXISTS dim_customers (
    customer_id SERIAL PRIMARY KEY,
    customer_key VARCHAR(50) NOT NULL UNIQUE,
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100) NOT NULL,
    email VARCHAR(255),
    phone VARCHAR(20),
    city VARCHAR(100) NOT NULL,
    state VARCHAR(100),
    country VARCHAR(100) DEFAULT 'Indonesia',
    registration_date DATE NOT NULL,
    customer_segment VARCHAR(50) DEFAULT 'Regular',
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Dimension Table: Products
CREATE TABLE IF NOT EXISTS dim_products (
    product_id SERIAL PRIMARY KEY,
    product_key VARCHAR(50) NOT NULL UNIQUE,
    product_name VARCHAR(255) NOT NULL,
    product_description TEXT,
    category VARCHAR(100) NOT NULL,
    subcategory VARCHAR(100),
    brand VARCHAR(100),
    supplier VARCHAR(100),
    unit_cost DECIMAL(10,2),
    unit_price DECIMAL(10,2) NOT NULL,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Fact Table: Sales
CREATE TABLE IF NOT EXISTS fact_sales (
    sale_id SERIAL PRIMARY KEY,
    transaction_id VARCHAR(50) NOT NULL,
    date_id INTEGER NOT NULL REFERENCES dim_date(date_id),
    customer_id INTEGER NOT NULL REFERENCES dim_customers(customer_id),
    product_id INTEGER NOT NULL REFERENCES dim_products(product_id),
    quantity INTEGER NOT NULL,
    unit_price DECIMAL(10,2) NOT NULL,
    discount_amount DECIMAL(10,2) DEFAULT 0,
    sales_amount DECIMAL(10,2) NOT NULL,
    cost_amount DECIMAL(10,2) NOT NULL,
    profit_amount DECIMAL(10,2) NOT NULL,
    payment_method VARCHAR(50),
    sales_channel VARCHAR(50) DEFAULT 'In-Store',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for better query performance
CREATE INDEX idx_sales_date ON fact_sales(date_id);
CREATE INDEX idx_sales_customer ON fact_sales(customer_id);
CREATE INDEX idx_sales_product ON fact_sales(product_id);
CREATE INDEX idx_sales_transaction ON fact_sales(transaction_id);
CREATE INDEX idx_customers_city ON dim_customers(city);
CREATE INDEX idx_customers_segment ON dim_customers(customer_segment);
CREATE INDEX idx_products_category ON dim_products(category);
CREATE INDEX idx_date_year_month ON dim_date(year, month_number);

-- Create a read-only user for the agent
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'analytics_reader') THEN
        CREATE USER analytics_reader WITH PASSWORD 'reader123';
    END IF;
END
$$;

-- Grant read-only permissions
GRANT USAGE ON SCHEMA public TO analytics_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO analytics_reader;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO analytics_reader;

-- Grant sequence usage for dimension tables (needed for joins)
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO analytics_reader;
