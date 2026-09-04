# Local development commands.

BINARY      := triage-service
CMD         := ./cmd/triage
BUILD_DIR   := bin
BASE_URL    ?= http://localhost:8080
COMPOSE     := docker compose

# Local run defaults. They match the compose service, so `make run` works against
# `make db-up` without any exports.
export PORT       ?= 8080
export DB_HOST    ?= localhost
export DB_PORT    ?= 5432
export DB_USER    ?= postgres
export DB_PASSWORD ?= postgres
export DB_NAME    ?= triage
export DB_SSLMODE ?= disable

.DEFAULT_GOAL := help

## help: list the available targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /' | sort

# ---------------------------------------------------------------------------- build & run

## build: compile the service into bin/
.PHONY: build
build:
	go build -trimpath -o $(BUILD_DIR)/$(BINARY) $(CMD)

## run: run the service locally against DB_HOST (migrations apply on startup)
.PHONY: run
run:
	go run $(CMD)

## clean: remove build artefacts and the test cache
.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)
	go clean -testcache

# ---------------------------------------------------------------------------- quality

## fmt: format the code
.PHONY: fmt
fmt:
	gofmt -w .

## fmt-check: fail if anything is unformatted
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "unformatted files:"; echo "$$unformatted"; exit 1; \
	fi

## vet: run go vet
.PHONY: vet
vet:
	go vet ./...

## test: run the unit tests
.PHONY: test
test:
	go test ./...

## test-race: run the unit tests with the race detector
.PHONY: test-race
test-race:
	go test -race ./...

## test-integration: run repository integration tests against PostgreSQL
.PHONY: test-integration
test-integration: db-up
	@TEST_DATABASE_URL="postgres://$(DB_USER):$(DB_PASSWORD)@$(DB_HOST):$(DB_PORT)/$(DB_NAME)?sslmode=$(DB_SSLMODE)" \
		go test -race -count=1 ./internal/store -v

## tidy: tidy go.mod and go.sum
.PHONY: tidy
tidy:
	go mod tidy

## check: everything CI would run
.PHONY: check
check: fmt-check vet test build

# ---------------------------------------------------------------------------- docker

## up: build and start the full stack (app + postgres)
.PHONY: up
up:
	$(COMPOSE) up -d --build
	@echo "API on $(BASE_URL)"

## down: stop the stack and drop its volume
.PHONY: down
down:
	$(COMPOSE) down -v

## db-up: start only postgres, for running the service from the host
.PHONY: db-up
db-up:
	$(COMPOSE) up -d db

## logs: follow the application logs
.PHONY: logs
logs:
	$(COMPOSE) logs -f app

## ps: show the state of the stack
.PHONY: ps
ps:
	$(COMPOSE) ps

## psql: open a psql shell on the running database
.PHONY: psql
psql:
	$(COMPOSE) exec db psql -U $(DB_USER) -d $(DB_NAME)

# ---------------------------------------------------------------------------- docs generation (swag)
SWAG_VERSION ?= v1.16.6
DOC_DIR ?= docs
SWAG_DIRS ?= cmd/triage,internal/api,internal/apierr
SWAG_MAIN ?= main.go

## docs: generate Swagger documentation into docs/
.PHONY: docs
docs:
	@echo "Generating Swagger documentation into $(DOC_DIR) using Swag $(SWAG_VERSION)"
	@go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init \
		--parseInternal --parseDependency=false --parseDepth=1 --outputTypes json,yaml \
		-d $(SWAG_DIRS) -g $(SWAG_MAIN) -o $(DOC_DIR)
	@echo "Docs generated to $(DOC_DIR)/swagger.json and $(DOC_DIR)/swagger.yaml"
