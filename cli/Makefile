.PHONY: build install clean test test-e2e test-battery test-usage dev

BINARY := 2nb
VERSION := $(shell cat ../VERSION | tr -d '\n')
BUILD_DIR := bin
MODULE := github.com/apresai/2ndbrain
LDFLAGS := -ldflags "-s -w -X $(MODULE)/internal/cli.Version=$(VERSION)"

# Pure-Go SQLite (modernc.org/sqlite): no CGO, no fts5 build tag — FTS5 is
# compiled into the driver (sqlite-vec ships in modernc's vec/ package, wired
# in the per-chunk vec0 PR). The shipped binary builds with CGO_ENABLED=0 so it
# cross-compiles to any GOOS/GOARCH from a single host with no C toolchain.
# Tests keep CGO on (the -race detector requires it); modernc is pure Go either way.

build:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/2nb

install: build
	cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)
	go clean

test:
	go test -race ./...

test-e2e:
	go test -race -v -run TestE2E -timeout 120s .

test-battery:
	go test -race -v -run TestBattery -timeout 180s .

# Usage suite: MCP write->query index round-trips (internal/mcp) + the runnable
# end-to-end usage battery (real binary + real mcp-server over stdio). The
# -run TestUsage regex catches both TestUsageMCP_* and TestUsageBattery_*.
# AI-gated steps skip without provider credentials.
test-usage:
	go test -race -v -run TestUsage -timeout 300s ./...

# Skill-usage eval: feeds the canonical SKILL.md to a real model and scores
# command selection (~$0.01/run). Opt-in only, never part of test-all.
test-skill-eval:
	env 2NB_SKILL_EVAL=1 go test -v -run TestSkillUsageSelection -timeout 600s ./internal/eval/

dev: build
	./$(BUILD_DIR)/$(BINARY) --help
