.PHONY: build install clean test test-e2e test-battery test-usage dev

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

test-e2e:
	go test -tags fts5 -race -v -run TestE2E -timeout 120s .

test-battery:
	go test -tags fts5 -race -v -run TestBattery -timeout 180s .

# Usage suite: MCP write->query index round-trips (internal/mcp) + the runnable
# end-to-end usage battery (real binary + real mcp-server over stdio). The
# -run TestUsage regex catches both TestUsageMCP_* and TestUsageBattery_*.
# AI-gated steps skip without provider credentials.
test-usage:
	go test -tags fts5 -race -v -run TestUsage -timeout 300s ./...

dev: build
	./$(BUILD_DIR)/$(BINARY) --help
