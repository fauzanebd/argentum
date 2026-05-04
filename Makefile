.PHONY: build up down logs test clean setup dev migrate db-shell

# Build Go binaries
build:
	go build -o api cmd/api/main.go
	go build -o worker cmd/worker/main.go

# Start all services with docker-compose
up:
	docker-compose up -d

# Start only infrastructure services (postgres, redis, metabase)
infra:
	docker-compose up -d postgres redis metabase

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

# Demo warehouse DDL + seed SQL on the Compose postgres service (defaults:
# DB_USER/DATABASE from docker-compose.yml). Scripts live under migrations/demo_tenant;
# main postgres does not mount /docker-entrypoint-initdb.d.
migrate:
	docker-compose up -d postgres
	sleep 5
	docker-compose exec postgres psql -U argentum -d postgres -tc "SELECT 1 FROM pg_database WHERE datname = 'demo_analytics'" | grep -q 1 || docker-compose exec postgres psql -U argentum -d postgres -c "CREATE DATABASE demo_analytics;"
	docker-compose exec -T postgres psql -U argentum -d demo_analytics -v ON_ERROR_STOP=1 < migrations/demo_tenant/001_create_schema.sql
	docker-compose exec -T postgres psql -U argentum -d demo_analytics -v ON_ERROR_STOP=1 < migrations/demo_tenant/002_seed_data_dim.sql
	docker-compose exec -T postgres psql -U argentum -d demo_analytics -v ON_ERROR_STOP=1 < migrations/demo_tenant/003_seed_data_facts.sql
	docker-compose exec -T postgres psql -U argentum -d demo_analytics -v ON_ERROR_STOP=1 < migrations/demo_tenant/004_enable_pgvector.sql
	docker-compose exec -T postgres psql -U argentum -d demo_analytics -v ON_ERROR_STOP=1 < migrations/demo_tenant/005_drop_unused_vector_tables.sql

# Control-plane Postgres shell (same defaults as Compose ${DB_USER:-argentum}/${DB_NAME:-argentum})
db-shell:
	docker-compose exec postgres psql -U argentum -d argentum

# Reset database (WARNING: destroys all data)
db-reset:
	docker-compose down -v postgres
	docker-compose up -d postgres
