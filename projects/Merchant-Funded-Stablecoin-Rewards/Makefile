.PHONY: setup dev test build contracts check-contracts reconciler

setup:
	cd backend && go mod download
	cd web && npm ci

dev:
	bash scripts/dev.sh

test:
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go test -race ./...
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go vet ./...
	cd reconciler && cargo test && cargo clippy --all-targets -- -D warnings
	cd web && npm run typecheck && npm test

build:
	cd web && npm run build
	mkdir -p .local
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go build -o ../.local/rewards-server ./cmd/server
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go build -o ../.local/rewards-worker ./cmd/worker
	cd reconciler && cargo build --release

contracts:
	mkdir -p contracts
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go run ./cmd/schema > ../contracts/openapi.json

check-contracts:
	bash scripts/check-contracts.sh

reconciler:
	cd reconciler && cargo run -- ../fixtures/statement_batch_input.json
