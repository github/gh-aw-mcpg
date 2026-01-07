.PHONY: build lint test coverage test-ci format clean install release help agent-finished

# Default target
.DEFAULT_GOAL := help

# Binary name
BINARY_NAME=awmg

# Go and toolchain versions
GO_VERSION=1.25.0
GOLANGCI_LINT_VERSION=v2.2.0

# Build the CLI binary
build:
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BINARY_NAME) .
	@echo "Build complete: $(BINARY_NAME)"

# Run all linters
lint:
	@echo "Running linters..."
	@go vet ./...
	@echo "Running gofmt check..."
	@test -z "$$(gofmt -l .)" || (echo "The following files are not formatted:"; gofmt -l .; exit 1)
	@echo "Linting complete!"

# Run all tests
test:
	@echo "Running tests..."
	@go test -v ./...

# Run format, build, lint, and test (for agents before completion)
agent-finished:
	@echo "Running agent-finished checks..."
	@echo ""
	@$(MAKE) format
	@echo ""
	@$(MAKE) build
	@echo ""
	@$(MAKE) lint
	@echo ""
	@$(MAKE) test
	@echo ""
	@echo "✓ All agent-finished checks passed!"

# Run tests with coverage
coverage:
	@echo "Running tests with coverage..."
	@go test -coverprofile=coverage.out ./... 2>&1 | grep -vE "go: no such tool \"covdata\"|no such tool" | grep -v "^$$" || true
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

# Run tests with coverage and JSON output for CI
test-ci:
	@echo "Running tests with coverage and JSON output..."
	@go test -v -parallel=8 -timeout=3m -coverprofile=coverage.out -json ./... | tee test-result-unit.json
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
	@echo "Clean complete!"

# Create and push a release tag
release:
	@echo "Creating release tag..."
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
	LATEST_TAG=$$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' | sort -V | tail -1); \
	if [ -z "$$LATEST_TAG" ]; then \
		echo "No existing tags found, starting from v0.0.0"; \
		LATEST_TAG="v0.0.0"; \
	else \
		echo "Latest tag: $$LATEST_TAG"; \
	fi; \
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
	NEW_VERSION="v$$MAJOR.$$MINOR.$$PATCH"; \
	echo ""; \
	echo "New version will be: $$NEW_VERSION"; \
	echo ""; \
	printf "Do you want to create and push this tag? [Y/n] "; \
	read -r CONFIRM; \
	CONFIRM=$${CONFIRM:-Y}; \
	if [ "$$CONFIRM" != "Y" ] && [ "$$CONFIRM" != "y" ]; then \
		echo "Release cancelled."; \
		exit 1; \
	fi; \
	echo "Creating and pushing tag: $$NEW_VERSION"; \
	git tag -a "$$NEW_VERSION" -m "Release $$NEW_VERSION"; \
	git push origin "$$NEW_VERSION"; \
	echo "✓ Tag $$NEW_VERSION created and pushed"; \
	echo "✓ Release workflow will be triggered automatically"; \
	echo ""; \
	echo "Monitor the release workflow at:"; \
	echo "  https://github.com/githubnext/gh-aw-mcpg/actions/workflows/release.lock.yml"

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
	@echo "  lint            - Run all linters (go vet, gofmt check)"
	@echo "  test            - Run all tests"
	@echo "  coverage        - Run tests with coverage report"
	@echo "  test-ci         - Run tests with coverage and JSON output for CI"
	@echo "  format          - Format Go code using gofmt"
	@echo "  clean           - Clean build artifacts"
	@echo "  install         - Install required toolchains and dependencies"
	@echo "  release         - Create and push a release tag (usage: make release patch|minor|major)"
	@echo "  agent-finished  - Run format, build, lint, and test (for agents before completion)"
	@echo "  help            - Display this help message"
