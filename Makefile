.PHONY: help build install uninstall test test-race test-real-vm lint fmt vet tidy clean run release

GO ?= go
GOLANGCI_LINT ?= golangci-lint
PKGS := ./...
BIN_DIR := bin
PREFIX ?= $(HOME)/.local
VERSION_FILE := VERSION
CURRENT_VERSION := $(shell cat $(VERSION_FILE) 2>/dev/null | tr -d '[:space:]')
VERSION_PKG := github.com/nficano/xpc/internal/version

help: ## List available targets.
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## Build the xpc binary into ./bin/xpc.
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "-X $(VERSION_PKG).Version=$(CURRENT_VERSION)" -o $(BIN_DIR)/xpc ./cmd/xpc

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

# ---- release ---------------------------------------------------------------
#
# `make release` bumps VERSION (patch by default), commits, tags `v<version>`,
# and pushes main + the tag. The GitHub Actions `release` workflow then runs
# debaser against the tagged commit, builds binaries, and publishes the
# release titled `v<version> — <codename>`.
#
#   make release                 # patch bump (0.1.0 -> 0.1.1)
#   make release BUMP=minor      # minor bump (0.1.0 -> 0.2.0)
#   make release BUMP=major      # major bump (0.1.0 -> 1.0.0)
#   make release VERSION=1.2.3   # explicit version (overrides BUMP)
BUMP ?= patch

release: ## Bump VERSION, commit, tag, and push. Use BUMP=patch|minor|major or VERSION=x.y.z.
	@set -eu; \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "error: working tree is dirty; commit or stash first" >&2; exit 1; \
	fi; \
	branch=$$(git symbolic-ref --short HEAD); \
	if [ "$$branch" != "main" ]; then \
		echo "error: releases must be cut from main (on $$branch)" >&2; exit 1; \
	fi; \
	cur=$$(cat $(VERSION_FILE) | tr -d '[:space:]'); \
	if [ -n "$(VERSION)" ]; then \
		next="$(VERSION)"; \
	else \
		major=$$(printf '%s' "$$cur" | cut -d. -f1); \
		minor=$$(printf '%s' "$$cur" | cut -d. -f2); \
		patch=$$(printf '%s' "$$cur" | cut -d. -f3); \
		case "$(BUMP)" in \
			major) next=$$((major + 1)).0.0 ;; \
			minor) next=$$major.$$((minor + 1)).0 ;; \
			patch) next=$$major.$$minor.$$((patch + 1)) ;; \
			*) echo "error: BUMP must be one of: patch, minor, major" >&2; exit 1 ;; \
		esac; \
	fi; \
	echo "Releasing v$$next (current: $$cur)"; \
	printf '%s\n' "$$next" > $(VERSION_FILE); \
	git add $(VERSION_FILE); \
	git commit -m "Release v$$next"; \
	git tag -a "v$$next" -m "Release v$$next"; \
	echo "Pushing main and v$$next..."; \
	git push origin main "v$$next"; \
	echo "Done. The release workflow will publish v$$next with a debaser codename."
