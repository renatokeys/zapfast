SHELL := /usr/bin/env bash

MODULE  := github.com/renatokeys/zapfast
APP     := zapfast

VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILDDATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
    -X 'main.version=$(VERSION)' \
    -X 'main.commit=$(COMMIT)' \
    -X 'main.buildDate=$(BUILDDATE)'

GO        := go
GOFLAGS   := -trimpath
DOCKER    ?= docker
IMAGE     ?= ghcr.io/renatokeys/$(APP)
IMAGE_TAG ?= $(VERSION)

PKGS := $(shell $(GO) list ./... 2>/dev/null)

.PHONY: help build build-api build-worker run test test-unit test-integration \
        lint vet tidy mocks docker-build docker-push fmt clean

help:
	@echo "Targets:"
	@echo "  build            - build api + worker binaries into ./bin/"
	@echo "  build-api        - build api binary"
	@echo "  build-worker     - build worker binary"
	@echo "  run              - go run the api"
	@echo "  test             - go test ./..."
	@echo "  test-unit        - unit tests only (short)"
	@echo "  test-integration - integration tests under ./test/integration"
	@echo "  lint             - golangci-lint run"
	@echo "  vet              - go vet ./..."
	@echo "  tidy             - go mod tidy"
	@echo "  mocks            - regenerate mockery mocks (placeholder)"
	@echo "  fmt              - gofmt -w ."
	@echo "  docker-build     - build container image"
	@echo "  docker-push      - push container image"
	@echo "  clean            - remove ./bin/"

build: build-api build-worker

build-api:
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o bin/$(APP) ./cmd/api

build-worker:
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o bin/$(APP)-worker ./cmd/worker

run:
	$(GO) run ./cmd/api

test:
	$(GO) test $(GOFLAGS) -race -count=1 ./...

test-unit:
	$(GO) test $(GOFLAGS) -race -count=1 -short ./...

test-integration:
	$(GO) test $(GOFLAGS) -race -count=1 -tags=integration ./test/integration/...

lint:
	golangci-lint run ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

mocks:
	@echo "mockery placeholder — wire up once internal/ports exists"

fmt:
	gofmt -s -w .

docker-build:
	$(DOCKER) build \
	    --build-arg VERSION=$(VERSION) \
	    --build-arg COMMIT=$(COMMIT) \
	    --build-arg BUILDDATE=$(BUILDDATE) \
	    -t $(IMAGE):$(IMAGE_TAG) \
	    -t $(IMAGE):latest \
	    .

docker-push:
	$(DOCKER) push $(IMAGE):$(IMAGE_TAG)
	$(DOCKER) push $(IMAGE):latest

clean:
	rm -rf ./bin
