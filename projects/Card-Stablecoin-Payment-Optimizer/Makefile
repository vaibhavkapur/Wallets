# Card-versus-Stablecoin Payment Optimizer
#
#   make test        every suite: Rust (unit, golden, property), Python reference check, Go (real engine + embedded PostgreSQL), TypeScript
#   make up          local stack in assisted mode (embedded PostgreSQL, engine, API on :8080); make up-shadow for shadow mode
#   make e2e         end-to-end scenario against the running stack
#   make web         comparison UI at http://localhost:5173
#   make down        stop the stack

SHELL := /bin/bash
ROOT  := $(abspath .)

.PHONY: test test-rust test-fixtures test-go test-ts engine up up-shadow down e2e web offline-eval \
        schema fixtures test-report fmt install

install:
	cd web && npm install --no-audit --no-fund

test: test-rust test-fixtures test-go test-ts

test-rust:
	cd engine && cargo clippy --workspace --all-targets -- -D warnings && cargo test --workspace

test-fixtures:
	python3 fixtures/tools/independent_check.py fixtures/golden/v1
	python3 fixtures/tools/independent_check.py fixtures/recorded/v1

test-go: engine
	cd orchestrator && go vet ./... && go test ./... -race -count=1

test-ts:
	cd web && npm run typecheck && npm test

engine:
	cd engine && cargo build -q -p optimizer-service

up:
	scripts/dev-up.sh assisted

up-shadow:
	scripts/dev-up.sh shadow

down:
	scripts/dev-down.sh

e2e:
	scripts/e2e.sh

web:
	cd web && npm run dev

# Milestone O1 against the engine started by `make up`.
offline-eval:
	ENGINE_AUTH_TOKEN=dev-engine-token-0123456789abcdef .dev/bin/offline-eval -dataset $(ROOT)/fixtures/recorded/v1 \
	  -engine-url http://$$(grep -o 'listening on 127.0.0.1:[0-9]*' .dev/devstack.log | head -1 | sed 's/listening on //')

# Regenerate the engine JSON Schema and the TypeScript types from both contracts.
schema: engine
	cd engine && cargo run -q -p optimizer-service -- schema > ../contracts/engine-v1.schema.json
	cd web && npm run generate

# Rebuild the golden vectors and the recorded dataset, then check them independently.
fixtures:
	python3 fixtures/tools/build_golden.py
	python3 fixtures/tools/build_recorded.py
	$(MAKE) test-fixtures

# Run every suite and the e2e scenario (stack must be up) and rewrite docs/test-report.md.
test-report:
	scripts/test-report.sh

fmt:
	cd engine && cargo fmt
	cd orchestrator && gofmt -w .
