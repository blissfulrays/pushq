GO      ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS  = -s -w -X github.com/blissfulrays/pushq/internal/version.Version=$(VERSION) \
                 -X github.com/blissfulrays/pushq/internal/version.Commit=$(COMMIT)

.PHONY: build
build: web ## Build the pushq binary (with embedded dashboard)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o pushq ./cmd/pushq

.PHONY: build-nofrontend
build-nofrontend: ## Build without rebuilding the dashboard
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o pushq ./cmd/pushq

.PHONY: web
web: ## Build the dashboard (SvelteKit → web/build)
	cd web && npm install --no-audit --no-fund && npm run build

.PHONY: test
test: ## Unit + SQLite conformance + e2e
	$(GO) vet ./...
	$(GO) test -race ./...

.PHONY: test-pg
test-pg: ## Full suite including Postgres (needs PUSHQ_TEST_PG_URL)
	@test -n "$$PUSHQ_TEST_PG_URL" || (echo "set PUSHQ_TEST_PG_URL"; exit 1)
	$(GO) test -race -count=1 ./...

.PHONY: sdk-test-ts
sdk-test-ts: build-nofrontend
	cd sdks/typescript && npm install --no-audit --no-fund && npm run build && \
		PUSHQ_BIN=$(CURDIR)/pushq npm test

.PHONY: sdk-test-py
sdk-test-py: build-nofrontend
	cd sdks/python && python3 -m venv .venv && .venv/bin/pip install -q --upgrade pip && \
		.venv/bin/pip install -q -e ".[dev]" && PUSHQ_BIN=$(CURDIR)/pushq .venv/bin/pytest -q

.PHONY: sdk-test-go
sdk-test-go:
	cd sdks/go && $(GO) vet ./... && $(GO) test ./...

.PHONY: sdk-test-java
sdk-test-java:
	cd sdks/java && mvn -q package

.PHONY: release-dry
release-dry: web ## GoReleaser snapshot build
	goreleaser release --snapshot --clean

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'
