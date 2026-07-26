# Argentum monorepo — top-level entry points.
#
# Per-app targets live in each app (see apps/backend/Makefile for the Docker
# Compose and demo-tenant seeding targets). This file is the single interface
# agents and CI use, so a task never has to guess which directory to stand in.

BACKEND := apps/backend

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------------------
# Local infrastructure
# ---------------------------------------------------------------------------

.PHONY: infra
infra: ## Start postgres, demo postgres, redis, metabase
	cd $(BACKEND) && docker-compose --profile dev up -d postgres postgres_demo redis metabase

.PHONY: infra-down
infra-down: ## Stop local infrastructure
	cd $(BACKEND) && docker-compose down

.PHONY: seed
seed: ## Apply demo-tenant schema + fixtures (local demo container only)
	cd $(BACKEND) && $(MAKE) migrate

# ---------------------------------------------------------------------------
# Run
# ---------------------------------------------------------------------------

.PHONY: api
api: ## Run the API server (applies control migrations on boot)
	cd $(BACKEND) && go run ./cmd/api

.PHONY: worker
worker: ## Run the agent worker (consumes chat:run, scheduled:run)
	cd $(BACKEND) && go run ./cmd/worker

.PHONY: discord
discord: ## Run the Discord gateway
	cd $(BACKEND) && go run ./cmd/discord

.PHONY: web
web: ## Run the dashboard dev server (:5173)
	pnpm --filter dashboard dev

.PHONY: landing
landing: ## Run the landing dev server
	pnpm --filter landing dev

# ---------------------------------------------------------------------------
# Verify — these are the commands the verification gates expect
# ---------------------------------------------------------------------------

.PHONY: vet
vet: ## go vet the backend
	cd $(BACKEND) && go vet ./...

.PHONY: test
test: ## Backend tests with the race detector
	cd $(BACKEND) && go test -race -count=1 ./...

.PHONY: build
build: ## Build every binary and every workspace package
	cd $(BACKEND) && go build ./...
	pnpm -r build

.PHONY: lint
lint: ## Lint every workspace app (dashboard=eslint, landing=tsc --noEmit)
	pnpm -r lint

.PHONY: check
check: vet test build ## Everything CI runs, locally

# ---------------------------------------------------------------------------
# Agent quality
# ---------------------------------------------------------------------------

.PHONY: eval
eval: ## Score the agent against the golden question set (T-01)
	@test -d $(BACKEND)/testdata/eval || { echo "eval harness not built yet — see docs/plan/01-tickets.md T-01"; exit 1; }
	cd $(BACKEND) && go run ./cmd/eval -set testdata/eval/golden.yaml

.PHONY: types
types: ## Regenerate packages/api-types from Go structs (T-02b)
	@echo "type generation not wired yet — see docs/plan/01-tickets.md T-02b"
	@exit 1

# ---------------------------------------------------------------------------
# Housekeeping
# ---------------------------------------------------------------------------

.PHONY: deps
deps: ## Install all dependencies
	cd $(BACKEND) && go mod download
	pnpm install

.PHONY: migration-next
migration-next: ## Show the last three control migrations, to claim the next number
	@ls $(BACKEND)/migrations/control/ | tail -3
