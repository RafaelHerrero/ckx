BINARY  := ckx
PKG     := github.com/eimarfandino/ckx
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(PKG)/cmd.Version=$(VERSION)

.DEFAULT_GOAL := build
.PHONY: build run install uninstall test vet fmt clean help

build: ## Build ./ckx
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

run: build ## Build and run ./ckx (pass args with ARGS="use my-profile")
	./$(BINARY) $(ARGS)

install: ## Install ckx into $(go env GOPATH)/bin
	go install -ldflags "$(LDFLAGS)" .

uninstall: ## Remove ckx from $(go env GOPATH)/bin
	rm -f "$$(go env GOPATH)/bin/$(BINARY)"

test: ## Run tests
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format code
	gofmt -w .

clean: ## Remove the built binary
	rm -f $(BINARY)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-10s %s\n", $$1, $$2}'
