# ViaFrei API — developer entry points. Every target is what CI runs.

GO        ?= go
BIN       := bin/viafrei-api
VERSION   ?= 0.2.0-dev
COMMIT    ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
LDFLAGS   := -s -w \
	-X github.com/mavrovde/viafrei-api/internal/buildinfo.version=$(VERSION) \
	-X github.com/mavrovde/viafrei-api/internal/buildinfo.commit=$(COMMIT)

.PHONY: build test test-integration coverage lint generate check-generated fixtures fixtures-verify catalogue report run clean \
	image image-multiarch up down logs reset compose-test

build: ## Build the binary into bin/
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/viafrei-api

test: ## Unit and handler tests (no container runtime needed)
	$(GO) test -race -shuffle=on -count=1 ./...

test-integration: ## Integration tests against a throwaway PostGIS (testcontainers)
	$(GO) test -race -count=1 -tags integration ./...

coverage: ## Unit + integration + boot profiles merged, gated at 100 % per package (TST-001)
	$(GO) test -count=1 -covermode=atomic -coverpkg=./... -coverprofile=coverage-unit.out ./...
	$(GO) test -count=1 -tags integration -covermode=atomic -coverpkg=./... -coverprofile=coverage-integration.out ./...
	scripts/boot-coverage.sh coverage-boot.out
	$(GO) run ./internal/testkit/covergate -min 100 -exclusions test/coverage-exclusions.txt -o coverage.out \
		coverage-unit.out coverage-integration.out coverage-boot.out

lint: ## golangci-lint with the repository configuration
	golangci-lint run ./...

generate: ## Regenerate sqlc code, docs/configuration.md and api/openapi.json
	$(SQLC) generate
	$(GO) run ./cmd/viafrei-api config docs > docs/configuration.md
	$(GO) run ./cmd/viafrei-api openapi > api/openapi.json

check-generated: generate ## Fail when a generated file differs from the committed one
	git diff --exit-code -- internal/adapters/store/contractdb docs/configuration.md api/openapi.json

SQLC ?= sqlc
PROFILE ?= tiny
SEED    ?= 1

fixtures: ## Write a synthetic fixture set: make fixtures PROFILE=ci SEED=7 (into testdata/fixtures/PROFILE)
	$(GO) run ./cmd/fixturegen -profile $(PROFILE) -seed $(SEED) -out testdata/fixtures/$(PROFILE)

fixtures-verify: ## Regenerate every recorded set and compare it with test/fixtures/checksums.tsv (TST-014)
	$(GO) run ./cmd/fixturegen -verify test/fixtures/checksums.tsv

WIKI ?= ../viafrei-api.wiki

catalogue: ## Regenerate test/requirements/catalogue.tsv from a wiki checkout: make catalogue WIKI=path
	$(GO) run ./cmd/reqreport catalogue -wiki $(WIKI) > test/requirements/catalogue.tsv.new
	mv test/requirements/catalogue.tsv.new test/requirements/catalogue.tsv

report: ## Requirement report over local unit results: make report (writes requirement-report.md)
	$(GO) test -count=1 -json ./... > test-unit.json
	$(GO) run ./cmd/reqreport check -scope $$(find test/requirements/scope -name '*.txt' | sort -V | tail -n 1) -results unit=test-unit.json -report requirement-report.md

run: build ## Run against VFAPI_DB_URL from the environment
	$(BIN) serve

# ---- container image and the local compose stack (docs/container.md)

IMAGE     ?= viafrei-api:local
PLATFORMS ?= linux/amd64,linux/arm64

image: ## Build the runtime image for this machine's platform
	docker buildx build --load --target runtime --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

image-multiarch: ## Build the runtime image for amd64 and arm64 (not loaded, not pushed)
	docker buildx build --platform $(PLATFORMS) --target runtime --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) .

up: ## Build and start the API and the development database; wait until both are healthy
	scripts/compose-guard.sh
	VERSION=$(VERSION) COMMIT=$(COMMIT) docker compose up --detach --build --wait

down: ## Stop the stack and keep the database volume
	docker compose down

logs: ## Follow the stack's logs
	docker compose logs --follow

reset: ## Stop the stack and delete this project's database volume (fixtures reload on the next up)
	docker compose down --volumes --remove-orphans

compose-test: ## Smoke-test the stack: healthz, readyz, meta and a short Schemathesis pass, then tear down
	scripts/compose-smoke.sh

clean:
	rm -rf bin
