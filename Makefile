# Recipes use POSIX shell. On Windows that comes from Git for Windows.
COMPOSE := docker compose
ifeq ($(OS),Windows_NT)
SHELL := C:/Program Files/Git/bin/bash.exe
EXE := .exe
# Docker's build permission check compares paths case-sensitively, so builds
# fail from a terminal opened at d: rather than D:. pwd -W gives the real case.
COMPOSE := cd "$$(pwd -W)" && docker compose
endif

# Pinned tool versions. Delete bin/ after changing one so it gets rebuilt.
SQLC_VERSION := v1.31.1
GOOSE_VERSION := v3.29.0
OAPI_CODEGEN_VERSION := v2.8.2
GOLANGCI_LINT_VERSION := v2.15.0

BIN := $(CURDIR)/bin
SQLC := $(BIN)/sqlc$(EXE)
GOOSE := $(BIN)/goose$(EXE)
OAPI_CODEGEN := $(BIN)/oapi-codegen$(EXE)
GOLANGCI_LINT := $(BIN)/golangci-lint$(EXE)
WEB_DEPS := web/node_modules/.modules.yaml

.PHONY: help tools generate check-generated build web-build lint test run run-web dev-web env up down migration clean

help: ## List the available targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sed -E 's/:.*## /\t/'

$(SQLC):
	GOBIN=$(BIN) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
$(GOOSE):
	GOBIN=$(BIN) go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
$(OAPI_CODEGEN):
	GOBIN=$(BIN) go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)
$(GOLANGCI_LINT):
	GOBIN=$(BIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(WEB_DEPS): web/package.json web/pnpm-lock.yaml
	cd web && pnpm install --frozen-lockfile
	touch $@

tools: $(SQLC) $(GOOSE) $(OAPI_CODEGEN) $(GOLANGCI_LINT) ## Install the pinned Go tools into bin/

generate: $(SQLC) $(OAPI_CODEGEN) ## Regenerate code from the SQL queries and openapi.yaml
	$(SQLC) generate
	$(OAPI_CODEGEN) -config internal/httpx/apigen/config.yaml api/openapi.yaml

check-generated: generate ## Fail if generated code is out of date
	git diff --exit-code -- internal/db/dbgen internal/httpx/apigen

web-build: $(WEB_DEPS) ## Type-check and build the frontend into web/dist
	cd web && pnpm build

build: web-build ## Build the frontend into web/dist and the Go server into bin/
	CGO_ENABLED=0 go build -trimpath -o $(BIN)/dextro$(EXE) ./cmd/server

lint: $(GOLANGCI_LINT) $(WEB_DEPS) ## Lint Go and TypeScript, and type-check the frontend
	$(GOLANGCI_LINT) run ./...
	cd web && pnpm lint
	cd web && pnpm typecheck

test: ## Run the Go and web server tests (needs Docker for the Postgres tests)
	go test ./...
	cd web && pnpm test

run: ## Run the Go server locally; set DEXTRO_DATABASE_URL, DEXTRO_PUBLIC_URL and DEXTRO_ENCRYPTION_KEYS first
	go run ./cmd/server

run-web: web-build ## Run the Node web server locally on port 3000
	cd web && pnpm start

dev-web: $(WEB_DEPS) ## Run the Vite dev server, proxying the API to localhost:8080
	cd web && pnpm dev

env: ## Create .env with random secrets, or add any missing from an existing one
	@if [ -f .env ]; then echo ".env already exists"; else \
		printf 'POSTGRES_PASSWORD=%s\nS3_ACCESS_KEY=%s\nS3_SECRET_KEY=%s\n' \
			"$$(openssl rand -hex 24)" "$$(openssl rand -hex 10)" "$$(openssl rand -hex 24)" > .env; \
		echo "created .env"; \
	fi

up: env ## Build and start the whole stack
	$(COMPOSE) up --build

down: ## Stop the stack and keep its data
	$(COMPOSE) down

migration: $(GOOSE) ## Create a migration: make migration name=add_users
	$(GOOSE) -dir internal/db/migrations create $(name) sql

clean: ## Remove build output, keeping the tools in bin/
	rm -rf $(BIN)/dextro$(EXE) web/dist
