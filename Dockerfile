# Build stage
FROM golang:1.25-alpine AS builder

# Install ca-certificates for TLS connections
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy only necessary files for Go build
COPY go.mod go.sum ./
RUN go mod download

# Copy Go source files
COPY *.go ./
COPY internal ./internal

# Build argument for version (defaults to "dev")
ARG VERSION=dev

# Build the binary with version information
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=${VERSION}" -o awmg .

# Runtime stage - use specific Alpine version for better layer caching
FROM alpine:3.21

# Install only Docker CLI (bash removed, using POSIX sh)
RUN apk add --no-cache docker-cli

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/awmg .

# Copy only the containerized run script (run.sh not needed in container)
COPY run_containerized.sh .
RUN chmod +x run_containerized.sh

# Expose default HTTP port
EXPOSE 8000

# Use run_containerized.sh as entrypoint for container deployments
# This script requires stdin (-i flag) for JSON configuration
ENTRYPOINT ["/app/run_containerized.sh"]
