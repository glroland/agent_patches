BINARY     := patches-endpoint-server
CLI_BINARY := patches-cli
SRC_DIR    := ./endpoint-server
CLI_DIR    := ./cli
TARGET_DIR := target
GO         := go

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

# Directory for the central-ui React app.
CENTRAL_UI_DIR := $(CURDIR)/central-ui

# Directory for the central-backend Node app.
CENTRAL_BACKEND_DIR := $(CURDIR)/central-backend

# Ansible inventory used by `make deploy`.
INVENTORY := $(CURDIR)/../home-utils/admin/agent_patches/inventory.yaml
PLAYBOOK  := $(CURDIR)/deploy/linux/playbook.yml

.PHONY: install build build-server build-cli release release-server release-cli \
        test run run-cli run-central-ui run-central-backend deploy clean fmt vet help

## install: download and tidy all module dependencies
install:
	$(GO) mod download
	$(GO) mod tidy

## build: fmt + vet, then compile both binaries for the current platform into target/
build: fmt vet build-server build-cli

## build-server: compile the patches-endpoint-server binary for the current platform
build-server:
	mkdir -p $(TARGET_DIR)
	$(GO) build -o $(TARGET_DIR)/$(BINARY) $(SRC_DIR)

## build-cli: compile the patches-cli binary for the current platform
build-cli:
	mkdir -p $(TARGET_DIR)
	$(GO) build -o $(TARGET_DIR)/$(CLI_BINARY) $(CLI_DIR)

## release: cross-compile both binaries for all target platforms
release: clean release-server release-cli

## release-server: cross-compile patches-endpoint-server for all target platforms
release-server:
	mkdir -p $(LINUX_AMD64_DIR) $(LINUX_ARM64_DIR) $(MAC_AMD64_DIR) $(MAC_ARM64_DIR) $(WINDOWS_AMD64_DIR)
	GOOS=linux   GOARCH=amd64 $(GO) build -o $(LINUX_AMD64_DIR)/$(BINARY)          $(SRC_DIR)
	GOOS=linux   GOARCH=arm64 $(GO) build -o $(LINUX_ARM64_DIR)/$(BINARY)          $(SRC_DIR)
	GOOS=darwin  GOARCH=amd64 $(GO) build -o $(MAC_AMD64_DIR)/$(BINARY)            $(SRC_DIR)
	GOOS=darwin  GOARCH=arm64 $(GO) build -o $(MAC_ARM64_DIR)/$(BINARY)            $(SRC_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -o $(WINDOWS_AMD64_DIR)/$(BINARY).exe    $(SRC_DIR)

## release-cli: cross-compile patches-cli for all target platforms
release-cli:
	mkdir -p $(LINUX_AMD64_DIR) $(LINUX_ARM64_DIR) $(MAC_AMD64_DIR) $(MAC_ARM64_DIR) $(WINDOWS_AMD64_DIR)
	GOOS=linux   GOARCH=amd64 $(GO) build -o $(LINUX_AMD64_DIR)/$(CLI_BINARY)       $(CLI_DIR)
	GOOS=linux   GOARCH=arm64 $(GO) build -o $(LINUX_ARM64_DIR)/$(CLI_BINARY)       $(CLI_DIR)
	GOOS=darwin  GOARCH=amd64 $(GO) build -o $(MAC_AMD64_DIR)/$(CLI_BINARY)         $(CLI_DIR)
	GOOS=darwin  GOARCH=arm64 $(GO) build -o $(MAC_ARM64_DIR)/$(CLI_BINARY)         $(CLI_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -o $(WINDOWS_AMD64_DIR)/$(CLI_BINARY).exe $(CLI_DIR)

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
	cd $(CENTRAL_BACKEND_DIR) && DOTENV_CONFIG_PATH=$(CURDIR)/.env AGENT_INVENTORY_FILE=$(CURDIR)/inventory.csv npm start

## deploy: release and deploy to all hosts in the Ansible inventory
deploy:
	ANSIBLE_CONFIG=$(CURDIR)/deploy/linux/ansible.cfg ansible-playbook -K -i $(INVENTORY) $(PLAYBOOK) --ask-pass

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
