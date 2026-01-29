#!/bin/sh
# run_containerized.sh - Startup script for containerized MCP Gateway
# This script should be used when running the gateway inside a Docker container.
# It performs comprehensive validation of the container environment before starting.

set -e

# Detect if stderr is a TTY and colors should be enabled
# Respects NO_COLOR and DEBUG_COLORS environment variables
USE_COLORS=false
if [ -t 2 ] && [ -z "$NO_COLOR" ] && [ "${DEBUG_COLORS:-1}" != "0" ]; then
    USE_COLORS=true
fi

# Color output for better visibility (only when USE_COLORS=true)
if [ "$USE_COLORS" = true ]; then
    RED='\033[0;31m'
    YELLOW='\033[1;33m'
    GREEN='\033[0;32m'
    NC='\033[0m' # No Color
else
    RED=''
    YELLOW=''
    GREEN=''
    NC=''
fi

log_info() {
    printf "${GREEN}[INFO]${NC} %s\n" "$1" >&2
}

log_warn() {
    printf "${YELLOW}[WARN]${NC} %s\n" "$1" >&2
}

log_error() {
    printf "${RED}[ERROR]${NC} %s\n" "$1" >&2
}

# Validate container ID contains only hex characters (security check)
validate_container_id() {
    local cid="$1"
    if [ -z "$cid" ]; then
        return 1
    fi
    # Container IDs must be 12-64 hex characters only
    if ! echo "$cid" | grep -qE '^[a-f0-9]{12,64}$'; then
        log_warn "Invalid container ID format: $cid"
        return 1
    fi
    return 0
}

# Detect container ID from /proc/self/cgroup
get_container_id() {
    if [ -f /proc/self/cgroup ]; then
        # Try to extract container ID from cgroup v1 or v2 paths
        # Use grep directly on the file instead of cat | grep
        local cid=$(grep -oE '[0-9a-f]{12,64}' /proc/self/cgroup | head -1)
        if validate_container_id "$cid"; then
            echo "$cid"
            return 0
        fi
    fi
    
    # Fallback: check hostname (often set to container ID)
    if [ -f /.dockerenv ]; then
        local host_id=$(hostname)
        if validate_container_id "$host_id"; then
            echo "$host_id"
            return 0
        fi
    fi
    
    return 1
}

# Verify we're running in a container
verify_containerized() {
    if [ ! -f /.dockerenv ] && ! grep -q 'docker\|containerd' /proc/self/cgroup 2>/dev/null; then
        log_error "This script should only be run inside a Docker container."
        log_error "For non-containerized deployments, use run.sh instead."
        exit 1
    fi
    log_info "Running in containerized environment"
}

# Check Docker daemon accessibility
check_docker_socket() {
    local socket_path="${DOCKER_HOST:-/var/run/docker.sock}"
    socket_path="${socket_path#unix://}"
    
    if [ ! -S "$socket_path" ]; then
        log_error "Docker socket not found at $socket_path"
        log_error "Mount the Docker socket: -v /var/run/docker.sock:/var/run/docker.sock"
        exit 1
    fi
    
    if ! docker info > /dev/null 2>&1; then
        log_error "Docker daemon is not accessible"
        log_error "Ensure the Docker socket is properly mounted and accessible"
        exit 1
    fi
    
    log_info "Docker daemon is accessible"
}

# Validate required environment variables
check_required_env_vars() {
    local missing_vars=""
    
    if [ -z "$MCP_GATEWAY_PORT" ]; then
        missing_vars="${missing_vars:+$missing_vars }MCP_GATEWAY_PORT"
    fi
    
    if [ -z "$MCP_GATEWAY_DOMAIN" ]; then
        missing_vars="${missing_vars:+$missing_vars }MCP_GATEWAY_DOMAIN"
    fi
    
    if [ -z "$MCP_GATEWAY_API_KEY" ]; then
        missing_vars="${missing_vars:+$missing_vars }MCP_GATEWAY_API_KEY"
    fi
    
    if [ -n "$missing_vars" ]; then
        log_error "Required environment variables not set:"
        for var in $missing_vars; do
            log_error "  - $var"
        done
        log_error ""
        log_error "Set these when running the container:"
        log_error "  docker run -e MCP_GATEWAY_PORT=8080 -e MCP_GATEWAY_DOMAIN=localhost -e MCP_GATEWAY_API_KEY=your-key ..."
        exit 1
    fi
    
    log_info "Required environment variables are set"
}

# Validate port mapping using docker inspect
validate_port_mapping() {
    local container_id="$1"
    local port="$MCP_GATEWAY_PORT"
    
    if ! validate_container_id "$container_id"; then
        log_warn "Cannot validate port mapping: container ID invalid or unknown"
        return 0
    fi
    
    local port_mapping=$(docker inspect --format '{{json .NetworkSettings.Ports}}' "$container_id" 2>/dev/null || echo "{}")
    
    if ! echo "$port_mapping" | grep -q "\"${port}/tcp\""; then
        log_error "Port $port is not exposed from the container"
        log_error "Add port mapping: -p <host_port>:$port"
        exit 1
    fi
    
    if ! echo "$port_mapping" | grep -q '"HostPort"'; then
        log_error "Port $port is exposed but not mapped to a host port"
        log_error "Add port mapping: -p <host_port>:$port"
        exit 1
    fi
    
    log_info "Port $port is properly mapped"
}

# Validate stdin is interactive (requires -i flag)
validate_stdin_interactive() {
    local container_id="$1"
    
    if ! validate_container_id "$container_id"; then
        log_warn "Cannot validate stdin: container ID invalid or unknown"
        return 0
    fi
    
    local stdin_open=$(docker inspect --format '{{.Config.OpenStdin}}' "$container_id" 2>/dev/null || echo "unknown")
    
    if [ "$stdin_open" != "true" ]; then
        log_error "Container was not started with -i flag"
        log_error "Stdin is required for passing JSON configuration"
        log_error "Start container with: docker run -i ..."
        exit 1
    fi
    
    log_info "Stdin is interactive"
}

# Validate container mounts and environment
validate_container_config() {
    local container_id="$1"
    
    if ! validate_container_id "$container_id"; then
        log_warn "Cannot validate container config: container ID invalid or unknown"
        return 0
    fi
    
    # Check for Docker socket mount
    local mounts=$(docker inspect --format '{{json .Mounts}}' "$container_id" 2>/dev/null || echo "[]")
    
    if ! echo "$mounts" | grep -q 'docker.sock'; then
        log_warn "Docker socket mount not detected in container mounts"
        log_warn "The gateway needs Docker access to spawn backend MCP servers"
    else
        log_info "Docker socket is mounted"
    fi
}

# Validate log directory is mounted for persistent logging
validate_log_directory_mount() {
    local container_id="$1"
    local log_dir="${MCP_GATEWAY_LOG_DIR:-/tmp/gh-aw/mcp-logs}"
    
    if ! validate_container_id "$container_id"; then
        log_warn "Cannot validate log directory mount: container ID invalid or unknown"
        return 0
    fi
    
    # Check if log directory is a mount point
    local mounts=$(docker inspect --format '{{json .Mounts}}' "$container_id" 2>/dev/null || echo "[]")
    
    if ! echo "$mounts" | grep -q "$log_dir"; then
        log_warn "Log directory $log_dir is not mounted"
        log_warn "Gateway logs will not persist outside the container"
        log_warn "Add mount: -v /path/on/host:$log_dir"
    else
        log_info "Log directory $log_dir is mounted"
    fi
}

# Set DOCKER_API_VERSION based on architecture and Docker daemon requirements
set_docker_api_version() {
    # First, check what the Docker daemon requires as minimum API version
    local min_api=$(docker version --format '{{.Server.MinAPIVersion}}' 2>/dev/null || echo "")
    
    if [ -n "$min_api" ]; then
        # Use the daemon's minimum API version to ensure compatibility
        export DOCKER_API_VERSION="$min_api"
        log_info "Set DOCKER_API_VERSION=$DOCKER_API_VERSION (daemon minimum)"
    else
        # Fallback: set based on architecture
        local arch=$(uname -m)
        if [ "$arch" = "arm64" ] || [ "$arch" = "aarch64" ]; then
            export DOCKER_API_VERSION=1.44
        else
            export DOCKER_API_VERSION=1.44
        fi
        log_info "Set DOCKER_API_VERSION=$DOCKER_API_VERSION for $arch (fallback)"
    fi
}

# Detect host IP and configure host.docker.internal DNS mapping
configure_host_dns() {
    log_info "Detecting host IP for container networking..."
    
    local HOST_IP=""
    
    # Method 1: Try to get the primary network interface IP (not loopback)
    HOST_IP=$(ip -4 addr show 2>/dev/null | grep 'inet ' | awk '{print $2}' | cut -d/ -f1 | grep -v '^127\.' | head -1)
    if [ -n "$HOST_IP" ]; then
        log_info "Method 1 (primary interface): $HOST_IP"
    fi
    
    # Method 2: Try default gateway IP
    if [ -z "$HOST_IP" ]; then
        HOST_IP=$(ip route 2>/dev/null | grep default | awk '{print $3}' | head -1)
        if [ -n "$HOST_IP" ]; then
            log_info "Method 2 (default gateway): $HOST_IP"
        fi
    fi
    
    # Method 3: Try docker0 bridge IP
    if [ -z "$HOST_IP" ]; then
        HOST_IP=$(ip addr show docker0 2>/dev/null | grep 'inet ' | awk '{print $2}' | cut -d/ -f1)
        if [ -n "$HOST_IP" ]; then
            log_info "Method 3 (docker0 bridge): $HOST_IP"
        fi
    fi
    
    # Method 4: Last resort - common Docker bridge IP
    if [ -z "$HOST_IP" ]; then
        HOST_IP="172.17.0.1"
        log_info "Method 4 (fallback): $HOST_IP"
    fi
    
    log_info "Using host IP: $HOST_IP"
    
    # Add host.docker.internal mapping to /etc/hosts
    # Check if the entry already exists to avoid duplicates
    if ! grep -q "host.docker.internal" /etc/hosts 2>/dev/null; then
        log_info "Adding host.docker.internal mapping to /etc/hosts"
        echo "$HOST_IP   host.docker.internal" >> /etc/hosts
        log_info "DNS mapping configured: $HOST_IP -> host.docker.internal"
    else
        log_info "host.docker.internal already exists in /etc/hosts"
    fi
}

# Build command line arguments
build_command_args() {
    local host="${MCP_GATEWAY_HOST:-0.0.0.0}"
    local port="$MCP_GATEWAY_PORT"
    local mode="${MCP_GATEWAY_MODE:---routed}"
    local log_dir="${MCP_GATEWAY_LOG_DIR:-/tmp/gh-aw/mcp-logs}"
    
    local flags="$mode --listen ${host}:${port} --config-stdin --log-dir ${log_dir}"
    
    # Add env file if specified and exists
    if [ -n "$ENV_FILE" ] && [ -f "$ENV_FILE" ]; then
        flags="$flags --env $ENV_FILE"
        log_info "Using environment file: $ENV_FILE"
    fi
    
    log_info "Gateway will listen on ${host}:${port}"
    log_info "Log directory: ${log_dir}"
    
    echo "$flags"
}

# Main execution
main() {
    log_info "Starting MCP Gateway in containerized mode..."
    
    # Verify we're in a container
    verify_containerized
    
    # Get container ID
    CONTAINER_ID=$(get_container_id) || true
    if [ -n "$CONTAINER_ID" ]; then
        log_info "Container ID: ${CONTAINER_ID:0:12}..."
    else
        log_warn "Could not determine container ID"
    fi
    
    # Perform environment validation
    check_docker_socket
    check_required_env_vars
    set_docker_api_version
    
    # Perform container-specific validation
    if [ -n "$CONTAINER_ID" ]; then
        validate_port_mapping "$CONTAINER_ID"
        validate_stdin_interactive "$CONTAINER_ID"
        validate_container_config "$CONTAINER_ID"
        validate_log_directory_mount "$CONTAINER_ID"
    fi
    
    # Configure DNS for host.docker.internal
    configure_host_dns
    
    # Build command
    FLAGS=$(build_command_args)
    CMD="./awmg"
    
    log_info "Command: $CMD $FLAGS"
    log_info "Waiting for JSON configuration on stdin..."
    log_info ""
    log_info "IMPORTANT: Configuration must be provided via stdin."
    log_info "No default configuration is used in containerized mode."
    log_info ""
    
    # Execute - stdin will be passed through
    exec $CMD $FLAGS
}

main "$@"
