.PHONY: build lint test coverage format clean help

# Default target
.DEFAULT_GOAL := help

# Binary name
BINARY_NAME=mcpg

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
	@echo "Clean complete!"

# Display help information
help:
	@echo "Available targets:"
	@echo "  build      - Build the CLI binary"
	@echo "  lint       - Run all linters (go vet, gofmt check)"
	@echo "  test       - Run all tests"
	@echo "  coverage   - Run tests with coverage report"
	@echo "  format     - Format Go code using gofmt"
	@echo "  clean      - Clean build artifacts"
	@echo "  help       - Display this help message"
