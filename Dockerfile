# Build stage
FROM golang:1.26.4-alpine3.22@sha256:727cfc3c40be55cd1bc9a4a059406b28a059857e3be752aa9d09531e12c20c56 AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build argument for version (defaults to "dev")
ARG VERSION=dev

# Build the binary with version information
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=${VERSION}" -o awmg .

# Runtime stage
FROM alpine:3.22.5@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce

# Install Docker CLI and bash for launching backend MCP servers
RUN apk add --no-cache docker-cli bash

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/awmg .

# Copy run scripts
COPY run.sh .
COPY run_containerized.sh .
RUN chmod +x run.sh run_containerized.sh

# Copy pre-built WASM guard into the image (must exist before docker build)
# The gateway discovers guards from /guards/{serverID}/*.wasm
COPY guards/github-guard/github-guard-rust.wasm /guards/github/00-github-guard.wasm

# Expose default HTTP port
EXPOSE 8000

# Use run_containerized.sh as entrypoint for container deployments
# This script requires stdin (-i flag) for JSON configuration
ENTRYPOINT ["/app/run_containerized.sh"]
