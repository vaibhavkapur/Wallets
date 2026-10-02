.PHONY: all build test test-go test-rust test-ts e2e up down lint clean

all: build

build: ## Build Go binaries, the Rust gateway, and TypeScript packages
	cd control-plane && go build -o ../bin/ ./cmd/...
	cd gateway && cargo build
	npm run build

test: test-go test-rust test-ts ## Run every test suite

test-go: ## Go tests with the race detector (embedded PostgreSQL)
	cd control-plane && go vet ./... && go test ./... -race -count=1

test-rust: ## Rust unit + property tests, clippy
	cd gateway && cargo clippy --all-targets -- -D warnings && cargo test

test-ts: ## TypeScript typecheck + unit/contract tests
	npm run build -w sdk && npm run typecheck && npm test

up: ## Start the full local stack (embedded Postgres, mocks, gateway) in the background
	./scripts/dev-up.sh --daemon

down: ## Stop the local stack
	./scripts/dev-down.sh

e2e: ## Run the end-to-end adversarial scenario against a running stack
	./scripts/e2e.sh

live-sdk: ## SDK live contract test against a running stack
	ASA_LIVE=1 npx vitest run --root sdk src/live.test.ts

example: ## Run the example research agent against a running stack
	npm start -w examples/research-agent

dashboard: ## Start the owner dashboard dev server
	npm run dev -w dashboard

test-report: ## Run all suites + e2e (stack must be up) and rewrite docs/test-report.md
	./scripts/test-report.sh

clean:
	rm -rf bin .dev sdk/dist mcp/dist examples/research-agent/dist dashboard/dist
	cd gateway && cargo clean

help:
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
