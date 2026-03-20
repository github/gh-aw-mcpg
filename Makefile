.PHONY: build lint test test-unit test-integration test-all test-serena test-serena-gateway test-container-proxy coverage test-ci format clean install release help agent-finished

# Default target
.DEFAULT_GOAL := help

# Binary name
BINARY_NAME=awmg

# Go and toolchain versions
GO_VERSION=1.25.0
GOLANGCI_LINT_VERSION=v2.8.0

# Build the CLI binary
build:
	@echo "Building $(BINARY_NAME)..."
	@go mod tidy
	@go build -o $(BINARY_NAME) .
	@echo "Build complete: $(BINARY_NAME)"

# Run all linters
lint:
	@echo "Running linters..."
	@go mod tidy
	@go vet ./...
	@echo "Running gofmt check..."
	@test -z "$$(gofmt -l .)" || (echo "The following files are not formatted:"; gofmt -l .; exit 1)
	@echo "Running golangci-lint..."
	@GOPATH=$$(go env GOPATH); \
	if [ -f "$$GOPATH/bin/golangci-lint" ]; then \
		$$GOPATH/bin/golangci-lint run --timeout=5m || echo "⚠ Warning: golangci-lint failed (compatibility issue with Go 1.25.0). Continuing with other checks..."; \
	elif command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m || echo "⚠ Warning: golangci-lint failed (compatibility issue with Go 1.25.0). Continuing with other checks..."; \
	else \
		echo "⚠ Warning: golangci-lint not found. Run 'make install' to install it."; \
		echo "  Skipping golangci-lint checks..."; \
	fi
	@echo "Linting complete!"

# Run unit tests only (no build required)
test-unit:
	@echo "Running unit tests..."
	@go mod tidy
	@go test -v ./internal/...

# Run all tests (unit + integration) — requires a built binary for integration tests
test-all: build
	@echo "Running all tests..."
	@go mod tidy
	@go test -v ./...

# Legacy target: run unit tests (for backward compatibility)
test: test-unit

# Run binary integration tests (requires built binary)
test-integration:
	@echo "Running binary integration tests..."
	@if [ ! -f $(BINARY_NAME) ]; then \
		echo "Binary not found. Building..."; \
		$(MAKE) build; \
	fi
	@go test -v ./test/integration/...

# Run container proxy integration tests (builds Docker image, requires Docker + GitHub token)
test-container-proxy:
	@echo "Running container proxy integration tests..."
	@echo "This will build a Docker image and test proxy mode with TLS."
	@echo "Requires: Docker daemon, GitHub token (GITHUB_TOKEN, GH_TOKEN, or gh auth login)"
	@echo ""
	@go test -v -tags=container -run TestContainerProxy -timeout 10m ./test/integration/...

# Run format, build, lint, and all tests (for agents before completion)
# Optimized: single go mod tidy, no redundant clean/vet/gofmt-check
agent-finished:
	@echo "Running agent-finished checks..."
	@echo ""
	@go mod tidy
	@echo "Formatting Go code..."
	@gofmt -w .
	@echo "Formatting complete!"
	@echo ""
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BINARY_NAME) .
	@echo "Build complete: $(BINARY_NAME)"
	@echo ""
	@echo "Running linters..."
	@go vet ./...
	@echo "Running golangci-lint..."
	@GOPATH=$$(go env GOPATH); \
	if [ -f "$$GOPATH/bin/golangci-lint" ]; then \
		$$GOPATH/bin/golangci-lint run --timeout=5m || echo "⚠ Warning: golangci-lint failed (compatibility issue with Go 1.25.0). Continuing with other checks..."; \
	elif command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m || echo "⚠ Warning: golangci-lint failed (compatibility issue with Go 1.25.0). Continuing with other checks..."; \
	else \
		echo "⚠ Warning: golangci-lint not found. Run 'make install' to install it."; \
		echo "  Skipping golangci-lint checks..."; \
	fi
	@echo "Linting complete!"
	@echo ""
	@echo "Running all tests..."
	@go test ./...
	@echo ""
	@echo "✓ All agent-finished checks passed!"

# Run unit tests with coverage
coverage:
	@echo "Running unit tests with coverage..."
	@go test -coverprofile=coverage.out ./internal/... 2>&1 | grep -vE "go: no such tool \"covdata\"|no such tool" | grep -v "^$$" || true
	@echo ""
	@echo "Coverage report:"
	@if [ -f coverage.out ]; then \
		go tool cover -func=coverage.out 2>/dev/null || echo "Note: go tool cover not available, but coverage data was collected"; \
	else \
		echo "Error: coverage.out not generated"; \
		exit 1; \
	fi
	@echo ""
	@echo "Coverage profile saved to coverage.out"
	@echo "To view HTML coverage report, run: go tool cover -html=coverage.out"

# Run Serena MCP Server tests (direct connection)
test-serena:
	@echo "Running Serena MCP Server tests (direct connection)..."
	@cd test/serena-mcp-tests && ./test_serena.sh
	@echo ""
	@echo "Test results saved to test/serena-mcp-tests/results/"
	@echo "For detailed analysis, see test/serena-mcp-tests/TEST_REPORT.md"

# Run Serena MCP Server tests through MCP Gateway
test-serena-gateway:
	@echo "Running Serena MCP Server tests (via MCP Gateway)..."
	@cd test/serena-mcp-tests && ./test_serena_via_gateway.sh
	@echo ""
	@echo "Test results saved to test/serena-mcp-tests/results-gateway/"
	@echo "Compare with direct connection results in test/serena-mcp-tests/results/"

# Run unit tests with coverage and JSON output for CI
test-ci:
	@echo "Running unit tests with coverage and JSON output..."
	@go mod tidy
	@go test -v -parallel=8 -timeout=3m -coverprofile=coverage.out -json ./internal/... | tee test-result-unit.json
	@echo "Test results saved to test-result-unit.json"
	@echo "Coverage profile saved to coverage.out"

# Format Go code
format:
	@echo "Formatting Go code..."
	@gofmt -w .
	@echo "Formatting complete!"

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -f $(BINARY_NAME)
	@rm -f coverage.out
	@rm -f test-result-unit.json
	@go mod tidy
	@go clean
	@echo "Clean complete!"

# Trigger the release workflow via workflow_dispatch
release:
	@echo "Triggering release workflow..."
	@# Check if first argument is provided
	@if [ -z "$(filter-out $@,$(MAKECMDGOALS))" ]; then \
		echo "Error: Bump type is required. Usage: make release patch|minor|major"; \
		exit 1; \
	fi
	@BUMP_TYPE="$(filter-out $@,$(MAKECMDGOALS))"; \
	if ! echo "$$BUMP_TYPE" | grep -qE '^(patch|minor|major)$$'; then \
		echo "Error: Bump type must be one of: patch, minor, major"; \
		exit 1; \
	fi; \
	echo "Bump type: $$BUMP_TYPE"; \
	echo ""; \
	echo "Fetching latest changes from remote..."; \
	git pull || { echo "Error: Failed to pull latest changes"; exit 1; }; \
	git fetch --tags --force || { echo "Error: Failed to fetch tags"; exit 1; }; \
	echo "✓ Latest changes and tags fetched"; \
	echo ""; \
	echo "Checking for uncommitted changes..."; \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "Error: You have uncommitted changes. Please commit or stash them before creating a release."; \
		git status --short; \
		exit 1; \
	fi; \
	echo "✓ Working directory is clean"; \
	echo ""; \
	LATEST_TAG=$$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' | sort -V | tail -1); \
	if [ -z "$$LATEST_TAG" ]; then \
		NEXT_VERSION="v0.0.1"; \
		if [ "$$BUMP_TYPE" = "minor" ]; then \
			NEXT_VERSION="v0.1.0"; \
		elif [ "$$BUMP_TYPE" = "major" ]; then \
			NEXT_VERSION="v1.0.0"; \
		fi; \
		echo "No existing tags found, will create: $$NEXT_VERSION"; \
	else \
		echo "Latest tag: $$LATEST_TAG"; \
		VERSION_NUM=$$(echo $$LATEST_TAG | sed 's/^v//'); \
		MAJOR=$$(echo $$VERSION_NUM | cut -d. -f1); \
		MINOR=$$(echo $$VERSION_NUM | cut -d. -f2); \
		PATCH=$$(echo $$VERSION_NUM | cut -d. -f3); \
		if [ "$$BUMP_TYPE" = "major" ]; then \
			MAJOR=$$((MAJOR + 1)); \
			MINOR=0; \
			PATCH=0; \
		elif [ "$$BUMP_TYPE" = "minor" ]; then \
			MINOR=$$((MINOR + 1)); \
			PATCH=0; \
		elif [ "$$BUMP_TYPE" = "patch" ]; then \
			PATCH=$$((PATCH + 1)); \
		fi; \
		NEXT_VERSION="v$$MAJOR.$$MINOR.$$PATCH"; \
		echo "Next version will be: $$NEXT_VERSION"; \
	fi; \
	echo ""; \
	printf "Do you want to trigger the release workflow? [Y/n] "; \
	read -r CONFIRM; \
	CONFIRM=$${CONFIRM:-Y}; \
	if [ "$$CONFIRM" != "Y" ] && [ "$$CONFIRM" != "y" ]; then \
		echo "Release cancelled."; \
		exit 1; \
	fi; \
	echo "Triggering release workflow with type: $$BUMP_TYPE"; \
	echo ""; \
	if ! command -v gh >/dev/null 2>&1; then \
		echo "Error: 'gh' CLI is not installed. Please install it from https://cli.github.com/"; \
		echo ""; \
		echo "Alternative: Manually trigger the workflow at:"; \
		echo "  https://github.com/github/gh-aw-mcpg/actions/workflows/release.lock.yml"; \
		echo "  Select 'Run workflow' and choose release type: $$BUMP_TYPE"; \
		exit 1; \
	fi; \
	gh workflow run release.lock.yml --ref main -f release_type=$$BUMP_TYPE || { \
		echo ""; \
		echo "Error: Failed to trigger workflow. Please check:"; \
		echo "  1. You are authenticated with 'gh auth login'"; \
		echo "  2. You have permission to trigger workflows"; \
		echo ""; \
		echo "Alternative: Manually trigger the workflow at:"; \
		echo "  https://github.com/github/gh-aw-mcpg/actions/workflows/release.lock.yml"; \
		exit 1; \
	}; \
	echo "✓ Release workflow triggered successfully"; \
	echo ""; \
	echo "The workflow will:"; \
	echo "  1. Run tests to ensure everything passes"; \
	echo "  2. Create and push tag: $$NEXT_VERSION"; \
	echo "  3. Build multi-platform binaries"; \
	echo "  4. Build and push Docker containers"; \
	echo "  5. Generate SBOMs"; \
	echo "  6. Create GitHub release with artifacts"; \
	echo ""; \
	echo "Monitor the release workflow at:"; \
	echo "  https://github.com/github/gh-aw-mcpg/actions/workflows/release.lock.yml"

# Prevent make from treating the argument as a target
%:
	@:

# Install required toolchains
install:
	@echo "Installing required toolchains..."
	@echo "Checking Go installation..."
	@if command -v go >/dev/null 2>&1; then \
		INSTALLED_VERSION=$$(go version | awk '{print $$3}' | sed 's/go//'); \
		echo "✓ Go $$INSTALLED_VERSION is installed"; \
		if [ "$$INSTALLED_VERSION" != "$(GO_VERSION)" ]; then \
			echo "⚠ Warning: Expected Go $(GO_VERSION), but found $$INSTALLED_VERSION"; \
			echo "  Visit https://go.dev/dl/ to install Go $(GO_VERSION)"; \
		fi; \
	else \
		echo "✗ Go is not installed"; \
		echo "  Visit https://go.dev/dl/ to install Go $(GO_VERSION)"; \
		exit 1; \
	fi
	@echo ""
	@echo "Checking golangci-lint installation..."
	@GOPATH=$$(go env GOPATH); \
	if [ -f "$$GOPATH/bin/golangci-lint" ] || command -v golangci-lint >/dev/null 2>&1; then \
		if [ -f "$$GOPATH/bin/golangci-lint" ]; then \
			INSTALLED_LINT_VERSION=$$($$GOPATH/bin/golangci-lint version 2>&1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo "unknown"); \
		else \
			INSTALLED_LINT_VERSION=$$(golangci-lint version 2>&1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo "unknown"); \
		fi; \
		echo "✓ golangci-lint v$$INSTALLED_LINT_VERSION is installed"; \
	else \
		echo "✗ golangci-lint is not installed"; \
		echo "  Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$GOPATH/bin $(GOLANGCI_LINT_VERSION); \
		echo "✓ golangci-lint $(GOLANGCI_LINT_VERSION) installed"; \
	fi
	@echo ""
	@echo "Installing Go dependencies..."
	@go mod download
	@go mod verify
	@echo "✓ Dependencies installed and verified"
	@echo ""
	@echo "✓ Toolchain installation complete!"

# Display help information
help:
	@echo "Available targets:"
	@echo "  build           - Build the CLI binary"
	@echo "  lint            - Run all linters (go vet, gofmt check, golangci-lint)"
	@echo "  test            - Run unit tests (no build required)"
	@echo "  test-unit       - Run unit tests (no build required)"
	@echo "  test-integration - Run binary integration tests (requires built binary)"
	@echo "  test-container-proxy - Run container proxy integration tests (requires Docker + GitHub token)"
	@echo "  test-all        - Run all tests (unit + integration)"
	@echo "  test-serena     - Run Serena MCP Server tests (direct connection)"
	@echo "  test-serena-gateway - Run Serena MCP Server tests (via MCP Gateway)"
	@echo "  coverage        - Run unit tests with coverage report"
	@echo "  test-ci         - Run unit tests with coverage and JSON output for CI"
	@echo "  format          - Format Go code using gofmt"
	@echo "  clean           - Clean build artifacts"
	@echo "  install         - Install required toolchains and dependencies"
	@echo "  release         - Trigger the release workflow (usage: make release patch|minor|major)"
	@echo "  agent-finished  - Run format, build, lint, and all tests (for agents before completion)"
	@echo "  help            - Display this help message"
