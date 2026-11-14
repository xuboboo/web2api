GO ?= go
BINARY ?= web2api
PKGS ?= ./...
GOFILES := $(shell git ls-files '*.go')

.PHONY: build test test-integration lint run fmt

build:
	$(GO) build -o $(BINARY) .

test:
	$(GO) test -race -count=1 $(PKGS)

test-integration:
	WEB2API_INTEGRATION_TESTS=1 $(GO) test -race -count=1 -timeout=120s -tags=integration $(PKGS)

lint:
	$(GO) vet $(PKGS)
	golangci-lint run --config .golangci.yml

run:
	$(GO) run . serve

fmt:
	gofmt -w $(GOFILES)
