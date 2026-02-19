# =============================================================================
# strct-agent Makefile
# =============================================================================
#
# Usage:
#   make              → build (default)
#   make dev          → run in dev mode (no sudo, mock hardware)
#   make run          → build + run with sudo (real hardware)
#   make test         → run all unit tests with race detector
#   make test-e2e     → run end-to-end tests (builds real binary)
#   make test-cover   → unit tests + open HTML coverage report
#   make lint         → gofmt + go vet + staticcheck
#   make tidy         → go mod tidy
#   make clean        → remove build artifacts
#   make help         → print this message
#
# =============================================================================

# -----------------------------------------------------------------------------
# Variables
# -----------------------------------------------------------------------------

# Binary name and output path
BINARY      := strct-agent
BUILD_DIR   := ./bin

# Main entrypoint
CMD         := ./cmd/agent

# Build-time variable injection (overridden by CI/release pipeline)
DEFAULT_DOMAIN  ?= localhost
DEFAULT_VPS_IP  ?= 127.0.0.1

# These match the var names declared in cmd/agent/main.go
LDFLAGS := -X main.DefaultDomain=$(DEFAULT_DOMAIN) \
           -X main.DefaultVPSIP=$(DEFAULT_VPS_IP)

# Strip debug info in release builds (smaller binary, no source paths)
RELEASE_LDFLAGS := $(LDFLAGS) -s -w

# Go toolchain
GO      := go
GOTEST  := $(GO) test
GOBUILD := $(GO) build

# Test flags used everywhere
# -race:     data race detector (mandatory — never skip this)
# -count=1:  disable test result caching so tests always re-run
TEST_FLAGS := -race -count=1

# Timeout for unit tests. E2E gets its own longer timeout.
TEST_TIMEOUT    := 2m
E2E_TIMEOUT     := 5m

# staticcheck binary (installed via go install if missing)
STATICCHECK := $(shell which staticcheck 2>/dev/null)

# Platform detection for open/xdg-open
UNAME := $(shell uname)
ifeq ($(UNAME), Darwin)
	OPEN := open
else
	OPEN := xdg-open
endif

# Colour output (disabled if NO_COLOR is set or terminal has no colours)
ifneq ($(NO_COLOR),1)
	RESET  := \033[0m
	BOLD   := \033[1m
	GREEN  := \033[32m
	YELLOW := \033[33m
	CYAN   := \033[36m
	RED    := \033[31m
endif

# -----------------------------------------------------------------------------
# Default target
# -----------------------------------------------------------------------------

.DEFAULT_GOAL := build

# -----------------------------------------------------------------------------
# Build
# -----------------------------------------------------------------------------

.PHONY: build
build: ## Build the agent binary for the current platform
	@printf "$(CYAN)Building $(BINARY)...$(RESET)\n"
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) $(CMD)
	@printf "$(GREEN)✓ Built: $(BUILD_DIR)/$(BINARY)$(RESET)\n"

.PHONY: build-arm64
build-arm64: ## Cross-compile for ARM64 Linux (Orange Pi / Raspberry Pi)
	@printf "$(CYAN)Cross-compiling for linux/arm64...$(RESET)\n"
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 \
		$(GOBUILD) \
		-ldflags "$(RELEASE_LDFLAGS)" \
		-o $(BUILD_DIR)/$(BINARY)-arm64 \
		$(CMD)
	@printf "$(GREEN)✓ Built: $(BUILD_DIR)/$(BINARY)-arm64$(RESET)\n"

.PHONY: build-release
build-release: ## Build stripped release binary for current platform
	@printf "$(CYAN)Building release binary...$(RESET)\n"
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) \
		-ldflags "$(RELEASE_LDFLAGS)" \
		-o $(BUILD_DIR)/$(BINARY) \
		$(CMD)
	@printf "$(GREEN)✓ Release binary: $(BUILD_DIR)/$(BINARY)$(RESET)\n"

# -----------------------------------------------------------------------------
# Run
# -----------------------------------------------------------------------------

.PHONY: dev
dev: ## Run in dev mode (mock WiFi/disk, no sudo required)
	@printf "$(CYAN)Starting agent in dev mode...$(RESET)\n"
	$(GO) run $(CMD) -dev

.PHONY: run
run: build ## Build then run with sudo (real hardware, requires root for iptables/nmcli)
	@printf "$(YELLOW)Running with sudo (real hardware mode)...$(RESET)\n"
	sudo $(BUILD_DIR)/$(BINARY)

.PHONY: run-dev-sudo
run-dev-sudo: build ## Build then run in dev mode with sudo (test sudo flow without real hardware)
	@printf "$(YELLOW)Running with sudo in dev mode...$(RESET)\n"
	sudo $(BUILD_DIR)/$(BINARY) -dev

# -----------------------------------------------------------------------------
# Test
# -----------------------------------------------------------------------------

.PHONY: test
test: ## Run all unit tests with race detector
	@printf "$(CYAN)Running unit tests...$(RESET)\n"
	$(GOTEST) $(TEST_FLAGS) -timeout $(TEST_TIMEOUT) ./...
	@printf "$(GREEN)✓ All tests passed$(RESET)\n"

.PHONY: test-short
test-short: ## Run tests, skip slow ones (marked with testing.Short())
	@printf "$(CYAN)Running short tests...$(RESET)\n"
	$(GOTEST) $(TEST_FLAGS) -short -timeout 30s ./...

.PHONY: test-e2e
test-e2e: ## Run end-to-end tests (builds real binary, requires no hardware)
	@printf "$(CYAN)Running e2e tests...$(RESET)\n"
	$(GOTEST) $(TEST_FLAGS) -tags e2e -timeout $(E2E_TIMEOUT) -v ./e2e/...
	@printf "$(GREEN)✓ E2E tests passed$(RESET)\n"

.PHONY: test-cover
test-cover: ## Run unit tests with coverage and open HTML report
	@printf "$(CYAN)Running tests with coverage...$(RESET)\n"
	$(GOTEST) $(TEST_FLAGS) \
		-timeout $(TEST_TIMEOUT) \
		-coverprofile=coverage.out \
		-covermode=atomic \
		./...
	$(GO) tool cover -func=coverage.out | tail -1
	$(GO) tool cover -html=coverage.out -o coverage.html
	@printf "$(GREEN)✓ Coverage report: coverage.html$(RESET)\n"
	$(OPEN) coverage.html

.PHONY: test-cover-ci
test-cover-ci: ## Run unit tests with coverage (CI mode, no browser open)
	@printf "$(CYAN)Running tests with coverage (CI)...$(RESET)\n"
	$(GOTEST) $(TEST_FLAGS) \
		-timeout $(TEST_TIMEOUT) \
		-coverprofile=coverage.out \
		-covermode=atomic \
		./...
	$(GO) tool cover -func=coverage.out

.PHONY: test-pkg
test-pkg: ## Run tests for a specific package: make test-pkg PKG=./internal/features/cloud
ifndef PKG
	@printf "$(RED)Usage: make test-pkg PKG=./internal/features/cloud$(RESET)\n"
	@exit 1
endif
	$(GOTEST) $(TEST_FLAGS) -v -timeout $(TEST_TIMEOUT) $(PKG)

# -----------------------------------------------------------------------------
# Lint & Format
# -----------------------------------------------------------------------------

.PHONY: fmt
fmt: ## Format all Go source files with gofmt
	@printf "$(CYAN)Formatting...$(RESET)\n"
	gofmt -w -s .
	@printf "$(GREEN)✓ Formatted$(RESET)\n"

.PHONY: fmt-check
fmt-check: ## Check formatting without modifying files (used in CI)
	@printf "$(CYAN)Checking formatting...$(RESET)\n"
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		printf "$(RED)Unformatted files:$(RESET)\n$$unformatted\n"; \
		printf "$(YELLOW)Run: make fmt$(RESET)\n"; \
		exit 1; \
	fi
	@printf "$(GREEN)✓ All files formatted$(RESET)\n"

.PHONY: vet
vet: ## Run go vet on all packages
	@printf "$(CYAN)Running go vet...$(RESET)\n"
	$(GO) vet ./...
	@printf "$(GREEN)✓ go vet passed$(RESET)\n"

.PHONY: staticcheck
staticcheck: ## Run staticcheck (install: go install honnef.co/go/tools/cmd/staticcheck@latest)
	@if [ -z "$(STATICCHECK)" ]; then \
		printf "$(YELLOW)staticcheck not found. Installing...$(RESET)\n"; \
		$(GO) install honnef.co/go/tools/cmd/staticcheck@latest; \
	fi
	@printf "$(CYAN)Running staticcheck...$(RESET)\n"
	staticcheck ./...
	@printf "$(GREEN)✓ staticcheck passed$(RESET)\n"

.PHONY: lint
lint: fmt-check vet staticcheck ## Run all linters (fmt-check + vet + staticcheck)
	@printf "$(GREEN)✓ All linters passed$(RESET)\n"

# -----------------------------------------------------------------------------
# Dependencies
# -----------------------------------------------------------------------------

.PHONY: tidy
tidy: ## Run go mod tidy and verify the module graph
	@printf "$(CYAN)Tidying modules...$(RESET)\n"
	$(GO) mod tidy
	$(GO) mod verify
	@printf "$(GREEN)✓ Modules tidy$(RESET)\n"

.PHONY: deps
deps: ## Print all direct dependencies
	$(GO) list -m -mod=mod all

.PHONY: deps-update
deps-update: ## Update all dependencies to their latest minor/patch versions
	@printf "$(CYAN)Updating dependencies...$(RESET)\n"
	$(GO) get -u ./...
	$(GO) mod tidy
	@printf "$(GREEN)✓ Dependencies updated. Review go.sum before committing.$(RESET)\n"

# -----------------------------------------------------------------------------
# Code generation
# -----------------------------------------------------------------------------

.PHONY: generate
generate: ## Run go generate across all packages (Wire, mocks, etc.)
	@printf "$(CYAN)Running go generate...$(RESET)\n"
	$(GO) generate ./...
	@printf "$(GREEN)✓ Generation complete$(RESET)\n"

# -----------------------------------------------------------------------------
# Deploy / Install
# -----------------------------------------------------------------------------

.PHONY: install
install: build-arm64 ## Copy the ARM64 binary to the device over SSH
ifndef DEVICE
	@printf "$(RED)Usage: make install DEVICE=pi@192.168.1.10$(RESET)\n"
	@exit 1
endif
	@printf "$(CYAN)Deploying to $(DEVICE)...$(RESET)\n"
	scp $(BUILD_DIR)/$(BINARY)-arm64 $(DEVICE):~/$(BINARY)
	ssh $(DEVICE) "chmod +x ~/$(BINARY)"
	@printf "$(GREEN)✓ Deployed to $(DEVICE):~/$(BINARY)$(RESET)\n"

.PHONY: install-service
install-service: ## Install and enable the systemd service on the device
ifndef DEVICE
	@printf "$(RED)Usage: make install-service DEVICE=pi@192.168.1.10$(RESET)\n"
	@exit 1
endif
	@printf "$(CYAN)Installing systemd service on $(DEVICE)...$(RESET)\n"
	scp scripts/strct-agent.service $(DEVICE):/tmp/strct-agent.service
	ssh $(DEVICE) "sudo mv /tmp/strct-agent.service /etc/systemd/system/ && \
	               sudo systemctl daemon-reload && \
	               sudo systemctl enable strct-agent && \
	               sudo systemctl restart strct-agent"
	@printf "$(GREEN)✓ Service installed and started$(RESET)\n"

# -----------------------------------------------------------------------------
# Utilities
# -----------------------------------------------------------------------------

.PHONY: clean
clean: ## Remove build artifacts and coverage files
	@printf "$(CYAN)Cleaning...$(RESET)\n"
	rm -rf $(BUILD_DIR) coverage.out coverage.html
	@printf "$(GREEN)✓ Clean$(RESET)\n"

.PHONY: info
info: ## Print build information
	@printf "$(BOLD)Binary:$(RESET)  $(BINARY)\n"
	@printf "$(BOLD)Go:$(RESET)      $$($(GO) version)\n"
	@printf "$(BOLD)Domain:$(RESET)  $(DEFAULT_DOMAIN)\n"
	@printf "$(BOLD)VPS IP:$(RESET)  $(DEFAULT_VPS_IP)\n"
	@printf "$(BOLD)LDFLAGS:$(RESET) $(LDFLAGS)\n"

.PHONY: help
help: ## Print available targets and their descriptions
	@printf "$(BOLD)strct-agent$(RESET) — available targets:\n\n"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  $(CYAN)%-20s$(RESET) %s\n", $$1, $$2}'
	@printf "\n$(BOLD)Variables:$(RESET)\n"
	@printf "  $(CYAN)DEFAULT_DOMAIN$(RESET)   Backend domain (default: localhost)\n"
	@printf "  $(CYAN)DEFAULT_VPS_IP$(RESET)   VPS server IP  (default: 127.0.0.1)\n"
	@printf "  $(CYAN)DEVICE$(RESET)           SSH target for deploy (e.g. pi@192.168.1.10)\n"
	@printf "  $(CYAN)PKG$(RESET)              Package path for test-pkg target\n"
	@printf "\n$(BOLD)Examples:$(RESET)\n"
	@printf "  make dev\n"
	@printf "  make test\n"
	@printf "  make test-pkg PKG=./internal/features/cloud\n"
	@printf "  make build-arm64 DEFAULT_DOMAIN=strct.org DEFAULT_VPS_IP=1.2.3.4\n"
	@printf "  make install DEVICE=pi@192.168.1.10\n"