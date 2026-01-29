# Build stage
FROM golang:1.25-alpine AS builder

# Install ca-certificates for TLS connections
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .
RUN go mod tidy

# Build argument for version (defaults to "dev")
ARG VERSION=dev

# Build the binary with version information
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=${VERSION}" -o awmg .

# Runtime stage
FROM alpine:latest

# Install Docker CLI for launching backend MCP servers
# Note: bash removed to reduce image size - scripts use POSIX sh
RUN apk add --no-cache docker-cli

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/awmg .

# Copy run scripts
COPY run.sh .
COPY run_containerized.sh .
RUN chmod +x run.sh run_containerized.sh

# Expose default HTTP port
EXPOSE 8000

# Use run_containerized.sh as entrypoint for container deployments
# This script requires stdin (-i flag) for JSON configuration
ENTRYPOINT ["/app/run_containerized.sh"]
