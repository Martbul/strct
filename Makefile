BINARY      := strct-agent
BUILD_DIR   := ./bin
CMD         := ./cmd/agent

DEFAULT_DOMAIN  ?= localhost
DEFAULT_VPS_IP  ?= 127.0.0.1

LDFLAGS := -X main.DefaultDomain=$(DEFAULT_DOMAIN) \
           -X main.DefaultVPSIP=$(DEFAULT_VPS_IP)

RELEASE_LDFLAGS := $(LDFLAGS) -s -w

GO      := go
GOTEST  := $(GO) test
GOBUILD := $(GO) build

# -race:    data race detector (mandatory — never skip this)
# -count=1: disable test result caching so tests always re-run
TEST_FLAGS   := -race -count=1
TEST_TIMEOUT := 2m
E2E_TIMEOUT  := 5m

# Integration tests are slow: blocklist download ~10s, Tailscale handshake ~30s.
INTEG_TIMEOUT := 5m

# SSH targets — override per-session: make test-integration-remote DEVICE_OPI=pi@x.x.x.x
DEVICE_VM  ?= martbul@192.168.100.19
DEVICE_OPI ?= martbul@192.168.1.10

# Tailscale pre-auth key for VPN integration tests.
# Generate at tailscale.com/admin/settings/keys then:
#   export TAILSCALE_TEST_AUTH_KEY=tskey-auth-xxx
TAILSCALE_TEST_AUTH_KEY ?=

STATICCHECK := $(shell which staticcheck 2>/dev/null)

PPROF_PORT ?= 6060

UNAME := $(shell uname)
ifeq ($(UNAME), Darwin)
	OPEN := open
else
	OPEN := xdg-open
endif

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
# Unit tests (run anywhere — no hardware, no sudo needed)
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
# Integration tests — run directly ON the Orange Pi
#
# Prerequisites on the Pi:
#   - root / sudo — iptables, ip, hostapd, dnsmasq all require it
#   - Packages installed: hostapd dnsmasq tailscale iw wireless-tools
#   - Interface wlan0 present (need not be active)
#   - Go installed on the Pi, OR use the remote targets below to ship
#     pre-compiled test binaries from your laptop (no Go needed on Pi)
#
# All integration tests are guarded by //go:build integration so they
# never run during a normal `go test ./...` — only when -tags integration
# is explicitly passed.
# -----------------------------------------------------------------------------

# Internal macro — keeps all local integration invocations consistent.
define run_integration
	@printf "$(CYAN)▶ Integration: $(1)$(RESET)\n"
	@printf "$(YELLOW)  Requires root + real hardware on this machine$(RESET)\n"
	sudo $(GO) test \
		-tags integration \
		-v \
		-count=1 \
		-timeout $(INTEG_TIMEOUT) \
		$(2) \
		$(3)
endef

.PHONY: test-integration
test-integration: ## Run ALL integration tests (must be on the Orange Pi, as root)
	$(call run_integration,all packages,./internal/features/...,)

.PHONY: test-integration-wifi
test-integration-wifi: ## WiFi: AP mode, extender mode, teardown, Status() accuracy
	$(call run_integration,wifi,./internal/features/wifi/...,-run TestIntegration)

.PHONY: test-integration-adblock
test-integration-adblock: ## Adblock: blocklist download, conf write, dnsmasq reload, DNS query check
	$(call run_integration,adblock,./internal/features/adblock/...,-run TestIntegration)

.PHONY: test-integration-router
test-integration-router: ## Router: iptables rules, hostapd conf, device scanning via arp
	$(call run_integration,router,./internal/features/router/...,-run TestIntegration)

.PHONY: test-integration-vpn
test-integration-vpn: ## VPN: tailscale up, subnet routing, status — needs TAILSCALE_TEST_AUTH_KEY
	@if [ -z "$(TAILSCALE_TEST_AUTH_KEY)" ]; then \
		printf "$(RED)✗ TAILSCALE_TEST_AUTH_KEY is not set$(RESET)\n"; \
		printf "$(YELLOW)  Generate an ephemeral key at tailscale.com/admin/settings/keys$(RESET)\n"; \
		printf "$(YELLOW)  Then: export TAILSCALE_TEST_AUTH_KEY=tskey-auth-xxx$(RESET)\n"; \
		exit 1; \
	fi
	@printf "$(CYAN)▶ Integration: vpn$(RESET)\n"
	@printf "$(YELLOW)  Requires root, tailscale installed, and valid auth key$(RESET)\n"
	sudo -E $(GO) test \
		-tags integration \
		-v \
		-count=1 \
		-timeout $(INTEG_TIMEOUT) \
		-run TestIntegration \
		./internal/features/vpn/...

.PHONY: test-integration-one
test-integration-one: ## Run one named integration test: make test-integration-one TEST=TestIntegration_RouterMode_HostapdStarts
ifndef TEST
	@printf "$(RED)Usage: make test-integration-one TEST=TestIntegration_RouterMode_HostapdStarts$(RESET)\n"
	@exit 1
endif
	@printf "$(CYAN)▶ Running: $(TEST)$(RESET)\n"
	sudo $(GO) test \
		-tags integration \
		-v \
		-count=1 \
		-timeout $(INTEG_TIMEOUT) \
		-run $(TEST) \
		./internal/features/...

# -----------------------------------------------------------------------------
# Remote integration tests
#
# Cross-compiles test binaries on your laptop then ships them to the Pi
# over SSH. Output streams back so you see results in your terminal.
# No Go installation required on the Pi.
#
# Default target: DEVICE_OPI (martbul@192.168.1.10)
# Override:       make test-integration-remote DEVICE_OPI=pi@x.x.x.x
# -----------------------------------------------------------------------------

# Compile a test binary for linux/arm64.
# $1 = package path   $2 = output binary name
define build_test_binary
	@printf "$(CYAN)  Compiling $(2) (linux/arm64)...$(RESET)\n"
	GOOS=linux GOARCH=arm64 \
		$(GO) test \
		-tags integration \
		-c \
		-o $(BUILD_DIR)/$(2) \
		$(1)
endef

.PHONY: test-integration-remote
test-integration-remote: ## Cross-compile all integration tests → scp → run on Pi (streams output)
	@printf "$(CYAN)Building integration test binaries for linux/arm64...$(RESET)\n"
	@mkdir -p $(BUILD_DIR)
	$(call build_test_binary,./internal/features/wifi,wifi.test)
	$(call build_test_binary,./internal/features/adblock,adblock.test)
	$(call build_test_binary,./internal/features/router,router.test)
	$(call build_test_binary,./internal/features/vpn,vpn.test)
	@printf "$(CYAN)Copying to $(DEVICE_OPI)...$(RESET)\n"
	ssh $(DEVICE_OPI) "mkdir -p ~/integ-tests"
	scp $(BUILD_DIR)/wifi.test \
	    $(BUILD_DIR)/adblock.test \
	    $(BUILD_DIR)/router.test \
	    $(BUILD_DIR)/vpn.test \
	    $(DEVICE_OPI):~/integ-tests/
	@printf "$(CYAN)Running on $(DEVICE_OPI) — streaming output...$(RESET)\n"
	ssh $(DEVICE_OPI) " \
		cd ~/integ-tests && \
		sudo ./wifi.test    -test.v -test.timeout $(INTEG_TIMEOUT) -test.run TestIntegration && \
		sudo ./adblock.test -test.v -test.timeout $(INTEG_TIMEOUT) -test.run TestIntegration && \
		sudo ./router.test  -test.v -test.timeout $(INTEG_TIMEOUT) -test.run TestIntegration \
	"
	@printf "$(GREEN)✓ Remote integration tests complete$(RESET)\n"

.PHONY: test-integration-remote-wifi
test-integration-remote-wifi: ## Cross-compile + run WiFi integration tests on Pi only
	@mkdir -p $(BUILD_DIR)
	$(call build_test_binary,./internal/features/wifi,wifi.test)
	scp $(BUILD_DIR)/wifi.test $(DEVICE_OPI):~/
	@printf "$(CYAN)Running WiFi integration tests on $(DEVICE_OPI)...$(RESET)\n"
	ssh $(DEVICE_OPI) "sudo ~/wifi.test -test.v -test.timeout $(INTEG_TIMEOUT) -test.run TestIntegration"
	@printf "$(GREEN)✓ Done$(RESET)\n"

.PHONY: test-integration-remote-adblock
test-integration-remote-adblock: ## Cross-compile + run adblock integration tests on Pi only
	@mkdir -p $(BUILD_DIR)
	$(call build_test_binary,./internal/features/adblock,adblock.test)
	scp $(BUILD_DIR)/adblock.test $(DEVICE_OPI):~/
	@printf "$(CYAN)Running adblock integration tests on $(DEVICE_OPI)...$(RESET)\n"
	ssh $(DEVICE_OPI) "sudo ~/adblock.test -test.v -test.timeout $(INTEG_TIMEOUT) -test.run TestIntegration"
	@printf "$(GREEN)✓ Done$(RESET)\n"

.PHONY: test-integration-remote-vpn
test-integration-remote-vpn: ## Cross-compile + run VPN integration tests on Pi (needs TAILSCALE_TEST_AUTH_KEY)
	@if [ -z "$(TAILSCALE_TEST_AUTH_KEY)" ]; then \
		printf "$(RED)✗ TAILSCALE_TEST_AUTH_KEY is not set$(RESET)\n"; \
		printf "$(YELLOW)  Generate one at tailscale.com/admin/settings/keys$(RESET)\n"; \
		exit 1; \
	fi
	@mkdir -p $(BUILD_DIR)
	$(call build_test_binary,./internal/features/vpn,vpn.test)
	scp $(BUILD_DIR)/vpn.test $(DEVICE_OPI):~/
	@printf "$(CYAN)Running VPN integration tests on $(DEVICE_OPI)...$(RESET)\n"
	ssh $(DEVICE_OPI) " \
		sudo TAILSCALE_TEST_AUTH_KEY=$(TAILSCALE_TEST_AUTH_KEY) \
		~/vpn.test -test.v -test.timeout $(INTEG_TIMEOUT) -test.run TestIntegration \
	"
	@printf "$(GREEN)✓ Done$(RESET)\n"

# -----------------------------------------------------------------------------
# Pi diagnostics — run these before/during/after integration tests
# -----------------------------------------------------------------------------

.PHONY: integ-check
integ-check: ## SSH to Pi and verify all integration test prerequisites
	@printf "$(CYAN)Checking prerequisites on $(DEVICE_OPI)...$(RESET)\n"
	@ssh $(DEVICE_OPI) ' \
		echo "=== Board ==="; \
		cat /proc/device-tree/model 2>/dev/null || echo "(no device-tree — not on SBC)"; \
		uname -a; \
		echo ""; \
		echo "=== User (tests need root) ==="; \
		id; \
		echo ""; \
		echo "=== Required binaries ==="; \
		for b in hostapd dnsmasq tailscale tailscaled iw ip iptables tc wpa_supplicant; do \
			which $$b 2>/dev/null && echo "  ✓ $$b" || echo "  ✗ $$b  ← MISSING"; \
		done; \
		echo ""; \
		echo "=== Network interfaces ==="; \
		ip link show | grep -E "^[0-9]+:" | awk "{print \"  \" \$$2}"; \
		echo ""; \
		echo "=== Service status ==="; \
		for svc in hostapd dnsmasq tailscaled; do \
			st=$$(systemctl is-active $$svc 2>/dev/null); \
			echo "  $$svc: $$st"; \
		done; \
		echo ""; \
		echo "=== Disk space (/etc /tmp) ==="; \
		df -h /etc /tmp; \
		echo ""; \
		echo "=== Go on Pi ==="; \
		go version 2>/dev/null || echo "  not installed (use remote targets to ship pre-built test binaries)"; \
	'
	@printf "$(GREEN)✓ Check complete$(RESET)\n"

.PHONY: integ-logs
integ-logs: ## Stream journald logs from hostapd, dnsmasq, strct-agent on Pi (Ctrl+C to stop)
	@printf "$(CYAN)Streaming logs from $(DEVICE_OPI) — Ctrl+C to stop$(RESET)\n"
	ssh $(DEVICE_OPI) "sudo journalctl -f -u hostapd -u dnsmasq -u strct-agent --no-hostname -o short-monotonic"

.PHONY: integ-cleanup
integ-cleanup: ## SSH to Pi and remove all leftover state from failed integration tests
	@printf "$(YELLOW)Cleaning up integration test state on $(DEVICE_OPI)...$(RESET)\n"
	ssh $(DEVICE_OPI) ' \
		sudo systemctl stop hostapd dnsmasq 2>/dev/null || true; \
		sudo iptables -F; \
		sudo iptables -t nat -F; \
		sudo iptables -X; \
		sudo ip6tables -F 2>/dev/null || true; \
		sudo ip addr flush dev wlan0 2>/dev/null || true; \
		sudo iw dev wlan0_ap del 2>/dev/null || true; \
		sudo killall wpa_supplicant 2>/dev/null || true; \
		sudo tailscale down 2>/dev/null || true; \
		sudo rm -f /etc/dnsmasq.d/adblock.conf \
		           /etc/dnsmasq.d/strct.conf \
		           /etc/hostapd/hostapd.conf; \
		echo "Done — Pi is clean"; \
	'
	@printf "$(GREEN)✓ Cleanup complete$(RESET)\n"

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
# Profiling (pprof via SSH tunnel)
# -----------------------------------------------------------------------------

.PHONY: pprof-kill
pprof-kill: ## Kill any existing SSH tunnel holding PPROF_PORT
	@printf "$(YELLOW)Freeing port $(PPROF_PORT)...$(RESET)\n"
	@fuser -k $(PPROF_PORT)/tcp 2>/dev/null || lsof -ti:$(PPROF_PORT) | xargs kill -9 2>/dev/null || true
	@printf "$(GREEN)✓ Port $(PPROF_PORT) is free$(RESET)\n"

.PHONY: pprof-vm
pprof-vm: pprof-kill ## Tunnel pprof from dev VM (192.168.100.19) → localhost:6060
	@printf "$(CYAN)Tunnelling pprof from VM → localhost:$(PPROF_PORT)$(RESET)\n"
	@printf "$(YELLOW)Open:  http://localhost:$(PPROF_PORT)/debug/pprof$(RESET)\n"
	@printf "$(YELLOW)Ctrl+C to close the tunnel$(RESET)\n"
	ssh -N -L $(PPROF_PORT):localhost:$(PPROF_PORT) $(DEVICE_VM)

.PHONY: pprof-opi
pprof-opi: pprof-kill ## Tunnel pprof from Orange Pi → localhost:6060
	@printf "$(CYAN)Tunnelling pprof from Orange Pi → localhost:$(PPROF_PORT)$(RESET)\n"
	@printf "$(YELLOW)Open:  http://localhost:$(PPROF_PORT)/debug/pprof$(RESET)\n"
	@printf "$(YELLOW)Ctrl+C to close the tunnel$(RESET)\n"
	ssh -N -L $(PPROF_PORT):localhost:$(PPROF_PORT) $(DEVICE_OPI)

.PHONY: pprof-cpu
pprof-cpu: ## Capture 30s CPU profile and open flame graph (tunnel must be open)
	@printf "$(CYAN)Capturing 30s CPU profile...$(RESET)\n"
	$(GO) tool pprof -http=:8081 \
		"http://localhost:$(PPROF_PORT)/debug/pprof/profile?seconds=30"

.PHONY: pprof-heap
pprof-heap: ## Capture heap profile and open flame graph (tunnel must be open)
	@printf "$(CYAN)Capturing heap profile...$(RESET)\n"
	$(GO) tool pprof -http=:8081 \
		"http://localhost:$(PPROF_PORT)/debug/pprof/heap"

.PHONY: pprof-goroutines
pprof-goroutines: ## Dump all goroutines — useful for finding leaks (tunnel must be open)
	@printf "$(CYAN)Goroutine dump:$(RESET)\n"
	curl -s "http://localhost:$(PPROF_PORT)/debug/pprof/goroutine?debug=2"

.PHONY: pprof-allocs
pprof-allocs: ## Show allocation profile (tunnel must be open)
	@printf "$(CYAN)Capturing allocation profile...$(RESET)\n"
	$(GO) tool pprof -http=:8081 \
		"http://localhost:$(PPROF_PORT)/debug/pprof/allocs"

# -----------------------------------------------------------------------------
# Deploy / Install
# -----------------------------------------------------------------------------

.PHONY: install
install: build-arm64 ## Cross-compile and copy binary to the device over SSH
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
	@printf "$(BOLD)Unit tests (run anywhere):$(RESET)\n"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| grep -E '^test' | grep -v integration \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  $(CYAN)%-40s$(RESET) %s\n", $$1, $$2}'
	@printf "\n$(BOLD)Integration — local (on Orange Pi, as root):$(RESET)\n"
	@grep -E '^test-integration[^-r][a-zA-Z_-]*:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  $(CYAN)%-40s$(RESET) %s\n", $$1, $$2}'
	@printf "\n$(BOLD)Integration — remote (cross-compile on laptop → run on Pi):$(RESET)\n"
	@grep -E '^test-integration-remote[a-zA-Z_-]*:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  $(CYAN)%-40s$(RESET) %s\n", $$1, $$2}'
	@printf "\n$(BOLD)Pi diagnostics:$(RESET)\n"
	@grep -E '^integ-[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  $(CYAN)%-40s$(RESET) %s\n", $$1, $$2}'
	@printf "\n$(BOLD)Build / Run / Other:$(RESET)\n"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| grep -vE '^(test|integ)' \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  $(CYAN)%-40s$(RESET) %s\n", $$1, $$2}'
	@printf "\n$(BOLD)Variables:$(RESET)\n"
	@printf "  $(CYAN)DEFAULT_DOMAIN$(RESET)            Backend domain (default: localhost)\n"
	@printf "  $(CYAN)DEFAULT_VPS_IP$(RESET)            VPS server IP  (default: 127.0.0.1)\n"
	@printf "  $(CYAN)DEVICE$(RESET)                    SSH target for install (e.g. pi@192.168.1.10)\n"
	@printf "  $(CYAN)DEVICE_OPI$(RESET)                Orange Pi SSH target (default: martbul@192.168.1.10)\n"
	@printf "  $(CYAN)DEVICE_VM$(RESET)                 VM SSH target for pprof (default: martbul@192.168.100.19)\n"
	@printf "  $(CYAN)TAILSCALE_TEST_AUTH_KEY$(RESET)   Ephemeral pre-auth key for VPN integration tests\n"
	@printf "  $(CYAN)PKG$(RESET)                       Package path for test-pkg\n"
	@printf "  $(CYAN)TEST$(RESET)                      Test name for test-integration-one\n"
	@printf "  $(CYAN)PPROF_PORT$(RESET)                pprof port (default: 6060)\n"
	@printf "\n$(BOLD)Typical integration workflow:$(RESET)\n"
	@printf "  make integ-check                                   # verify Pi has prereqs\n"
	@printf "  make test-integration-remote-wifi                  # compile + ship + run WiFi tests\n"
	@printf "  make test-integration-remote-adblock               # compile + ship + run adblock tests\n"
	@printf "  make integ-logs                                    # stream Pi logs in parallel\n"
	@printf "  make integ-cleanup                                 # tear down after tests\n"
	@printf "  make test-integration-one TEST=TestIntegration_X   # iterate on one test\n"