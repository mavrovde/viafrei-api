# ViaFrei API — developer entry points. Every target is what CI runs.

GO        ?= go
BIN       := bin/viafrei-api
VERSION   ?= 0.2.0-dev
COMMIT    ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
LDFLAGS   := -s -w \
	-X github.com/mavrovde/viafrei-api/internal/buildinfo.version=$(VERSION) \
	-X github.com/mavrovde/viafrei-api/internal/buildinfo.commit=$(COMMIT)

.PHONY: build test test-integration lint generate check-generated run clean

build: ## Build the binary into bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/viafrei-api

test: ## Unit and handler tests (no container runtime needed)
	$(GO) test -race -shuffle=on -count=1 ./...

test-integration: ## Integration tests against a throwaway PostGIS (testcontainers)
	$(GO) test -race -count=1 -tags integration ./...

lint: ## golangci-lint with the repository configuration
	golangci-lint run ./...

generate: ## Regenerate sqlc code, docs/configuration.md and api/openapi.json
	sqlc generate
	$(GO) run ./cmd/viafrei-api config docs > docs/configuration.md
	$(GO) run ./cmd/viafrei-api openapi > api/openapi.json

check-generated: generate ## Fail when a generated file differs from the committed one
	git diff --exit-code -- internal/adapters/store/contractdb docs/configuration.md api/openapi.json

run: build ## Run against VFAPI_DB_URL from the environment
	$(BIN) serve

clean:
	rm -rf bin
