SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

APP         := goforge
CMD_DIR     := ./cmd/api
BIN_DIR     := ./bin
MIGRATE_URL ?= postgres://goforge:goforge@localhost:5432/goforge?sslmode=disable
MIGRATIONS  := ./migrations

## tidy: Sync go.mod / go.sum
.PHONY: tidy
tidy:
	go mod tidy

## fmt: Format sources
.PHONY: fmt
fmt:
	go fmt ./...

## vet: go vet
.PHONY: vet
vet:
	go vet ./...

## lint: golangci-lint (install first: see README)
.PHONY: lint
lint:
	golangci-lint run ./...

## test: run unit tests
.PHONY: test
test:
	go test -race -count=1 ./...

## build: compile statically linked binary into ./bin
.PHONY: build
build:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BIN_DIR)/$(APP) $(CMD_DIR)

## run: run from source with .env
.PHONY: run
run:
	go run $(CMD_DIR)

## migrate-up: apply all pending migrations
.PHONY: migrate-up
migrate-up:
	migrate -path $(MIGRATIONS) -database "$(MIGRATE_URL)" up

## migrate-down: rollback the last migration
.PHONY: migrate-down
migrate-down:
	migrate -path $(MIGRATIONS) -database "$(MIGRATE_URL)" down 1

## migrate-new: create a new migration pair (usage: make migrate-new name=add_orders)
.PHONY: migrate-new
migrate-new:
	@test -n "$(name)" || (echo "usage: make migrate-new name=<snake_case_name>"; exit 1)
	migrate create -ext sql -dir $(MIGRATIONS) -seq $(name)

## up: bring up postgres + api via docker compose
.PHONY: up
up:
	docker compose -f deploy/docker/docker-compose.yml up --build -d

## down: stop compose stack (preserves the pgdata volume)
.PHONY: down
down:
	docker compose -f deploy/docker/docker-compose.yml down

## logs: tail compose logs
.PHONY: logs
logs:
	docker compose -f deploy/docker/docker-compose.yml logs -f

## scaffold: generate a new resource (usage: make scaffold name=Order)
.PHONY: scaffold
scaffold:
	@test -n "$(name)" || (echo "usage: make scaffold name=<Resource>"; exit 1)
	@bash scripts/scaffold.sh $(name)

## sdk-ts: regenerate the TypeScript SDK from a running API (URL=http://localhost:8080)
.PHONY: sdk-ts
sdk-ts:
	go run ./cmd/forge sdk ts $(if $(URL),--url=$(URL),)

## help: show this help
.PHONY: help
help:
	@awk 'BEGIN{FS=":.*##"; printf "Usage: make <target>\n\n"} /^## / {sub(/^## /,""); split($$0,a,": "); printf "  \033[36m%-16s\033[0m %s\n",a[1],a[2]}' $(MAKEFILE_LIST)
