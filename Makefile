BINARY     := projector
MODULE     := github.com/akyrey/projector
CMD        := ./cmd/projector
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS    := -X main.version=$(VERSION) -s -w

.PHONY: all build install test test-race test-cover lint vet fmt clean help

## all: build the binary (default target)
all: build

## build: compile the binary to ./bin/projector
build:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

## install: install the binary to $GOPATH/bin (or ~/go/bin)
install:
	go install -ldflags "$(LDFLAGS)" $(CMD)

## test: run all tests
test:
	go test ./...

## test-race: run tests with the race detector
test-race:
	go test -race ./...

## test-cover: run tests and output coverage report
test-cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	@rm -f coverage.out

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go source files
fmt:
	gofmt -w -s .

## lint: run golangci-lint (must be installed separately)
lint:
	golangci-lint run ./...

## tidy: tidy and verify go.mod
tidy:
	go mod tidy
	go mod verify

## clean: remove build artifacts
clean:
	rm -rf bin/ coverage.out

## help: print this help
help:
	@grep -h '## ' $(MAKEFILE_LIST) | grep -v 'grep' | sed 's/## //'
