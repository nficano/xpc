.PHONY: help build install uninstall test test-race test-real-vm lint fmt vet tidy clean run

GO ?= go
GOLANGCI_LINT ?= golangci-lint
PKGS := ./...
BIN_DIR := bin
PREFIX ?= $(HOME)/.local

help: ## List available targets.
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build the xpc binary into ./bin/xpc.
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/xpc ./cmd/xpc

install: build ## Install xpc to $(PREFIX)/bin (default: ~/.local/bin).
	@mkdir -p $(PREFIX)/bin
	install -m 0755 $(BIN_DIR)/xpc $(PREFIX)/bin/xpc
	@echo "Installed: $(PREFIX)/bin/xpc"

uninstall: ## Remove xpc from $(PREFIX)/bin.
	rm -f $(PREFIX)/bin/xpc

test: ## Run unit + integration tests.
	$(GO) test -race -coverprofile=coverage.out $(PKGS)

test-race: test ## Alias for test (race detector enabled by default).

test-real-vm: ## Run tests against the live XP VM (requires ~/.xpc/config).
	@echo "Real-VM tests are not yet implemented. They land alongside Phase 4 (xpc serve)."

lint: vet ## Run linters: go vet + golangci-lint.
	$(GOLANGCI_LINT) run

fmt: ## Format Go source files.
	$(GO) fmt $(PKGS)

vet: ## Run go vet.
	$(GO) vet $(PKGS)

tidy: ## Run go mod tidy.
	$(GO) mod tidy

clean: ## Remove build artifacts.
	rm -rf $(BIN_DIR)/ dist/ coverage.out coverage.htmlot

run: build ## Build and run the xpc binary.
	./$(BIN_DIR)/xpc $(ARGS)
