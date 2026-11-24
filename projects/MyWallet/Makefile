.PHONY: all build test test-go test-kit run-dev lint clean help

all: build

build: ## Build the Go server binary
	cd backend && go build -o ../bin/ ./cmd/...

test: test-go test-kit ## Run every automated suite

test-go: ## Go vet + tests with the race detector (embedded PostgreSQL)
	cd backend && go vet ./... && go test ./... -race -count=1

test-kit: ## MyWalletKit tests (Swift Testing). Works with Xcode or Command Line Tools.
	./scripts/kit-test.sh

run-dev: ## Run the API locally with the FAKE provider, dev sign-in, and embedded PostgreSQL
	cd backend && PROVIDER=fake DEV_SIGN_IN=1 LISTEN_ADDR=127.0.0.1:8080 go run ./cmd/server --dev-embedded-postgres

smoke: ## Exercise a running dev server end to end with curl
	./scripts/smoke.sh

clean:
	rm -rf bin backend/bin ios/MyWalletKit/.build

help:
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-10s %s\n", $$1, $$2}'
