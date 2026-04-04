.PHONY: build install clean test dev

BINARY := 2nb
VERSION := $(shell cat ../VERSION | tr -d '\n')
BUILD_DIR := bin
MODULE := github.com/apresai/2ndbrain
LDFLAGS := -ldflags "-s -w -X $(MODULE)/internal/cli.Version=$(VERSION)"

export CGO_ENABLED=1

build:
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -tags fts5 -o $(BUILD_DIR)/$(BINARY) ./cmd/2nb

install: build
	cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)
	go clean

test:
	go test -race -tags fts5 ./...

dev: build
	./$(BUILD_DIR)/$(BINARY) --help
