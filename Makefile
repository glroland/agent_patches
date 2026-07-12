BINARY         := patches-endpoint-server
CLI_BINARY     := patches-cli
MIGRATE_BINARY := migrate-memory
SRC_DIR        := ./endpoint-server
CLI_DIR        := ./cli
MIGRATE_DIR    := ./migrate-memory
TARGET_DIR     := target
GO         := go
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -ldflags "-X agent_patches/endpoint-server/buildinfo.BuildTime=$(BUILD_TIME)"

# Release subdirectories per platform
LINUX_AMD64_DIR   := $(TARGET_DIR)/linux-x86_64
LINUX_ARM64_DIR   := $(TARGET_DIR)/linux-arm64
MAC_AMD64_DIR     := $(TARGET_DIR)/darwin-x86_64
MAC_ARM64_DIR     := $(TARGET_DIR)/darwin-arm64
WINDOWS_AMD64_DIR := $(TARGET_DIR)/windows-x86_64

# Detect the current platform to locate the right release binary for `make run`.
# uname -s: Linux | Darwin   uname -m: x86_64 | arm64 | ...
UNAME_OS   := $(shell uname -s | tr '[:upper:]' '[:lower:]')
UNAME_ARCH := $(shell uname -m)
PLATFORM_DIR := $(TARGET_DIR)/$(UNAME_OS)-$(UNAME_ARCH)

# Config file used by `make run`. Defaults to config.yaml in the project root.
# Override with: make run CONFIG=/path/to/config.yaml
CONFIG ?= $(CURDIR)/config.yaml

# Set to 0 to skip per-host confirmation prompts during deploy.
# Override with: make deploy DEPLOY_CONFIRM=0  (or use make deploy-all)
DEPLOY_CONFIRM ?= 1

# Directory for the central-ui React app.
CENTRAL_UI_DIR := $(CURDIR)/central-ui

# Directory for the central-backend Node app.
CENTRAL_BACKEND_DIR := $(CURDIR)/central-backend

# Inventory and config shared between deploy and central-backend.
INVENTORY_ROOT := $(CURDIR)/../home-utils/admin/agent_patches
DEPLOY_SCRIPT  := $(CURDIR)/deploy/linux/deploy.sh
RESTART_SCRIPT := $(CURDIR)/deploy/linux/restart.sh
MIGRATE_MEMORY_SCRIPT := $(CURDIR)/deploy/linux/migrate_memory.sh

# Set to 0 to skip per-host confirmation prompts during migrate-memory.
# Override with: make migrate-memory MIGRATE_CONFIRM=0  (or use make migrate-memory-all)
MIGRATE_CONFIRM ?= 1

# Set to 1 to purge migrated keys from attrs.json after migrate-memory runs.
# Off by default so the old copy survives as a backup on first pass.
MIGRATE_PURGE ?= 0

# Set to 1 to preview migrate-memory's changes without writing anything.
MIGRATE_DRY_RUN ?= 0

# OS-specific default responsibilities and system prompt files (in-repo).
CONFIG_DIR := $(CURDIR)/config

.PHONY: install build build-server build-cli build-migrate-memory \
        release release-server release-cli release-migrate-memory \
        test run run-cli run-central-ui run-central-backend \
        deploy deploy-all restart migrate-memory migrate-memory-all clean fmt vet help

## install: download and tidy all module dependencies
install:
	$(GO) mod download
	$(GO) mod tidy

## build: fmt + vet, then compile both binaries for the current platform into target/
build: fmt vet build-server build-cli

## build-server: compile the patches-endpoint-server binary for the current platform
build-server:
	mkdir -p $(TARGET_DIR)
	$(GO) build $(LDFLAGS) -o $(TARGET_DIR)/$(BINARY) $(SRC_DIR)

## build-cli: compile the patches-cli binary for the current platform
build-cli:
	mkdir -p $(TARGET_DIR)
	$(GO) build -o $(TARGET_DIR)/$(CLI_BINARY) $(CLI_DIR)

## build-migrate-memory: compile the migrate-memory binary for the current platform
build-migrate-memory:
	mkdir -p $(TARGET_DIR)
	$(GO) build -o $(TARGET_DIR)/$(MIGRATE_BINARY) $(MIGRATE_DIR)

## release: cross-compile both binaries for all target platforms
release: clean release-server release-cli

## release-server: cross-compile patches-endpoint-server for all target platforms
release-server:
	mkdir -p $(LINUX_AMD64_DIR) $(LINUX_ARM64_DIR) $(MAC_AMD64_DIR) $(MAC_ARM64_DIR) $(WINDOWS_AMD64_DIR)
	GOOS=linux   GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(LINUX_AMD64_DIR)/$(BINARY)          $(SRC_DIR)
	GOOS=linux   GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(LINUX_ARM64_DIR)/$(BINARY)          $(SRC_DIR)
	GOOS=darwin  GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(MAC_AMD64_DIR)/$(BINARY)            $(SRC_DIR)
	GOOS=darwin  GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(MAC_ARM64_DIR)/$(BINARY)            $(SRC_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(WINDOWS_AMD64_DIR)/$(BINARY).exe    $(SRC_DIR)

## release-cli: cross-compile patches-cli for all target platforms
release-cli:
	mkdir -p $(LINUX_AMD64_DIR) $(LINUX_ARM64_DIR) $(MAC_AMD64_DIR) $(MAC_ARM64_DIR) $(WINDOWS_AMD64_DIR)
	GOOS=linux   GOARCH=amd64 $(GO) build -o $(LINUX_AMD64_DIR)/$(CLI_BINARY)       $(CLI_DIR)
	GOOS=linux   GOARCH=arm64 $(GO) build -o $(LINUX_ARM64_DIR)/$(CLI_BINARY)       $(CLI_DIR)
	GOOS=darwin  GOARCH=amd64 $(GO) build -o $(MAC_AMD64_DIR)/$(CLI_BINARY)         $(CLI_DIR)
	GOOS=darwin  GOARCH=arm64 $(GO) build -o $(MAC_ARM64_DIR)/$(CLI_BINARY)         $(CLI_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -o $(WINDOWS_AMD64_DIR)/$(CLI_BINARY).exe $(CLI_DIR)

## release-migrate-memory: cross-compile migrate-memory for all target platforms
release-migrate-memory:
	mkdir -p $(LINUX_AMD64_DIR) $(LINUX_ARM64_DIR) $(MAC_AMD64_DIR) $(MAC_ARM64_DIR) $(WINDOWS_AMD64_DIR)
	GOOS=linux   GOARCH=amd64 $(GO) build -o $(LINUX_AMD64_DIR)/$(MIGRATE_BINARY)       $(MIGRATE_DIR)
	GOOS=linux   GOARCH=arm64 $(GO) build -o $(LINUX_ARM64_DIR)/$(MIGRATE_BINARY)       $(MIGRATE_DIR)
	GOOS=darwin  GOARCH=amd64 $(GO) build -o $(MAC_AMD64_DIR)/$(MIGRATE_BINARY)         $(MIGRATE_DIR)
	GOOS=darwin  GOARCH=arm64 $(GO) build -o $(MAC_ARM64_DIR)/$(MIGRATE_BINARY)         $(MIGRATE_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -o $(WINDOWS_AMD64_DIR)/$(MIGRATE_BINARY).exe $(MIGRATE_DIR)

## test: run all unit tests
test:
	$(GO) test ./tests/... -v

## run: build for the current platform and start the server using config.yaml from the project root
run: release-server
	AGENT_PATCHES_CONFIG=$(CONFIG) ./$(PLATFORM_DIR)/$(BINARY) $(ARGS)

## run-cli: build and run the CLI client (pass ARGS="<message>" to send a task)
run-cli: build-cli
	./$(TARGET_DIR)/$(CLI_BINARY) $(ARGS)

## run-central-ui: install dependencies (if needed) and start the central-ui React dev server,
## loading config from the .env file in the project root (see vite.config.js envDir)
run-central-ui:
	cd $(CENTRAL_UI_DIR) && [ -d node_modules ] || npm install
	cd $(CENTRAL_UI_DIR) && npm run dev

## run-central-backend: install dependencies (if needed) and start the central-backend server,
## loading config from the .env file in the project root
run-central-backend:
	cd $(CENTRAL_BACKEND_DIR) && [ -d node_modules ] || npm install
	cd $(CENTRAL_BACKEND_DIR) && DOTENV_CONFIG_PATH=$(CURDIR)/.env npm start

## deploy: deploy release builds to all hosts in inventory.csv (prompts before each host)
deploy:
	DEPLOY_CONFIRM=$(DEPLOY_CONFIRM) \
	WINDOWS_BINARY=$(WINDOWS_AMD64_DIR)/$(BINARY).exe \
	WINDOWS_CONFIG=$(INVENTORY_ROOT)/endpoint-server-config-windows.yaml \
	WINDOWS_RESPONSIBILITIES=$(CONFIG_DIR)/windows-responsibilities.yaml \
	LINUX_RESPONSIBILITIES=$(CONFIG_DIR)/linux-responsibilities.yaml \
	WINDOWS_SYSTEM_PROMPT=$(CONFIG_DIR)/windows-system-prompt.txt \
	LINUX_SYSTEM_PROMPT=$(CONFIG_DIR)/linux-system-prompt.txt \
	WINDOWS_BASELINE_PORTS=$(CONFIG_DIR)/windows-baseline-ports.csv \
	LINUX_BASELINE_PORTS=$(CONFIG_DIR)/linux-baseline-ports.csv \
	$(DEPLOY_SCRIPT) \
		$(INVENTORY_ROOT)/inventory.csv \
		$(LINUX_AMD64_DIR)/$(BINARY) \
		$(CURDIR)/deploy/linux/agent_patches.service \
		$(INVENTORY_ROOT)/endpoint-server-config-linux.yaml

## deploy-all: deploy to all hosts without per-host confirmation
deploy-all: DEPLOY_CONFIRM=0
deploy-all: deploy

## restart: restart the agent_patches daemon on all hosts in inventory.csv (no binary/config changes, no prompts)
restart:
	$(RESTART_SCRIPT) \
		$(INVENTORY_ROOT)/inventory.csv

## migrate-memory: one-time migration of network/disk-trend/incident/skill-state data out of the
## flat attrs bucket into their own memory domains, on every host in inventory.csv. Stops the
## service, runs migrate-memory, restarts it. Requires `make release-migrate-memory` first.
## Override with: make migrate-memory MIGRATE_PURGE=1 MIGRATE_DRY_RUN=1
migrate-memory:
	MIGRATE_CONFIRM=$(MIGRATE_CONFIRM) \
	MIGRATE_PURGE=$(MIGRATE_PURGE) \
	MIGRATE_DRY_RUN=$(MIGRATE_DRY_RUN) \
	WINDOWS_BINARY=$(WINDOWS_AMD64_DIR)/$(MIGRATE_BINARY).exe \
	$(MIGRATE_MEMORY_SCRIPT) \
		$(INVENTORY_ROOT)/inventory.csv \
		$(LINUX_AMD64_DIR)/$(MIGRATE_BINARY)

## migrate-memory-all: migrate-memory without per-host confirmation
migrate-memory-all: MIGRATE_CONFIRM=0
migrate-memory-all: migrate-memory

## fmt: format all Go source files
fmt:
	$(GO) fmt ./...

## vet: run the Go static analyser
vet:
	$(GO) vet ./...

## clean: remove the target directory and all build artifacts
clean:
	rm -rf $(TARGET_DIR)

## help: list available targets
help:
	@grep -E '^##' $(MAKEFILE_LIST) | sed 's/## /  /'
