.PHONY: build lint test coverage test-ci format clean install help

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
	@echo "  build      - Build the CLI binary"
	@echo "  lint       - Run all linters (go vet, gofmt check)"
	@echo "  test       - Run all tests"
	@echo "  coverage   - Run tests with coverage report"
	@echo "  test-ci    - Run tests with coverage and JSON output for CI"
	@echo "  format     - Format Go code using gofmt"
	@echo "  clean      - Clean build artifacts"
	@echo "  install    - Install required toolchains and dependencies"
	@echo "  help       - Display this help message"
