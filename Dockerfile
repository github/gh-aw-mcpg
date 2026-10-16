# Podman build stage
FROM golang:1.26.4-alpine3.24@sha256:3ad57304ad93bbec8548a0437ad9e06a455660655d9af011d58b993f6f615648 AS podman-builder

ARG PODMAN_VERSION=6.0.2
ARG PODMAN_SHA256=0895a541aeb7aa8e99133ed2b328c1bb40fd397b7c3b01e083396c90e8628756

RUN apk add --no-cache \
    bash \
    btrfs-progs-dev \
    build-base \
    coreutils \
    gpgme-dev \
    libassuan-dev \
    libseccomp-dev \
    linux-headers \
    sqlite-dev

WORKDIR /src
RUN wget -q "https://github.com/containers/podman/archive/refs/tags/v${PODMAN_VERSION}.tar.gz" -O podman.tar.gz \
    && echo "${PODMAN_SHA256}  podman.tar.gz" | sha256sum -c - \
    && tar -xzf podman.tar.gz --strip-components=1 \
    && rm podman.tar.gz \
    && BUILDTAGS="exclude_graphdriver_devicemapper seccomp apparmor libsqlite3" \
       make -j1 podman rootlessport

# Podman 6 requires the Netavark 2 network schema, which is newer than the
# version packaged by Alpine 3.24.
FROM golang:1.26.4-alpine3.24@sha256:3ad57304ad93bbec8548a0437ad9e06a455660655d9af011d58b993f6f615648 AS network-builder

ARG NETAVARK_VERSION=2.0.0
ARG NETAVARK_SHA256=031aeeacc930382e8635d40a885798eff1da164dfcf9024b698f822e5995d9c8
ARG AARDVARK_DNS_VERSION=2.0.0
ARG AARDVARK_DNS_SHA256=d3f5d6b3be3c2d80e8257fb9467e34ff104f299474427979454034dca6dc88cc

RUN apk add --no-cache build-base cargo protoc

WORKDIR /src/netavark
RUN wget -q "https://github.com/containers/netavark/archive/refs/tags/v${NETAVARK_VERSION}.tar.gz" -O netavark.tar.gz \
    && echo "${NETAVARK_SHA256}  netavark.tar.gz" | sha256sum -c - \
    && tar -xzf netavark.tar.gz --strip-components=1 \
    && rm netavark.tar.gz \
    && cargo build --release --locked --bin netavark

WORKDIR /src/aardvark-dns
RUN wget -q "https://github.com/containers/aardvark-dns/archive/refs/tags/v${AARDVARK_DNS_VERSION}.tar.gz" -O aardvark-dns.tar.gz \
    && echo "${AARDVARK_DNS_SHA256}  aardvark-dns.tar.gz" | sha256sum -c - \
    && tar -xzf aardvark-dns.tar.gz --strip-components=1 \
    && rm aardvark-dns.tar.gz \
    && cargo build --release --locked --bin aardvark-dns

# Gateway build stage
FROM golang:1.26.4-alpine3.24@sha256:3ad57304ad93bbec8548a0437ad9e06a455660655d9af011d58b993f6f615648 AS builder

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
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

# Keep both supported container runtimes in one image. Podman is built above
# because Alpine's packaged version still embeds vulnerable Go dependencies.
RUN apk add --no-cache \
    bash \
    catatonit \
    conmon \
    containers-common \
    crun \
    docker-cli=29.8.2-r1 \
    fuse-overlayfs \
    gpgme \
    libgcc \
    nftables \
    passt \
    shadow-subids \
    sqlite-libs \
    && sed -i 's/^driver = "overlay"$/driver = "vfs"/' /etc/containers/storage.conf

COPY --from=podman-builder /src/bin/podman /usr/bin/podman
COPY --from=podman-builder /src/bin/rootlessport /usr/libexec/podman/rootlessport
COPY --from=network-builder /src/netavark/target/release/netavark /usr/libexec/podman/netavark
COPY --from=network-builder /src/aardvark-dns/target/release/aardvark-dns /usr/libexec/podman/aardvark-dns

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
