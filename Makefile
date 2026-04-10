.PHONY: build up down logs test clean setup dev

# Build Go binaries
build:
	go build -o api cmd/api/main.go
	go build -o worker cmd/worker/main.go

# Start all services with docker-compose
up:
	docker-compose up -d

# Start only infrastructure services (postgres, rabbitmq, redis, metabase)
infra:
	docker-compose up -d postgres rabbitmq redis metabase

# Stop all services
down:
	docker-compose down

# View logs
logs:
	docker-compose logs -f

# Run tests
test:
	go test -v ./...

# Clean build artifacts and volumes
clean:
	docker-compose down -v
	rm -f api worker

# Download dependencies
deps:
	go mod download
	go mod tidy

# Run API server locally
dev-api:
	go run cmd/api/main.go

# Run worker locally
dev-worker:
	go run cmd/worker/main.go

# Setup development environment
setup:
	cp .env.example .env
	@echo "Please edit .env file with your credentials"

# Database migrations
migrate:
	docker-compose up -d postgres
	sleep 5
	docker-compose exec postgres psql -U analytics -d analytics_db -f /docker-entrypoint-initdb.d/001_create_schema.sql
	docker-compose exec postgres psql -U analytics -d analytics_db -f /docker-entrypoint-initdb.d/002_seed_data_dim.sql
	docker-compose exec postgres psql -U analytics -d analytics_db -f /docker-entrypoint-initdb.d/003_seed_data_facts.sql

# Check database
db-shell:
	docker-compose exec postgres psql -U analytics -d analytics_db

# Reset database (WARNING: destroys all data)
db-reset:
	docker-compose down -v postgres
	docker-compose up -d postgres
