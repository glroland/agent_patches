BINARY     := patches-endpoint-server
CLI_BINARY := patches-cli
SRC_DIR    := ./endpoint-server
CLI_DIR    := ./cli
TARGET_DIR := target
GO         := go

# Release subdirectories per platform
LINUX_DIR   := $(TARGET_DIR)/linux-x86_64
MAC_DIR     := $(TARGET_DIR)/darwin-x86_64
WINDOWS_DIR := $(TARGET_DIR)/windows-x86_64

.PHONY: install build build-server build-cli release release-server release-cli \
        test run run-cli clean fmt lint vet help

## install: download and tidy all module dependencies
install:
	$(GO) mod download
	$(GO) mod tidy

## build: compile both binaries for the current platform into target/
build: build-server build-cli

## build-server: compile the patches-endpoint-server binary for the current platform
build-server:
	mkdir -p $(TARGET_DIR)
	$(GO) build -o $(TARGET_DIR)/$(BINARY) $(SRC_DIR)

## build-cli: compile the patches-cli binary for the current platform
build-cli:
	mkdir -p $(TARGET_DIR)
	$(GO) build -o $(TARGET_DIR)/$(CLI_BINARY) $(CLI_DIR)

## release: cross-compile both binaries for linux-x86_64, darwin-x86_64, and windows-x86_64
release: release-server release-cli

## release-server: cross-compile patches-endpoint-server for all target platforms
release-server:
	mkdir -p $(LINUX_DIR) $(MAC_DIR) $(WINDOWS_DIR)
	GOOS=linux   GOARCH=amd64 $(GO) build -o $(LINUX_DIR)/$(BINARY)          $(SRC_DIR)
	GOOS=darwin  GOARCH=amd64 $(GO) build -o $(MAC_DIR)/$(BINARY)            $(SRC_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -o $(WINDOWS_DIR)/$(BINARY).exe    $(SRC_DIR)

## release-cli: cross-compile patches-cli for all target platforms
release-cli:
	mkdir -p $(LINUX_DIR) $(MAC_DIR) $(WINDOWS_DIR)
	GOOS=linux   GOARCH=amd64 $(GO) build -o $(LINUX_DIR)/$(CLI_BINARY)       $(CLI_DIR)
	GOOS=darwin  GOARCH=amd64 $(GO) build -o $(MAC_DIR)/$(CLI_BINARY)         $(CLI_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -o $(WINDOWS_DIR)/$(CLI_BINARY).exe $(CLI_DIR)

## test: run all unit tests
test:
	$(GO) test ./tests/... -v

## run: build and start the server (pass ARGS="..." to supply arguments)
run: build-server
	./$(TARGET_DIR)/$(BINARY) $(ARGS)

## run-cli: build and run the CLI client (pass ARGS="<message>" to send a task)
run-cli: build-cli
	./$(TARGET_DIR)/$(CLI_BINARY) $(ARGS)

## fmt: format all Go source files
fmt:
	$(GO) fmt ./...

## vet: run the Go static analyser
vet:
	$(GO) vet ./...

## lint: fmt + vet combined
lint: fmt vet

## clean: remove the target directory and all build artifacts
clean:
	rm -rf $(TARGET_DIR)

## help: list available targets
help:
	@grep -E '^##' $(MAKEFILE_LIST) | sed 's/## /  /'
