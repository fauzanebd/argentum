#!/bin/bash

# Setup script for Analytics Agent
# This script helps with initial project setup

set -e

echo "=========================================="
echo "Analytics Agent - Setup Script"
echo "=========================================="
echo ""

# Check prerequisites
check_command() {
    if ! command -v $1 &> /dev/null; then
        echo "❌ $1 is not installed. Please install it first."
        return 1
    fi
    echo "✅ $1 is installed"
}

echo "Checking prerequisites..."
check_command docker || exit 1
check_command docker-compose || exit 1
check_command go || exit 1
echo ""

# Create .env file if it doesn't exist
if [ ! -f .env ]; then
    echo "Creating .env file from template..."
    cp .env.example .env
    echo "⚠️  Please edit .env file with your actual credentials:"
    echo "   - LLM_API_KEY (OpenAI API key)"
    echo "   - WHATSAPP_ACCESS_TOKEN"
    echo "   - WHATSAPP_PHONE_NUMBER_ID"
    echo "   - WHATSAPP_APP_SECRET"
    echo "   - WHATSAPP_WEBHOOK_VERIFY_TOKEN"
    echo ""
else
    echo "✅ .env file already exists"
fi

# Download Go dependencies
echo "Downloading Go dependencies..."
go mod download
go mod tidy
echo "✅ Dependencies downloaded"
echo ""

# Create necessary directories
echo "Creating directories..."
mkdir -p logs
echo "✅ Directories created"
echo ""

echo "=========================================="
echo "Setup complete!"
echo "=========================================="
echo ""
echo "Next steps:"
echo "1. Edit .env file with your credentials"
echo "2. Start infrastructure: make infra"
echo "3. Build and run: make build && make up"
echo "4. Or run locally: make dev-api (in one terminal) && make dev-worker (in another)"
echo ""
echo "For more information, see README.md and docs/DEVELOPMENT.md"
