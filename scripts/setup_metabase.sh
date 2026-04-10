#!/bin/bash

# Metabase Setup Script
# This script configures Metabase with the analytics database

set -e

echo "=========================================="
echo "Metabase Auto-Setup Script"
echo "=========================================="
echo ""

METABASE_URL="${METABASE_URL:-http://localhost:3000}"
DB_HOST="${DB_HOST:-postgres}"
DB_PORT="${DB_PORT:-5432}"
DB_NAME="${DB_NAME:-analytics_db}"
DB_USER="${DB_USER:-analytics}"
DB_PASSWORD="${DB_PASSWORD:-analytics123}"
ADMIN_EMAIL="${METABASE_ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${METABASE_ADMIN_PASSWORD:-Metabase123!}"

echo "Configuration:"
echo "  Metabase URL: $METABASE_URL"
echo "  Database: $DB_NAME@$DB_HOST:$DB_PORT"
echo ""

# Wait for Metabase to be ready
echo "Waiting for Metabase to be ready..."
for i in {1..30}; do
    if curl -s "$METABASE_URL/api/health" > /dev/null 2>&1; then
        echo "✅ Metabase is ready"
        break
    fi
    echo "  Attempt $i/30..."
    sleep 5
done

# Check if Metabase is already set up
if curl -s "$METABASE_URL/api/session/properties" | grep -q '"setup-token":null'; then
    echo "✅ Metabase is already initialized"
else
    echo "Setting up Metabase..."
    
    # Get setup token
    SETUP_TOKEN=$(curl -s "$METABASE_URL/api/session/properties" | grep -o '"setup-token":"[^"]*"' | cut -d'"' -f4)
    
    if [ -z "$SETUP_TOKEN" ] || [ "$SETUP_TOKEN" == "null" ]; then
        echo "❌ Could not get setup token"
        exit 1
    fi
    
    echo "  Got setup token"
    
    # Complete setup
    curl -s -X POST "$METABASE_URL/api/setup" \
        -H "Content-Type: application/json" \
        -d "{
            \"token\": \"$SETUP_TOKEN\",
            \"user\": {
                \"email\": \"$ADMIN_EMAIL\",
                \"password\": \"$ADMIN_PASSWORD\",
                \"first_name\": \"Admin\",
                \"last_name\": \"User\"
            },
            \"prefs\": {
                \"site_name\": \"Analytics Agent\",
                \"site_locale\": \"en\",
                \"allow_tracking\": false
            }
        }" > /dev/null
    
    echo "✅ Metabase setup completed"
fi

# Authenticate and get session token
echo ""
echo "Authenticating with Metabase..."
SESSION_RESPONSE=$(curl -s -X POST "$METABASE_URL/api/session" \
    -H "Content-Type: application/json" \
    -d "{
        \"username\": \"$ADMIN_EMAIL\",
        \"password\": \"$ADMIN_PASSWORD\"
    }")

SESSION_TOKEN=$(echo "$SESSION_RESPONSE" | grep -o '"id":"[^"]*"' | cut -d'"' -f4)

if [ -z "$SESSION_TOKEN" ]; then
    echo "❌ Authentication failed"
    echo "Response: $SESSION_RESPONSE"
    exit 1
fi

echo "✅ Authenticated successfully"

# Check if database already exists
echo ""
echo "Checking for existing database connection..."
EXISTING_DB=$(curl -s "$METABASE_URL/api/database" \
    -H "X-Metabase-Session: $SESSION_TOKEN" | \
    grep -o '"name":"[^"]*"' | \
    grep -c "Analytics DB" || true)

if [ "$EXISTING_DB" -gt 0 ]; then
    echo "✅ Database connection already exists"
else
    echo "Creating database connection..."
    
    curl -s -X POST "$METABASE_URL/api/database" \
        -H "Content-Type: application/json" \
        -H "X-Metabase-Session: $SESSION_TOKEN" \
        -d "{
            \"engine\": \"postgres\",
            \"name\": \"Analytics DB\",
            \"details\": {
                \"host\": \"$DB_HOST\",
                \"port\": $DB_PORT,
                \"dbname\": \"$DB_NAME\",
                \"user\": \"$DB_USER\",
                \"password\": \"$DB_PASSWORD\",
                \"ssl\": false,
                \"tunnel-enabled\": false
            },
            \"is_full_sync\": true,
            \"is_on_demand\": false
        }" > /dev/null
    
    echo "✅ Database connection created"
fi

echo ""
echo "=========================================="
echo "Metabase Setup Complete!"
echo "=========================================="
echo ""
echo "Access Metabase at: $METABASE_URL"
echo "Login: $ADMIN_EMAIL"
echo ""
echo "The analytics database has been connected."
echo "You can now create dashboards and questions."
