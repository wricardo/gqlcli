.PHONY: help install build test clean lint fmt dev docs

# Variables
APP_NAME := gqlcli
BINARY_NAME := gql
BINARY_PATH := ./bin/$(BINARY_NAME)
EXAMPLE_PATH := ./example/main.go
INSTALL_PATH := /usr/local/bin/$(BINARY_NAME)

# Go settings
GO := go
GOFLAGS := -v
LDFLAGS := -ldflags "-X main.Version=dev"

# Colors for output
GREEN := \033[0;32m
YELLOW := \033[0;33m
BLUE := \033[0;34m
NC := \033[0m # No Color

help: ## Show this help message
	@echo "$(BLUE)$(APP_NAME) - GraphQL CLI Library$(NC)"
	@echo ""
	@echo "$(GREEN)Available commands:$(NC)"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(BLUE)%-15s$(NC) %s\n", $$1, $$2}'
	@echo ""
	@echo "$(GREEN)Examples:$(NC)"
	@echo "  make install          # Install gql CLI to /usr/local/bin"
	@echo "  make build            # Build the CLI binary"
	@echo "  make dev              # Build and run locally for testing"
	@echo "  make test             # Run tests"
	@echo "  make clean            # Remove build artifacts"

## Build targets

build: ## Build the CLI binary
	@echo "$(BLUE)Building $(BINARY_NAME)...$(NC)"
	@mkdir -p bin
	@$(GO) build $(GOFLAGS) -o $(BINARY_PATH) $(EXAMPLE_PATH)
	@echo "$(GREEN)✓ Built successfully: $(BINARY_PATH)$(NC)"

install: build ## Install the CLI to /usr/local/bin/gql
	@echo "$(BLUE)Installing $(BINARY_NAME) to $(INSTALL_PATH)...$(NC)"
	@cp $(BINARY_PATH) $(INSTALL_PATH)
	@chmod +x $(INSTALL_PATH)
	@echo "$(GREEN)✓ Installed successfully to $(INSTALL_PATH)$(NC)"
	@echo "  Run '$(BINARY_NAME) --help' to get started"

install-local: build ## Install the CLI to ./bin (no sudo required)
	@echo "$(GREEN)✓ Already built at $(BINARY_PATH)$(NC)"
	@echo "  Add ./bin to your PATH or run: ./$(BINARY_PATH) --help"

uninstall: ## Uninstall the CLI from /usr/local/bin/gql
	@echo "$(BLUE)Uninstalling $(BINARY_NAME)...$(NC)"
	@rm -f $(INSTALL_PATH)
	@echo "$(GREEN)✓ Uninstalled successfully$(NC)"

## Development targets

dev: build ## Build and test locally
	@echo "$(BLUE)Testing $(BINARY_NAME)...$(NC)"
	@$(BINARY_PATH) --help
	@echo ""
	@echo "$(BLUE)Try these commands:$(NC)"
	@echo "  $(BINARY_PATH) query --help"
	@echo "  $(BINARY_PATH) mutation --help"
	@echo "  $(BINARY_PATH) introspect --help"
	@echo "  $(BINARY_PATH) types --help"

test: ## Run tests
	@echo "$(BLUE)Running tests...$(NC)"
	@$(GO) test -v ./...

test-coverage: ## Run tests with coverage
	@echo "$(BLUE)Running tests with coverage...$(NC)"
	@$(GO) test -v -coverprofile=coverage.out ./...
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)✓ Coverage report: coverage.html$(NC)"

## Code quality targets

lint: ## Run linter
	@echo "$(BLUE)Running linter...$(NC)"
	@which golangci-lint > /dev/null || (echo "$(YELLOW)Installing golangci-lint...$(NC)" && $(GO) install github.com/golangci-lint/golangci-lint/cmd/golangci-lint@latest)
	@golangci-lint run ./...

fmt: ## Format code
	@echo "$(BLUE)Formatting code...$(NC)"
	@$(GO) fmt ./...
	@echo "$(GREEN)✓ Code formatted$(NC)"

vet: ## Run go vet
	@echo "$(BLUE)Running go vet...$(NC)"
	@$(GO) vet ./...
	@echo "$(GREEN)✓ No issues found$(NC)"

## Dependency targets

deps: ## Download and verify dependencies
	@echo "$(BLUE)Downloading dependencies...$(NC)"
	@$(GO) mod download
	@echo "$(GREEN)✓ Dependencies downloaded$(NC)"

deps-update: ## Update all dependencies
	@echo "$(BLUE)Updating dependencies...$(NC)"
	@$(GO) get -u ./...
	@$(GO) mod tidy
	@echo "$(GREEN)✓ Dependencies updated$(NC)"

deps-tidy: ## Tidy up dependencies
	@echo "$(BLUE)Tidying dependencies...$(NC)"
	@$(GO) mod tidy
	@echo "$(GREEN)✓ Dependencies tidied$(NC)"

## Documentation targets

docs: ## Generate documentation
	@echo "$(BLUE)Generating documentation...$(NC)"
	@echo "$(GREEN)✓ See README.md for documentation$(NC)"
	@echo "  Run './$(BINARY_PATH) --help' for CLI help"
	@echo "  Run './$(BINARY_PATH) query --help' for query command help"

## Cleanup targets

clean: ## Remove build artifacts
	@echo "$(BLUE)Cleaning up...$(NC)"
	@rm -rf bin/
	@rm -f coverage.out coverage.html
	@$(GO) clean -testcache
	@echo "$(GREEN)✓ Cleaned up$(NC)"

clean-all: clean ## Remove all generated files including dependencies
	@echo "$(BLUE)Removing go.sum...$(NC)"
	@rm -f go.sum
	@$(GO) mod download
	@echo "$(GREEN)✓ Full cleanup complete$(NC)"

## Utility targets

version: ## Show version information
	@echo "$(BLUE)$(APP_NAME) Version Information$(NC)"
	@echo "  Go Version: $$($(GO) version | awk '{print $$3}')"
	@echo "  Module: $$($(GO) list -m)"
	@echo "  Build Path: $(BINARY_PATH)"

info: ## Show project information
	@echo "$(BLUE)Project Information$(NC)"
	@echo "  Name: $(APP_NAME)"
	@echo "  Binary: $(BINARY_NAME)"
	@echo "  Install Path: $(INSTALL_PATH)"
	@echo "  Source: $(EXAMPLE_PATH)"
	@echo ""
	@echo "$(GREEN)Dependencies:$(NC)"
	@$(GO) list -m all | grep -v "$(APP_NAME)" || echo "  None"
	@echo ""
	@echo "$(GREEN)Files:$(NC)"
	@find ./pkg -name "*.go" -type f | sort

all: deps fmt lint vet test build ## Run all checks and build

.DEFAULT_GOAL := help
