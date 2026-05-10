BINARY     := patches-server
CLI_BINARY := patches-cli
SRC_DIR    := ./endpoint-server
CLI_DIR    := ./cli
TARGET_DIR := target
GO         := go

.PHONY: install build build-server build-cli test run run-cli clean fmt lint vet help

## install: download and tidy all module dependencies
install:
	$(GO) mod download
	$(GO) mod tidy

## build: compile both the server and CLI binaries into target/
build: build-server build-cli

## build-server: compile the patches-server binary
build-server:
	mkdir -p $(TARGET_DIR)
	$(GO) build -o $(TARGET_DIR)/$(BINARY) $(SRC_DIR)

## build-cli: compile the patches-cli client binary
build-cli:
	mkdir -p $(TARGET_DIR)
	$(GO) build -o $(TARGET_DIR)/$(CLI_BINARY) $(CLI_DIR)

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
