.PHONY: setup dev test build contracts check-contracts

setup:
	cd backend && go mod download
	cd web && npm ci

dev:
	bash scripts/dev.sh

test:
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go test -race ./...
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go vet ./...
	cd web && npm run typecheck

build:
	cd web && npm run build
	mkdir -p .local
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go build -o ../.local/uno-server ./cmd/server

contracts:
	cd backend && GOCACHE="$(CURDIR)/.local/go-cache" go run ./cmd/schema > ../contracts/openapi.json
	cd web && npm run generate

check-contracts:
	bash scripts/check-contracts.sh
