.PHONY: setup dev test build contracts check-contracts

setup:
	cd backend && go mod download
	cd web && npm ci

dev:
	bash scripts/dev.sh

test:
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go test -race -v ./...
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go vet ./...
	cd web && npm run typecheck
	cd web && npm test

build:
	cd web && npm run build
	mkdir -p .local
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go build -o ../.local/recurring-server ./cmd/server
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go build -o ../.local/recurring-worker ./cmd/worker

contracts:
	mkdir -p contracts
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go run ./cmd/schema > ../contracts/openapi.json

check-contracts:
	bash scripts/check-contracts.sh
