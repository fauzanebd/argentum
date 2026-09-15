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
infra: ## Start postgres, demo postgres, redis, minio
	cd $(BACKEND) && docker-compose --profile dev up -d postgres postgres_demo redis minio

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

# GOLANGCI resolves the binary from PATH, then from GOPATH/bin, then from the
# default GOPATH.
#
# The fallback is not belt-and-braces: `go install` puts it in GOPATH/bin, which
# is the way a Go developer most often gets this tool and is NOT on PATH by
# default. .githooks/pre-push calls this target from git's own environment, so on
# 2026-09-11 the hook fired correctly, failed to find a golangci-lint that was
# installed, and blocked a push it should have passed. A guard that reports a
# missing tool it could have found is a guard people disable.
#
# **It blocked a second push the same day**, for the next link in the same
# chain: asking `go env GOPATH` assumes `go` is on PATH, and in git's environment
# it was not either — the tarball install lives in /usr/local/go/bin. The
# message even said so, printing "Looked on PATH and in /bin" because the
# substitution it interpolated had returned nothing. So `go` is now resolved the
# same way golangci-lint is, and $$HOME/go/bin is tried last, because that is
# where GOPATH points when nothing has set it. The recipe then puts that same
# toolchain on PATH for golangci-lint's own child processes — see below.
GO := $(shell command -v go 2>/dev/null || ls /usr/local/go/bin/go 2>/dev/null)
GOPATH_BIN := $(shell [ -n "$(GO)" ] && $(GO) env GOPATH 2>/dev/null || echo $$HOME/go)/bin
GOLANGCI := $(shell command -v golangci-lint 2>/dev/null || \
	ls $(GOPATH_BIN)/golangci-lint 2>/dev/null || \
	ls $$HOME/go/bin/golangci-lint 2>/dev/null)

.PHONY: lint-go
lint-go: ## golangci-lint the backend (config: apps/backend/.golangci.yml)
	@test -n "$(GOLANGCI)" || { \
		echo "golangci-lint is not installed. CI pins the version, so match it:"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2"; \
		echo "  # or: brew install golangci-lint — https://golangci-lint.run/welcome/install/"; \
		echo "Looked on PATH, in $(GOPATH_BIN), and in $$HOME/go/bin."; \
		exit 1; \
	}
	@# golangci-lint shells out to `go` for the package graph, so finding the
	@# linter is only half of it: the toolchain has to be on PATH for the child
	@# too. Prepending rather than replacing, so a developer whose own PATH is
	@# already right is unaffected.
	cd $(BACKEND) && PATH="$(dir $(GO)):$$PATH" $(GOLANGCI) run ./...

.PHONY: lint-web
lint-web: ## Lint every workspace app (dashboard=tsc + eslint + vitest, landing=tsc --noEmit)
	pnpm -r lint

.PHONY: test-web
test-web: ## Frontend unit tests (vitest, apps/dashboard)
	pnpm --filter dashboard test

.PHONY: lint
lint: lint-go lint-web ## Lint the backend and every workspace app

.PHONY: check
check: vet lint test build ## Everything CI runs, locally

.PHONY: hooks
hooks: ## Enable .githooks (pre-push runs lint-go when a push touches Go)
	git config core.hooksPath .githooks
	@echo "core.hooksPath = .githooks — skip a single push with: git push --no-verify"

# ---------------------------------------------------------------------------
# Agent quality
# ---------------------------------------------------------------------------

.PHONY: eval
eval: ## Score the agent against the golden question set (T-01)
	cd $(BACKEND) && go run ./cmd/eval -set testdata/eval/golden.yaml $(EVAL_ARGS)

.PHONY: eval-matrix
eval-matrix: ## Score the set across several models and print the comparison (T-Q5). MODELS=a,b
	@test -n "$(MODELS)" || (echo "set MODELS=model-a,model-b" && exit 1)
	cd $(BACKEND) && go run ./cmd/eval -set testdata/eval/golden.yaml -models "$(MODELS)" $(EVAL_ARGS)

.PHONY: eval-dry
eval-dry: ## Validate the golden set and seed the eval tenant without calling the LLM
	cd $(BACKEND) && go run ./cmd/eval -set testdata/eval/golden.yaml -dry-run

# Its own target and its own set file (T-H11). The five adversarial cases are
# scored against a third source carrying injected rows, identifiers and column
# comments, which the harness registers only for a run that holds one of them —
# so running them through `eval` above would change what every golden case
# sees. Read the result as named defects, not as a percentage.
.PHONY: eval-security
eval-security: ## Score the agent against the adversarial security set (T-H11)
	cd $(BACKEND) && go run ./cmd/eval -set testdata/eval/security.yaml $(EVAL_ARGS)

# The speech set (T-W7's live arm; research 08 §6). The set is a reading script:
# someone at the pilot records each line as <id>.<ext>, and the recordings stay
# outside the tree. CLIPS must be absolute — the recipe runs from $(BACKEND).
.PHONY: eval-speech
eval-speech: ## Score speech providers on Indonesian questions read aloud (T-W7). CLIPS=/abs/dir; OPENROUTER_API_KEY, GROQ_API_KEY and/or OPENAI_API_KEY
	@test -n "$(CLIPS)" || (echo "set CLIPS=/absolute/path/to/recordings — one <id>.<ext> per line of $(BACKEND)/testdata/eval/speech.yaml" && exit 1)
	cd $(BACKEND) && go run ./cmd/evalspeech -set testdata/eval/speech.yaml -clips "$(CLIPS)" -out eval-speech-report.md $(EVAL_ARGS)

.PHONY: eval-speech-dry
eval-speech-dry: ## Check the speech set against its own text, and CLIPS for missing recordings, calling no provider
	cd $(BACKEND) && go run ./cmd/evalspeech -set testdata/eval/speech.yaml -dry-run $(if $(CLIPS),-clips "$(CLIPS)")

# Key rotation (T-H14). The full procedure is in cmd/rekey's package comment and
# in docs/coverage/security-hardening.md; these are steps 2 and 3 of it.
.PHONY: rekey-check
rekey-check: ## Report which key each stored DSN is sealed under (T-H14)
	cd $(BACKEND) && go run ./cmd/rekey -check

.PHONY: rekey-apply
rekey-apply: ## Re-seal stored DSNs under the primary ARGENTUM_DSN_KEY (T-H14)
	cd $(BACKEND) && go run ./cmd/rekey -apply

.PHONY: types
types: ## Regenerate packages/api-types from Go structs (T-02b)
	node packages/api-types/scripts/generate.mjs

.PHONY: types-check
types-check: ## Verify packages/api-types matches the Go structs, writing nothing
	node packages/api-types/scripts/generate.mjs --check

# ---------------------------------------------------------------------------
# The published API contract (T-A4)
# ---------------------------------------------------------------------------

.PHONY: openapi
openapi: ## Validate apps/backend/openapi/v1.yaml and regenerate everything from it
	pnpm --filter @argentum/openapi-tools build
	pnpm --filter @argentum/sdk build

.PHONY: openapi-check
openapi-check: ## Verify the generated API artifacts match the spec, writing nothing
	pnpm --filter @argentum/openapi-tools check
	pnpm --filter @argentum/sdk types-check

.PHONY: api-examples
api-examples: ## Run every published sample against a live API (see docs/api/examples/run.sh)
	./docs/api/examples/run.sh $(EXAMPLES_MODE)

EXAMPLES_MODE ?= deterministic

# ---------------------------------------------------------------------------
# Design system
# ---------------------------------------------------------------------------

.PHONY: tokens
tokens: ## Regenerate the dashboard CSS variables and the Go report theme (T-R1)
	node packages/design-tokens/scripts/generate.mjs
	@unformatted=$$(cd $(BACKEND) && gofmt -l internal/report/theme); \
	if [ -n "$$unformatted" ]; then \
		echo "generated Go is not gofmt-clean: $$unformatted"; \
		echo "fix packages/design-tokens/scripts/gen-go.mjs, not the output"; \
		exit 1; \
	fi

.PHONY: tokens-check
tokens-check: palette motion-guards ## Verify the generated token files match tokens.json, writing nothing
	node packages/design-tokens/scripts/generate.mjs --check

.PHONY: palette
palette: ## Verify the chart palette in greyscale and under simulated CVD (T-R3)
	node packages/design-tokens/scripts/palette.mjs --check

.PHONY: motion-guards
motion-guards: ## Verify packages/motion names no colour of its own (T-V5)
	node packages/design-tokens/scripts/motion-guards.mjs --check

# ---------------------------------------------------------------------------
# Report artifacts
# ---------------------------------------------------------------------------

DECK_OUT ?= /tmp/argentum-decks

.PHONY: decks
decks: ## Render the fixture decks for the four-application check (T-R4)
	cd $(BACKEND) && ARGENTUM_DECK_OUT=$(DECK_OUT) go test -count=1 -v -run TestWriteDecks ./internal/report/pptx/
	@echo "open $(DECK_OUT) in PowerPoint, Keynote, Google Slides and LibreOffice"

.PHONY: decks-check
decks-check: ## Convert every fixture deck through headless LibreOffice (T-R4)
	@command -v soffice >/dev/null 2>&1 || { \
		echo "libreoffice is not installed — CI runs this, or:"; \
		echo "  docker run --rm -v \$$PWD:/w -w /w debian:bookworm-slim \\"; \
		echo "    bash -c 'apt-get update -qq && apt-get install -y -qq --no-install-recommends libreoffice-impress && soffice --headless --convert-to pdf *.pptx'"; \
		exit 1; \
	}
	cd $(BACKEND) && go test -count=1 -v -run TestLibreOfficeConverts ./internal/report/pptx/

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
