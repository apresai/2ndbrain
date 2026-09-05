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

# The release gate and CI must agree, and neither may depend on what happens to
# be sitting in the environment. Provider tests skip on CAPABILITY, so a machine
# carrying real credentials runs a strictly larger suite than CI does, against
# real endpoints, at real latency. On 2026-09-05 that difference pushed
# internal/ai and internal/cli past Go's 10m per-package timeout and failed a
# release whose CI run of this same target was green in two minutes.
#
# So `test` is hermetic, and each part is load-bearing:
#   - the unset AWS_* vars remove an ambient bearer token or SigV4 identity;
#   - 2NB_BEDROCK_SKIP_KEYCHAIN stops the macOS Keychain supplying one instead;
#   - HOME is redirected because ~/.config/2nb/bedrock.json sits ABOVE the
#     Keychain in the auth precedence and would put the credentials straight
#     back;
#   - AWS_EC2_METADATA_DISABLED matters only once the others work: with no
#     credentials the SDK falls through to the EC2 metadata service, which is
#     unreachable off EC2 and burns its full timeout on EVERY probe, turning a
#     3ms bad_credentials into a 10s hang.
# Omitting any one of them silently restores the non-determinism.
#
# `make test-live` is the opt-in that runs against whatever this machine can
# actually reach. It is the one that spends money.
# Go derives GOCACHE, GOMODCACHE and GOPATH from HOME, so redirecting HOME alone
# throws away the build and module caches and makes every run a cold download
# plus a full recompile. Resolve them from the REAL environment first and pass
# them back in: the isolation is meant to hide 2nb's credentials, not Go's cache.
TEST_HOME := $(CURDIR)/build/test-home
REAL_GOCACHE := $(shell go env GOCACHE)
REAL_GOMODCACHE := $(shell go env GOMODCACHE)
REAL_GOPATH := $(shell go env GOPATH)

test:
	@mkdir -p $(TEST_HOME)
	env -u AWS_BEARER_TOKEN_BEDROCK -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY \
	    -u AWS_SESSION_TOKEN -u AWS_PROFILE -u OPENROUTER_API_KEY \
	    AWS_EC2_METADATA_DISABLED=true 2NB_BEDROCK_SKIP_KEYCHAIN=1 \
	    HOME=$(TEST_HOME) \
	    GOCACHE=$(REAL_GOCACHE) GOMODCACHE=$(REAL_GOMODCACHE) GOPATH=$(REAL_GOPATH) \
	    go test -race ./...

test-live:
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
