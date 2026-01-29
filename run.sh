#!/bin/sh
# run.sh - Startup script for MCP Gateway (non-containerized mode)
# For containerized deployments, use run_containerized.sh instead.

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

# Check if running in a container - if so, redirect to containerized script
detect_containerized() {
    if [ -f /.dockerenv ] || grep -q 'docker\|containerd' /proc/self/cgroup 2>/dev/null; then
        log_info "Detected containerized environment, using run_containerized.sh"
        exec /app/run_containerized.sh "$@"
    fi
}

# Check Docker daemon accessibility
check_docker_socket() {
    local socket_path="${DOCKER_HOST:-/var/run/docker.sock}"
    socket_path="${socket_path#unix://}"
    
    if [ ! -S "$socket_path" ]; then
        log_error "Docker socket not found at $socket_path"
        log_error "The MCP Gateway requires Docker to spawn backend MCP servers."
        log_error "Make sure Docker is installed and running."
        exit 1
    fi
    
    if ! docker info > /dev/null 2>&1; then
        log_error "Docker daemon is not accessible"
        log_error "Make sure Docker is running and you have permission to access it."
        exit 1
    fi
    
    log_info "Docker daemon is accessible"
}

# Validate optional environment variables (warn if not set)
check_optional_env_vars() {
    local warnings=0
    
    if [ -z "$MCP_GATEWAY_PORT" ]; then
        log_warn "MCP_GATEWAY_PORT not set, using default: 8000"
        warnings=$((warnings + 1))
    fi
    
    if [ -z "$MCP_GATEWAY_DOMAIN" ]; then
        log_warn "MCP_GATEWAY_DOMAIN not set, using default: localhost"
        warnings=$((warnings + 1))
    fi
    
    if [ -z "$MCP_GATEWAY_API_KEY" ]; then
        log_warn "MCP_GATEWAY_API_KEY not set, API authentication disabled"
        warnings=$((warnings + 1))
    fi
    
    if [ $warnings -gt 0 ]; then
        log_warn "Some environment variables are not set. For production, set:"
        log_warn "  MCP_GATEWAY_PORT, MCP_GATEWAY_DOMAIN, MCP_GATEWAY_API_KEY"
    fi
}

# Set DOCKER_API_VERSION based on architecture
set_docker_api_version() {
    local arch=$(uname -m)
    if [ "$arch" = "arm64" ] || [ "$arch" = "aarch64" ]; then
        export DOCKER_API_VERSION=1.43
    else
        export DOCKER_API_VERSION=1.44
    fi
    log_info "Set DOCKER_API_VERSION=$DOCKER_API_VERSION for $arch"
}

# Main execution
main() {
    # Check for containerized environment first
    detect_containerized "$@"
    
    log_info "Starting MCP Gateway in non-containerized mode..."
    
    # Perform environment validation
    check_docker_socket
    check_optional_env_vars
    set_docker_api_version
    
    # Default values
    HOST="${HOST:-0.0.0.0}"
    PORT="${MCP_GATEWAY_PORT:-${PORT:-8000}}"
    CONFIG="${CONFIG}"
    ENV_FILE="${ENV_FILE:-.env}"
    MODE="${MODE:---routed}"
    
    # Build the command
    CMD="./awmg"
    FLAGS="$MODE --listen ${HOST}:${PORT}"
    
    # Only add --env flag if ENV_FILE is set and the file exists
    if [ -n "$ENV_FILE" ] && [ -f "$ENV_FILE" ]; then
        FLAGS="$FLAGS --env $ENV_FILE"
        log_info "Using environment file: $ENV_FILE"
    elif [ -n "$ENV_FILE" ] && [ "$ENV_FILE" != ".env" ]; then
        log_warn "ENV_FILE specified ($ENV_FILE) but file not found, skipping..."
    fi
    
    if [ -n "$CONFIG" ]; then
        if [ -f "$CONFIG" ]; then
            FLAGS="$FLAGS --config $CONFIG"
            log_info "Using config file: $CONFIG"
        else
            log_warn "CONFIG specified ($CONFIG) but file not found, using default config..."
            FLAGS="$FLAGS --config-stdin"
            CONFIG_JSON=$(cat <<EOF
{
    "mcpServers": {
        "github": {
            "type": "local",
            "container": "ghcr.io/github/github-mcp-server:latest",
            "env": {
                "GITHUB_PERSONAL_ACCESS_TOKEN": ""
            }
        },
        "fetch": {
            "type": "local",
            "container": "mcp/fetch"
        },
        "memory": {
            "type": "local",
            "container": "mcp/memory"
        }
    }
}
EOF
)
        fi
    else
        log_info "No config file specified, using default config..."
        FLAGS="$FLAGS --config-stdin"
        CONFIG_JSON=$(cat <<EOF
{
    "mcpServers": {
        "github": {
            "type": "local",
            "container": "ghcr.io/github/github-mcp-server:latest",
            "env": {
                "GITHUB_PERSONAL_ACCESS_TOKEN": ""
            }
        },
        "fetch": {
            "type": "local",
            "container": "mcp/fetch"
        },
        "memory": {
            "type": "local",
            "container": "mcp/memory"
        }
    }
}
EOF
)
    fi
    
    log_info "Starting MCPG Go server on port $PORT..."
    log_info "Command: $CMD $FLAGS"
    
    # Execute the command
    echo "$CONFIG_JSON" | $CMD $FLAGS
}

main "$@"