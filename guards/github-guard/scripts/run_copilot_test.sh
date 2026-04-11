#!/bin/bash
# Copilot test runner for GitHub Guard
# This script starts the gateway container with the guard and runs Copilot CLI
#
# ============================================================================
# USAGE
# ============================================================================
#
# ./run_copilot_test.sh [MODE]
#
# Modes:
#   yolo           - No guard, no DIFC (plain gateway passthrough)
#   all            - Allow all repos with approved integrity floor
#   public-only    - Filtering private data (public repos only)
#   owner-only     - Filtering private data outside owner scope
#   repo-only      - Filtering data outside repo scope (lpcox/github-guard)
#   prefix-only    - Filtering data outside repo prefix scope (lpcox/git-*)
#   multi-only     - Filtering using multiple repo scopes (lpcox/git-* + lpcox/github-guard)
#
# Default mode is 'yolo' if not specified.
#
# ============================================================================
# PREREQUISITES
# ============================================================================
#
# 1. GitHub Personal Access Token (PAT)
#    Create a PAT at: https://github.com/settings/tokens
#    Required scopes: repo, read:org, read:user
#
#    Save it in a .env file in the project root:
#      echo 'GITHUB_TOKEN=ghp_your_token_here' > .env
#
# 2. GitHub Container Registry (ghcr.io) Authentication
#    echo $GITHUB_TOKEN | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
#
# 3. Copilot CLI Installation
#    Install: brew install --cask copilot-cli
#
#    Default mode: Copilot CLI uses GitHub CLI authentication.
#    Make sure you're logged in: gh auth login
#
#    BYOK/Offline mode: set a provider URL in .env (or export it) to route
#    Copilot's LLM calls through your own provider instead of GitHub's models.
#    gh auth login is NOT required when a provider URL is configured.
#
#    Example .env additions for Ollama:
#      COPILOT_PROVIDER_BASE_URL=http://localhost:11434
#      COPILOT_MODEL=llama3.2
#
#    Supported provider env vars (can be set in .env or exported in shell):
#      COPILOT_PROVIDER_BASE_URL - LLM base URL  (enables offline mode)
#      COPILOT_PROVIDER_TYPE     - openai | azure | anthropic  (optional)
#      COPILOT_PROVIDER_API_KEY  - API key for the provider    (optional)
#      COPILOT_MODEL             - model name / deployment ID  (optional)
#
# ============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Parse mode argument (default: yolo)
MODE="${1:-yolo}"

# Validate mode
case "$MODE" in
  yolo|all|public-only|owner-only|repo-only|prefix-only|multi-only|lockdown)
        ;;
    *)
        echo -e "${RED}ERROR: Invalid mode '$MODE'${NC}"
        echo ""
        echo "Valid modes:"
        echo "  yolo           - No guard, no DIFC extensions"
        echo "  all            - Allow all repos with approved integrity floor"
        echo "  public-only    - Filtering private data (public repos only)"
        echo "  owner-only     - Filtering private data outside owner scope"
        echo "  repo-only      - Filtering data outside repo scope (lpcox/github-guard)"
        echo "  prefix-only    - Filtering data outside repo prefix scope (lpcox/git-*)"
        echo "  multi-only     - Filtering using multiple repo scopes (lpcox/git-* + lpcox/github-guard)"
        exit 1
        ;;
esac

SERVER_GUARD_POLICIES_JSON="{}"

# Markdown code fence helper (three backticks; avoids command-substitution issues in heredocs)
FENCE='```'

# Configuration
GATEWAY_IMAGE="${GATEWAY_IMAGE:-local/gh-aw-mcpg}"
GITHUB_MCP_IMAGE="${GITHUB_MCP_IMAGE:-ghcr.io/github/github-mcp-server:latest}"
GATEWAY_PORT="${GATEWAY_PORT:-18080}"
GATEWAY_API_KEY="${GATEWAY_API_KEY:-test-api-key-12345}"
CONTAINER_NAME="github-guard-copilot-test"
COPILOT_WORK_DIR="/tmp/copilot"

# DIFC scope - the repository to protect
DIFC_SCOPE="${DIFC_SCOPE:-lpcox/github-guard}"
ALLOW_OWNER="${ALLOW_OWNER:-lpcox}"
DIFC_OWNER="${DIFC_SCOPE%%/*}"
DIFC_REPO="${DIFC_SCOPE##*/}"
ALLOW_OWNER_POLICY="$(printf '%s' "$ALLOW_OWNER" | tr '[:upper:]' '[:lower:]')"
DIFC_OWNER_POLICY="$(printf '%s' "$DIFC_OWNER" | tr '[:upper:]' '[:lower:]')"
DIFC_REPO_POLICY="$(printf '%s' "$DIFC_REPO" | tr '[:upper:]' '[:lower:]')"

# AllowOnly policy JSON (override via env if policy shape/values change)
if [ -z "${ALLOW_ONLY_PUBLIC_POLICY:-}" ]; then
  ALLOW_ONLY_PUBLIC_POLICY='{"allow-only":{"repos":"public","min-integrity": "none"}}'
fi
if [ -z "${ALLOW_ONLY_ALL_POLICY:-}" ]; then
  ALLOW_ONLY_ALL_POLICY='{"allow-only":{"repos":"all","min-integrity": "none"}}'
fi
if [ -z "${ALLOW_ONLY_OWNER_POLICY:-}" ]; then
  ALLOW_ONLY_OWNER_POLICY="{\"allow-only\":{\"repos\":[\"${ALLOW_OWNER_POLICY}/*\"],\"min-integrity\":\"none\"}}"
fi
if [ -z "${ALLOW_ONLY_REPO_POLICY:-}" ]; then
  ALLOW_ONLY_REPO_POLICY="{\"allow-only\":{\"repos\":[\"${DIFC_OWNER_POLICY}/${DIFC_REPO_POLICY}\"],\"min-integrity\":\"none\"}}"
fi
if [ -z "${ALLOW_ONLY_PREFIX_POLICY:-}" ]; then
  ALLOW_ONLY_PREFIX_POLICY='{"allow-only":{"repos":["lpcox/git-*"],"min-integrity": "none"}}'
fi
if [ -z "${ALLOW_ONLY_MULTI_POLICY:-}" ]; then
  ALLOW_ONLY_MULTI_POLICY='{"allow-only":{"repos":["lpcox/git-*","lpcox/github-guard"],"min-integrity": "merged"}}'
fi

validate_json_policy() {
    local policy_name="$1"
    local policy_value="$2"

    if ! python -c "import json,sys; json.loads(sys.argv[1])" "$policy_value" >/dev/null 2>&1; then
        echo -e "${RED}ERROR: Invalid JSON in ${policy_name}${NC}"
        echo ""
        echo "Value: $policy_value"
        echo ""
        echo "Provide valid JSON for ${policy_name}."
        exit 1
    fi
}

validate_owner_scope_policy() {
  local policy_name="$1"
  local policy_value="$2"

  if ! python -c 'import json,re,sys
policy=json.loads(sys.argv[1])
repos=((policy.get("allow-only") or {}).get("repos"))
ok=isinstance(repos,list) and len(repos)==1 and isinstance(repos[0],str) and re.fullmatch(r"[a-z0-9_-]{1,39}/\*", repos[0])
sys.exit(0 if ok else 1)
' "$policy_value" >/dev/null 2>&1; then
    echo -e "${RED}ERROR: ${policy_name} must use allow-only.repos as [\"<owner>/*\"] in lowercase${NC}"
    echo ""
    echo "Value: $policy_value"
    echo ""
    echo "Example: {\"allow-only\":{\"repos\":[\"octocat/*\"],\"min-integrity\":\"approved\"}}"
    exit 1
  fi
}

validate_json_policy "ALLOW_ONLY_PUBLIC_POLICY" "$ALLOW_ONLY_PUBLIC_POLICY"
validate_json_policy "ALLOW_ONLY_ALL_POLICY" "$ALLOW_ONLY_ALL_POLICY"
validate_json_policy "ALLOW_ONLY_OWNER_POLICY" "$ALLOW_ONLY_OWNER_POLICY"
validate_json_policy "ALLOW_ONLY_REPO_POLICY" "$ALLOW_ONLY_REPO_POLICY"
validate_json_policy "ALLOW_ONLY_PREFIX_POLICY" "$ALLOW_ONLY_PREFIX_POLICY"
validate_json_policy "ALLOW_ONLY_MULTI_POLICY" "$ALLOW_ONLY_MULTI_POLICY"
validate_owner_scope_policy "ALLOW_ONLY_OWNER_POLICY" "$ALLOW_ONLY_OWNER_POLICY"

echo "=========================================="
echo "GitHub Guard Copilot Test Runner"
echo "=========================================="
echo -e "Mode: ${BLUE}${MODE}${NC}"
echo ""

# Check for Copilot CLI
if ! command -v copilot &> /dev/null; then
    echo -e "${RED}ERROR: Copilot CLI not found!${NC}"
    echo ""
    echo "Install Copilot CLI:"
    echo "  brew install --cask copilot-cli"
    exit 1
fi

echo -e "${GREEN}✓${NC} Copilot CLI found"

# Check for .env file and load credentials + BYOK config
ENV_FILE="$PROJECT_ROOT/.env"
if [ ! -f "$ENV_FILE" ]; then
    echo -e "${RED}ERROR: .env file not found!${NC}"
    echo ""
    echo "Create a .env file in the project root with your GitHub token:"
    echo "  echo 'GITHUB_TOKEN=ghp_your_token_here' > $ENV_FILE"
    echo ""
    echo "For BYOK (offline) mode, also add:"
    echo "  COPILOT_PROVIDER_BASE_URL=http://localhost:11434"
    echo "  COPILOT_MODEL=llama3.2"
    echo ""
    echo -e "${YELLOW}WARNING: Never commit .env to git!${NC}"
    exit 1
fi

# Load GitHub token and BYOK provider config from .env.
# Environment variables already exported in the shell take precedence over .env values.
GITHUB_TOKEN="${GITHUB_TOKEN:-}"
COPILOT_PROVIDER_BASE_URL="${COPILOT_PROVIDER_BASE_URL:-}"
COPILOT_PROVIDER_TYPE="${COPILOT_PROVIDER_TYPE:-}"
COPILOT_PROVIDER_API_KEY="${COPILOT_PROVIDER_API_KEY:-}"
while IFS='=' read -r key value; do
    # Skip comments and empty lines
    [[ "$key" =~ ^#.*$ ]] && continue
    [[ -z "$key" ]] && continue

    # Remove quotes from value
    value=$(echo "$value" | sed -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//")

    case "$key" in
        GITHUB_TOKEN|GITHUB_PERSONAL_ACCESS_TOKEN)
            [ -z "$GITHUB_TOKEN" ] && GITHUB_TOKEN="$value" ;;
        COPILOT_PROVIDER_BASE_URL)
            [ -z "$COPILOT_PROVIDER_BASE_URL" ] && COPILOT_PROVIDER_BASE_URL="$value" ;;
        COPILOT_PROVIDER_TYPE)
            [ -z "$COPILOT_PROVIDER_TYPE" ] && COPILOT_PROVIDER_TYPE="$value" ;;
        COPILOT_PROVIDER_API_KEY)
            [ -z "$COPILOT_PROVIDER_API_KEY" ] && COPILOT_PROVIDER_API_KEY="$value" ;;
        COPILOT_MODEL)
            # Allow .env to set a default model; CLI export or MODEL_AGENT_COPILOT takes precedence
            [ -z "${MODEL_AGENT_COPILOT:-}" ] && MODEL_AGENT_COPILOT="$value" ;;
    esac
done < "$ENV_FILE"

if [ -z "$GITHUB_TOKEN" ]; then
    echo -e "${RED}ERROR: GitHub token not found in .env file!${NC}"
    echo ""
    echo "Add one of these to your .env file:"
    echo "  GITHUB_TOKEN=ghp_your_token_here"
    exit 1
fi

echo -e "${GREEN}✓${NC} GitHub token loaded from .env"

# Determine offline / BYOK mode.
# When a custom provider URL is set, enable COPILOT_OFFLINE so Copilot does not
# phone home to GitHub's model API. The MCP servers still use GITHUB_TOKEN.
COPILOT_OFFLINE="${COPILOT_OFFLINE:-}"
if [ -n "$COPILOT_PROVIDER_BASE_URL" ]; then
    COPILOT_OFFLINE="true"
    echo -e "${GREEN}✓${NC} BYOK provider configured: ${BLUE}${COPILOT_PROVIDER_BASE_URL}${NC}"
    [ -n "$COPILOT_PROVIDER_TYPE" ] && echo -e "  Provider type: ${BLUE}${COPILOT_PROVIDER_TYPE}${NC}"
    echo -e "  Offline mode:  ${BLUE}enabled${NC} (COPILOT_OFFLINE=true)"
fi
echo ""

# Check GitHub CLI authentication.
# Skipped in offline (BYOK) mode because Copilot authenticates via the provider key instead.
if [ "${COPILOT_OFFLINE:-}" != "true" ]; then
    if ! gh auth status &> /dev/null 2>&1; then
        echo -e "${RED}ERROR: GitHub CLI not authenticated!${NC}"
        echo ""
        echo "Copilot CLI uses GitHub CLI authentication."
        echo "Run the following to authenticate:"
        echo "  gh auth login"
        echo ""
        echo "Alternatively, configure a BYOK provider in .env to use offline mode:"
        echo "  COPILOT_PROVIDER_BASE_URL=http://localhost:11434"
        echo "  COPILOT_MODEL=llama3.2"
        exit 1
    fi
    echo -e "${GREEN}✓${NC} GitHub CLI authenticated"
    echo ""
fi

echo ""

# Check Docker is running
if ! docker info > /dev/null 2>&1; then
    echo -e "${RED}ERROR: Docker is not running${NC}"
    exit 1
fi

echo -e "${GREEN}✓${NC} Docker is running"
echo ""

# Pull required container images
PLAYWRIGHT_MCP_IMAGE="${PLAYWRIGHT_MCP_IMAGE:-mcp/playwright:latest}"

echo "Checking required container images..."

ensure_image() {
  local image="$1"
  local description="$2"

  if docker image inspect "$image" > /dev/null 2>&1; then
    echo -e "${GREEN}✓${NC} Using local $description: $image"
    return 0
  fi

  echo -e "${YELLOW}Local $description not found. Pulling: $image${NC}"
  if docker pull "$image" > /dev/null 2>&1; then
    echo -e "${GREEN}✓${NC} Pulled $description: $image"
  else
    echo -e "${YELLOW}⚠${NC} Failed to pull $description, continuing if runtime can resolve it: $image"
  fi
}

ensure_image "$GATEWAY_IMAGE" "gateway image"
ensure_image "$GITHUB_MCP_IMAGE" "GitHub MCP image"
ensure_image "$PLAYWRIGHT_MCP_IMAGE" "Playwright MCP image"
echo ""

# Create Copilot working directory
mkdir -p "$COPILOT_WORK_DIR/logs"
echo -e "${GREEN}✓${NC} Created Copilot work directory: $COPILOT_WORK_DIR"
echo ""

# Stop any existing test container
docker rm -f "$CONTAINER_NAME" 2>/dev/null || true

echo "Starting gateway container..."
echo "  Image: $GATEWAY_IMAGE"
echo "  Port: $GATEWAY_PORT"
if [ "$MODE" != "yolo" ] && [ "$MODE" != "lockdown" ]; then
  echo "  Guard discovery dir: /guards (server: github, file: 00-github-guard.wasm)"
else
  echo "  Guard: disabled"
fi
echo "  Mode: $MODE"
echo ""

# Build environment variables based on mode
DOCKER_ENV_ARGS=(
    -e MCP_GATEWAY_PORT="$GATEWAY_PORT"
    -e MCP_GATEWAY_DOMAIN=localhost
    -e MCP_GATEWAY_API_KEY="$GATEWAY_API_KEY"
    -e DOCKER_API_VERSION=1.44
    -e DEBUG="*"
)

  DOCKER_VOLUME_ARGS=(
    -v /var/run/docker.sock:/var/run/docker.sock
    -v "/tmp:/tmp:rw"
  )

  # WASM guard is baked into the gateway image at /guards/github/00-github-guard.wasm
  # The entrypoint auto-detects it when MCP_GATEWAY_WASM_GUARDS_DIR is set or /guards exists.
  # For non-yolo/lockdown modes, explicitly enable guard discovery.
  if [ "$MODE" != "yolo" ] && [ "$MODE" != "lockdown" ]; then
    DOCKER_ENV_ARGS+=(
      -e MCP_GATEWAY_WASM_GUARDS_DIR="/guards"
    )
  else
    # In yolo/lockdown modes, suppress baked-in guard discovery
    DOCKER_ENV_ARGS+=(
      -e MCP_GATEWAY_WASM_GUARDS_DIR=""
    )
  fi

# Forward optional DIFC sink-server-IDs when the caller sets it
if [[ -n "${MCP_GATEWAY_GUARDS_SINK_SERVER_IDS:-}" ]]; then
    DOCKER_ENV_ARGS+=(-e MCP_GATEWAY_GUARDS_SINK_SERVER_IDS="$MCP_GATEWAY_GUARDS_SINK_SERVER_IDS")
    echo -e "${BLUE}Forwarding MCP_GATEWAY_GUARDS_SINK_SERVER_IDS=${MCP_GATEWAY_GUARDS_SINK_SERVER_IDS}${NC}"
fi

case "$MODE" in
    yolo)
        # No guard, no DIFC - plain gateway mode
        echo -e "${YELLOW}Mode: yolo - No guard, no DIFC (development)${NC}"
        ;;
  all)
    # DIFC with global scope and approved integrity floor
    echo -e "${BLUE}Mode: all - Allow all repos with approved integrity floor${NC}"
    SERVER_GUARD_POLICIES_JSON="$ALLOW_ONLY_ALL_POLICY"
    DOCKER_ENV_ARGS+=(
      -e DEBUG='server:unified,guard:wasm'
    )
    ;;
    public-only)
        # DIFC with integrity filtering only (public repos only)
        echo -e "${BLUE}Mode: public-only - Filtering private data (public repos only)${NC}"
      SERVER_GUARD_POLICIES_JSON="$ALLOW_ONLY_PUBLIC_POLICY"
        DOCKER_ENV_ARGS+=(
            -e DEBUG='server:unified,guard:wasm'
        )
        ;;
    owner-only)
      # DIFC with integrity filtering scoped to a single owner
      echo -e "${BLUE}Mode: owner-only - Filtering data outside owner scope (${ALLOW_OWNER})${NC}"
      SERVER_GUARD_POLICIES_JSON="$ALLOW_ONLY_OWNER_POLICY"
      DOCKER_ENV_ARGS+=(
        -e DEBUG='server:unified,guard:wasm'
      )
      ;;
    repo-only)
        # DIFC with integrity filtering scoped to a single repository
        echo -e "${BLUE}Mode: repo-only - Filtering data outside repo scope (${DIFC_SCOPE})${NC}"
      SERVER_GUARD_POLICIES_JSON="$ALLOW_ONLY_REPO_POLICY"
        DOCKER_ENV_ARGS+=(
        -e DEBUG='server:unified,guard:wasm'
        )
        ;;
      prefix-only)
        # DIFC with integrity filtering scoped to owner/repo prefix
        echo -e "${BLUE}Mode: prefix-only - Filtering data outside repo prefix scope (lpcox/git-*)${NC}"
        SERVER_GUARD_POLICIES_JSON="$ALLOW_ONLY_PREFIX_POLICY"
        DOCKER_ENV_ARGS+=(
        -e DEBUG='server:unified,guard:wasm'
        )
        ;;
      multi-only)
        # DIFC with integrity filtering scoped to multiple repo entries
        echo -e "${BLUE}Mode: multi-only - Filtering using multiple repo scopes (lpcox/git-* + lpcox/github-guard)${NC}"
        SERVER_GUARD_POLICIES_JSON="$ALLOW_ONLY_MULTI_POLICY"
        DOCKER_ENV_ARGS+=(
        -e DEBUG='server:unified,guard:wasm'
        )
        ;;
    lockdown)
        # Yolo mode + GitHub MCP lockdown flag
        echo -e "${GREEN}Mode: lockdown - Yolo mode with GitHub MCP --lockdown-mode${NC}"
        ;;
esac
echo ""

# Build JSON config based on mode
if [ "$MODE" = "yolo" ] || [ "$MODE" = "lockdown" ]; then
    # Yolo mode: no guard, no extensions - includes Playwright for unrestricted web access
        GITHUB_MCP_ENTRYPOINT_ARGS_JSON=""
        if [ "$MODE" = "lockdown" ]; then
            GITHUB_MCP_ENTRYPOINT_ARGS_JSON=",
          \"entrypointArgs\": [\"stdio\", \"--lockdown-mode\"]"
        fi

    GATEWAY_CONFIG=$(cat <<EOF
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "container": "$GITHUB_MCP_IMAGE",
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "$GITHUB_TOKEN"
            }${GITHUB_MCP_ENTRYPOINT_ARGS_JSON}
    },
    "playwright": {
      "type": "stdio",
      "container": "$PLAYWRIGHT_MCP_IMAGE",
      "env": {
        "PLAYWRIGHT_MCP_HEADLESS": "true"
      }
    }
  },
  "gateway": {
    "port": $GATEWAY_PORT,
    "domain": "localhost",
    "apiKey": "$GATEWAY_API_KEY"
  }
}
EOF
)
else
    # All other modes: guard is discovered from MCP_GATEWAY_WASM_GUARDS_DIR
    GATEWAY_CONFIG=$(cat <<EOF
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "container": "$GITHUB_MCP_IMAGE",
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "$GITHUB_TOKEN"
      },
      "guard-policies": $SERVER_GUARD_POLICIES_JSON
    },
    "playwright": {
      "type": "stdio",
      "container": "$PLAYWRIGHT_MCP_IMAGE",
      "env": {
        "PLAYWRIGHT_MCP_HEADLESS": "true"
      }
    }
  },
  "gateway": {
    "port": $GATEWAY_PORT,
    "domain": "localhost",
    "apiKey": "$GATEWAY_API_KEY"
  }
}
EOF
)
fi

# Build CLI args for the gateway.
# The entrypoint script doesn't translate env vars to CLI flags, so we override it.
USE_ROUTED_MODE="${USE_ROUTED_MODE:-1}"

# Extra CLI args beyond what the entrypoint handles via env vars
GATEWAY_CLI_ARGS=()

if [ "$USE_ROUTED_MODE" != "1" ]; then
  DOCKER_ENV_ARGS+=(-e MCP_GATEWAY_MODE="--unified")
fi

GATEWAY_GUARDS_MODE="disabled"
ACTIVE_ALLOW_ONLY_POLICY=""

# Add DIFC flags for non-yolo modes
if [ "$MODE" != "yolo" ] && [ "$MODE" != "lockdown" ]; then
    GATEWAY_CLI_ARGS+=(
    )
    
    # DIFC mode is guard-managed at runtime (do not set via CLI)
    case "$MODE" in
      all)
        GATEWAY_GUARDS_MODE="guard-managed"
      ACTIVE_ALLOW_ONLY_POLICY="$ALLOW_ONLY_ALL_POLICY"
        ;;
        public-only)
            GATEWAY_GUARDS_MODE="guard-managed"
        ACTIVE_ALLOW_ONLY_POLICY="$ALLOW_ONLY_PUBLIC_POLICY"
            ;;
        owner-only)
            GATEWAY_GUARDS_MODE="guard-managed"
        ACTIVE_ALLOW_ONLY_POLICY="$ALLOW_ONLY_OWNER_POLICY"
            ;;
        repo-only)
            GATEWAY_GUARDS_MODE="guard-managed"
        ACTIVE_ALLOW_ONLY_POLICY="$ALLOW_ONLY_REPO_POLICY"
            ;;
        prefix-only)
          GATEWAY_GUARDS_MODE="guard-managed"
        ACTIVE_ALLOW_ONLY_POLICY="$ALLOW_ONLY_PREFIX_POLICY"
          ;;
        multi-only)
          GATEWAY_GUARDS_MODE="guard-managed"
        ACTIVE_ALLOW_ONLY_POLICY="$ALLOW_ONLY_MULTI_POLICY"
          ;;
    esac
    
    # Add session labels based on mode
    # public-only: no session labels (empty)
fi

echo "Gateway runtime settings:"
echo "  Test mode: $MODE"
echo "  Guards mode: $GATEWAY_GUARDS_MODE"
if [ -n "$ACTIVE_ALLOW_ONLY_POLICY" ]; then
  echo "  AllowOnly policy: $ACTIVE_ALLOW_ONLY_POLICY"
fi
echo "  Enabled gateway flags:"
for ((i=0; i<${#GATEWAY_CLI_ARGS[@]}; i++)); do
    arg="${GATEWAY_CLI_ARGS[$i]}"
    if [[ "$arg" == --* ]]; then
        next="${GATEWAY_CLI_ARGS[$((i + 1))]:-}"
        if [[ -n "$next" && "$next" != --* ]]; then
            echo "    $arg=$next"
        else
            echo "    $arg"
        fi
    fi
done
echo ""

# Start the gateway container in the background with stdin
(docker run -i --rm --name "$CONTAINER_NAME" \
    "${DOCKER_ENV_ARGS[@]}" \
  "${DOCKER_VOLUME_ARGS[@]}" \
  -p "$GATEWAY_PORT:$GATEWAY_PORT" \
    "$GATEWAY_IMAGE" "${GATEWAY_CLI_ARGS[@]}" <<< "$GATEWAY_CONFIG"
) > "$COPILOT_WORK_DIR/gateway.log" 2>&1 &
GATEWAY_PID=$!

# Wait for gateway to be ready
echo "Waiting for gateway to start..."
MAX_WAIT=30
WAITED=0
while [ $WAITED -lt $MAX_WAIT ]; do
    if curl -s "http://localhost:$GATEWAY_PORT/health" > /dev/null 2>&1; then
        echo -e "${GREEN}✓${NC} Gateway is ready"
        break
    fi
    # Check if the process died
    if ! kill -0 $GATEWAY_PID 2>/dev/null; then
        echo -e "${RED}ERROR: Gateway process died${NC}"
        echo "Container logs:"
        cat "$COPILOT_WORK_DIR/gateway.log"
        exit 1
    fi
    sleep 1
    WAITED=$((WAITED + 1))
    echo -n "."
done
echo ""

if [ $WAITED -ge $MAX_WAIT ]; then
    echo -e "${RED}ERROR: Gateway failed to start within ${MAX_WAIT}s${NC}"
    echo ""
    echo "Container logs:"
    cat "$COPILOT_WORK_DIR/gateway.log"
    kill $GATEWAY_PID 2>/dev/null || true
    docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
    exit 1
fi

# Create MCP config for Copilot
# Copilot CLI supports HTTP MCP servers with type/url/headers format
MCP_CONFIG_FILE="$COPILOT_WORK_DIR/mcp-config.json"

# In restricted DIFC modes, hard-restrict GitHub tools to read-only calls.
GITHUB_MCP_TOOLS_JSON='["*"]'
if [ "$MODE" = "public-only" ] || [ "$MODE" = "owner-only" ] || [ "$MODE" = "repo-only" ] || [ "$MODE" = "prefix-only" ] || [ "$MODE" = "multi-only" ]; then
    GITHUB_MCP_TOOLS_JSON='[
        "get_commit",
        "get_file_contents",
        "get_label",
        "get_latest_release",
        "get_me",
        "get_release_by_tag",
        "get_tag",
        "issue_read",
        "list_branches",
        "list_commits",
        "list_issues",
        "list_pull_requests",
        "list_releases",
        "list_tags",
        "pull_request_read",
        "search_code",
        "search_issues",
        "search_pull_requests",
        "search_repositories",
        "search_users"
      ]'
fi

cat > "$MCP_CONFIG_FILE" <<EOF
{
  "mcpServers": {
    "github": {
      "type": "http",
      "url": "http://localhost:$GATEWAY_PORT/mcp/github",
      "headers": {
        "Authorization": "$GATEWAY_API_KEY"
      },
      "tools": $GITHUB_MCP_TOOLS_JSON
    },
    "playwright": {
      "type": "http",
      "url": "http://localhost:$GATEWAY_PORT/mcp/playwright",
      "headers": {
        "Authorization": "$GATEWAY_API_KEY"
      },
      "tools": ["*"]
    }
  }
}
EOF
echo -e "${GREEN}✓${NC} Created MCP config: $MCP_CONFIG_FILE"
echo ""

# Build mode-aware default prompt (can be overridden with COPILOT_PROMPT_FILE)
GENERATED_PROMPT_FILE="$COPILOT_WORK_DIR/copilot-prompt-${MODE}.txt"
case "$MODE" in
  yolo)
    cat > "$GENERATED_PROMPT_FILE" <<EOF
You are testing GitHub MCP behavior through the MCP Gateway. Do not use the 'gh' cli tool. Only use the tools provided by the MCP servers (github and playwright).

## Test Configuration
- **Mode**: yolo
- **Guard**: disabled
- **DIFC**: disabled

## Test Plan

Execute EVERY read-only GitHub MCP call currently available in this environment.

### Part 1: Global/User Read-Only Calls (run once)
Run each of these once:
1. get_me
2. search_users (query: "octocat", perPage: 5)
3. search_repositories (query: "github-guard", perPage: 10)
4. search_issues (query: "bug", perPage: 5)
5. search_pull_requests (query: "is:open", perPage: 5)

### Part 2: Repo-Scoped Read-Only Calls (run for BOTH repos)
For each tool below, execute it twice:
- private target: owner=lpcox repo=github-guard
- public target: owner=octocat repo=Hello-World

Repo-scoped read-only tools to test:
1. list_issues (perPage: 5)
2. issue_read (discover issue_number from list_issues first; skip only if repo has no issues)
3. list_pull_requests (perPage: 5)
4. pull_request_read (discover pullNumber from list_pull_requests first; skip only if repo has no PRs)
5. list_commits (perPage: 5)
6. get_commit (discover sha from list_commits first; skip only if no commits)
7. get_file_contents (path: "README.md")
8. list_branches (perPage: 10)
9. list_tags (perPage: 10)
10. get_tag (use strategy below)
11. list_releases (perPage: 5)
12. get_latest_release (skip only if repo has no releases)
13. get_release_by_tag (discover tag from list_releases first; skip only if no releases)
14. get_label (name: "bug")
15. search_code (query: "repo:<owner>/<repo> README", perPage: 5)

### get_tag Practical Strategy
- Use list_tags to pick up to 3 candidate tags per repo.
- Call get_tag on each candidate until one succeeds.
- If all attempts return 404 Not Found, mark get_tag as expected skip (lightweight tags), not failure.
- Only mark get_tag failed for non-404 errors (auth, permission, transport, etc.).

### Part 3: Expected Behavior Checks
- All read-only calls should succeed when data exists.
- No DIFC denial/filtering behavior should occur in yolo mode.
- Private and public repositories should both be accessible.

### Part 4: Final Report (required)
Provide:
1. A checklist of every read-only tool above with status for private and public calls.
2. Any skips and exact reason (e.g., no releases/tags in that repo).
3. Any unexpected errors observed.
4. A final PASS/FAIL for "yolo mode allows unrestricted read-only GitHub MCP access".
EOF
    ;;
  all)
    cat > "$GENERATED_PROMPT_FILE" <<EOF
You are testing GitHub Guard in all mode through the MCP Gateway.

In all mode, all repos and all objects within the repos are accessible (min-integrity == none).

## Test Configuration
- **Mode**: all
- **DIFC Mode**: filter
- **AllowOnly Policy**: ${ALLOW_ONLY_ALL_POLICY}

## Expected Behavior
**ALL MODE**: All repos and all objects within the repos are accessible (min-integrity == none).

- ✅ **All Repositories**: Full access to ALL data when it exists (private and public)
- ✅ **Global/User APIs**: Should work without any filtering
- ✅ **No Repository Restrictions**: Access should be unrestricted across all repositories

## Test Plan

Execute EVERY read-only GitHub MCP call currently available in this environment.

### Part 1: Global/User Read-Only Calls (run once)
Run each of these once:
1. get_me
2. search_users (query: "octocat", perPage: 5)
3. search_repositories (query: "github-guard", perPage: 10)
4. search_issues (query: "bug", perPage: 5)
5. search_pull_requests (query: "is:open", perPage: 5)

### Part 2: Repo-Scoped Read-Only Calls (run for BOTH repos)
For each tool below, execute it twice:
- private target: owner=lpcox repo=github-guard
- public target: owner=octocat repo=Hello-World

Repo-scoped read-only tools to test:
1. list_issues (perPage: 5)
2. issue_read (discover issue_number from list_issues first; skip only if repo has no issues)
3. list_pull_requests (perPage: 5)
4. pull_request_read (discover pullNumber from list_pull_requests first; skip only if repo has no PRs)
5. list_commits (perPage: 5)
6. get_commit (discover sha from list_commits first; skip only if no commits)
7. get_file_contents (path: "README.md")
8. list_branches (perPage: 10)
9. list_tags (perPage: 10)
10. get_tag (use strategy below)
11. list_releases (perPage: 5)
12. get_latest_release (skip only if repo has no releases)
13. get_release_by_tag (discover tag from list_releases first; skip only if no releases)
14. get_label (name: "bug")
15. search_code (query: "repo:<owner>/<repo> README", perPage: 5)

### get_tag Practical Strategy
- Use list_tags to pick up to 3 candidate tags per repo.
- Call get_tag on each candidate until one succeeds.
- If all attempts return 404 Not Found, mark get_tag as expected skip (lightweight tags), not failure.
- Only mark get_tag failed for non-404 errors (auth, permission, transport, etc.).

### Part 3: Expected Behavior Validation
**Global/User APIs** (expected: FULL ACCESS):
- get_me: Should work normally
- search_users: Should work normally
- search_repositories: Should return all accessible repositories without filtering
- search_issues: Should return all accessible issues without filtering
- search_pull_requests: Should return all accessible PRs without filtering

**All Repositories** (expected: FULL ACCESS):
- Both private (${DIFC_SCOPE}) and public (octocat/Hello-World) repositories should be fully accessible
- All repo-scoped calls should return complete data when it exists
- No filtering should occur in any repository

## Report Template (required)

Use this exact format for your final report:

${FENCE}
# GitHub Guard All Mode Test Results

## Test Configuration
- **Mode**: all
- **Policy**: ${ALLOW_ONLY_ALL_POLICY}
- **Private Repository**: ${DIFC_SCOPE}
- **Public Repository**: octocat/Hello-World

## Global/User API Results (Expected: FULL ACCESS)
| Tool | Result | Status | Notes |
|------|--------|--------|-------|
| get_me | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_users | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_repositories | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_issues | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_pull_requests | [result] | ✅ PASS / ❌ FAIL | [reason] |

## Repo-Scoped API Results
| Tool | Private Repo (${DIFC_SCOPE}) | Public Repo (octocat/Hello-World) | Status |
|------|-------------------------------------|-------------------------------------|--------|
| list_issues | [result] | [result] | ✅ PASS / ❌ FAIL |
| issue_read | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_pull_requests | [result] | [result] | ✅ PASS / ❌ FAIL |
| pull_request_read | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_commits | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_commit | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_file_contents | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_branches | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_tags | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_tag | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_releases | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_latest_release | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_release_by_tag | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_label | [result] | [result] | ✅ PASS / ❌ FAIL |
| search_code | [result] | [result] | ✅ PASS / ❌ FAIL |

## Summary
- **Global APIs Working**: [X/5] ✅ / ❌
- **Private Repository Access**: [X/15] ✅ / ❌  
- **Public Repository Access**: [X/15] ✅ / ❌
- **Overall Result**: ✅ PASS / ❌ FAIL

## Final Assessment
[Detailed explanation of whether all mode provides unrestricted access to all repositories and data]
${FENCE}
EOF
    ;;
  lockdown)
    cat > "$GENERATED_PROMPT_FILE" <<EOF
You are testing GitHub MCP behavior through the MCP Gateway. Do not use the 'gh' cli tool. Only use the tools provided by the MCP servers (github and playwright).

## Test Configuration
- **Mode**: lockdown
- **Guard**: disabled
- **DIFC**: disabled

## Test Plan
1. List issues and pull requests from ${DIFC_SCOPE}
2. List issues and pull requests from octocat/Hello-World
3. Run one search tool (e.g., search_issues)

Expected: All operations should succeed because security filtering is not active in this mode.
EOF
    ;;
  public-only)
    cat > "$GENERATED_PROMPT_FILE" <<EOF
You are testing GitHub Guard in public-only mode through the MCP Gateway.

In public-only mode, only public repos and all objects within the public repos are accessible (min-integrity == none).

## Test Configuration
- **Mode**: public-only
- **DIFC Mode**: filter
- **AllowOnly Policy**: ${ALLOW_ONLY_PUBLIC_POLICY}

## Expected Behavior
**PUBLIC-ONLY MODE**: Only public repos and all objects within the public repos are accessible (min-integrity == none).

- ✅ **Public Repositories (e.g., octocat/Hello-World)**: Full access to ALL data when it exists
- ❌ **Private Repositories (e.g., ${DIFC_SCOPE})**: No access to private data
- ✅ **Global/User APIs**: Should work for public data only

## Test Plan

Execute EVERY read-only GitHub MCP call currently available in this environment.

### Part 1: Global/User Read-Only Calls (run once)
Run each of these once:
1. get_me
2. search_users (query: "octocat", perPage: 5)
3. search_repositories (query: "github-guard", perPage: 10)
4. search_issues (query: "bug", perPage: 5)
5. search_pull_requests (query: "is:open", perPage: 5)

### Part 2: Repo-Scoped Read-Only Calls (run for BOTH repos)
For each tool below, execute it twice:
- private target: owner=lpcox repo=github-guard
- public target: owner=octocat repo=Hello-World

Repo-scoped read-only tools to test:
1. list_issues (perPage: 5)
2. issue_read (discover issue_number from list_issues first; skip only if repo has no issues)
3. list_pull_requests (perPage: 5)
4. pull_request_read (discover pullNumber from list_pull_requests first; skip only if repo has no PRs)
5. list_commits (perPage: 5)
6. get_commit (discover sha from list_commits first; skip only if no commits)
7. get_file_contents (path: "README.md")
8. list_branches (perPage: 10)
9. list_tags (perPage: 10)
10. get_tag (use strategy below)
11. list_releases (perPage: 5)
12. get_latest_release (skip only if repo has no releases)
13. get_release_by_tag (discover tag from list_releases first; skip only if no releases)
14. get_label (name: "bug")
15. search_code (query: "repo:<owner>/<repo> README", perPage: 5)

### get_tag Practical Strategy
- Use list_tags to pick up to 3 candidate tags per repo.
- Call get_tag on each candidate until one succeeds.
- If all attempts return 404 Not Found, mark get_tag as expected skip (lightweight tags), not failure.
- Only mark get_tag failed for non-404 errors (auth, permission, transport, etc.).

### Part 3: Expected Behavior Validation
**Global/User APIs** (expected: PUBLIC DATA ONLY):
- get_me: Should work (user data is generally not private)
- search_users: Should work (user data is generally public)
- search_repositories: Should filter out private repos, show public ones only
- search_issues: Should filter out private issues, show public ones only
- search_pull_requests: Should filter out private PRs, show public ones only

**Private Repository (${DIFC_SCOPE})** (expected: ALL BLOCKED):
- All repo-scoped calls should return empty/blocked
- This validates private repository filtering

**Public Repository (octocat/Hello-World)** (expected: FULL ACCESS):
- All repo-scoped calls should return complete data when it exists
- No filtering should occur within public repositories

## Report Template (required)

Use this exact format for your final report:

${FENCE}
# GitHub Guard Public-Only Mode Test Results

## Test Configuration
- **Mode**: public-only
- **Policy**: ${ALLOW_ONLY_PUBLIC_POLICY}
- **Private Repository**: ${DIFC_SCOPE}
- **Public Repository**: octocat/Hello-World

## Global/User API Results (Expected: PUBLIC DATA ONLY)
| Tool | Result | Status | Notes |
|------|--------|--------|-------|
| get_me | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_users | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_repositories | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_issues | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_pull_requests | [result] | ✅ PASS / ❌ FAIL | [reason] |

## Repo-Scoped API Results
| Tool | Private Repo (${DIFC_SCOPE}) | Public Repo (octocat/Hello-World) | Status |
|------|-------------------------------|-------------------------------------|--------|
| list_issues | [result] | [result] | ✅ PASS / ❌ FAIL |
| issue_read | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_pull_requests | [result] | [result] | ✅ PASS / ❌ FAIL |
| pull_request_read | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_commits | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_commit | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_file_contents | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_branches | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_tags | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_tag | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_releases | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_latest_release | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_release_by_tag | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_label | [result] | [result] | ✅ PASS / ❌ FAIL |
| search_code | [result] | [result] | ✅ PASS / ❌ FAIL |

## Summary
- **Global APIs Working with Public Data**: [X/5] ✅ / ❌
- **Private Repository Blocked**: [X/15] ✅ / ❌  
- **Public Repository Access**: [X/15] ✅ / ❌
- **Overall Result**: ✅ PASS / ❌ FAIL

## Final Assessment
[Detailed explanation of whether public-only mode correctly blocks private data while allowing public data]
${FENCE}
EOF
    ;;
  owner-only)
    cat > "$GENERATED_PROMPT_FILE" <<EOF
You are testing GitHub Guard in owner-only mode through the MCP Gateway.

In owner-only mode, only repos owned by the owner and all objects within the owner's repos are accessible (min-integrity == none).

## Test Configuration
- **Mode**: owner-only
- **DIFC Mode**: filter
- **AllowOnly Policy**: ${ALLOW_ONLY_OWNER_POLICY}

## Expected Behavior
**OWNER-ONLY MODE**: Only repos owned by ${ALLOW_OWNER} and all objects within the owner's repos are accessible (min-integrity == none).

- ✅ **Owner's Repositories (${ALLOW_OWNER}/*)**: Full access to ALL data when it exists
- ❌ **Other Owner's Repositories (e.g., octocat/Hello-World)**: No access even if public
- ✅ **Global/User APIs**: Should work but filter out non-owner data

## Test Plan

Execute EVERY read-only GitHub MCP call currently available in this environment.

### Part 1: Global/User Read-Only Calls (run once)
Run each of these once:
1. get_me
2. search_users (query: "octocat", perPage: 5)
3. search_repositories (query: "github-guard", perPage: 10)
4. search_issues (query: "bug", perPage: 5)
5. search_pull_requests (query: "is:open", perPage: 5)

### Part 2: Repo-Scoped Read-Only Calls (run for BOTH repos)
For each tool below, execute it twice:
- owner-scoped private target: owner=lpcox repo=github-guard
- outside-owner public target: owner=octocat repo=Hello-World

Repo-scoped read-only tools to test:
1. list_issues (perPage: 5)
2. issue_read (discover issue_number from list_issues first; skip only if repo has no issues)
3. list_pull_requests (perPage: 5)
4. pull_request_read (discover pullNumber from list_pull_requests first; skip only if repo has no PRs)
5. list_commits (perPage: 5)
6. get_commit (discover sha from list_commits first; skip only if no commits)
7. get_file_contents (path: "README.md")
8. list_branches (perPage: 10)
9. list_tags (perPage: 10)
10. get_tag (use strategy below)
11. list_releases (perPage: 5)
12. get_latest_release (skip only if repo has no releases)
13. get_release_by_tag (discover tag from list_releases first; skip only if no releases)
14. get_label (name: "bug")
15. search_code (query: "repo:<owner>/<repo> README", perPage: 5)

### get_tag Practical Strategy
- Use list_tags to pick up to 3 candidate tags per repo.
- Call get_tag on each candidate until one succeeds.
- If all attempts return 404 Not Found, mark get_tag as expected skip (lightweight tags), not failure.
- Only mark get_tag failed for non-404 errors (auth, permission, transport, etc.).

### Part 3: Expected Behavior Validation
**Global/User APIs** (expected: OWNER DATA ONLY):
- get_me: Should work (user data is generally not owner-scoped)
- search_users: Should work (user data is generally not owner-scoped)
- search_repositories: Should filter out non-owner repos, show ${ALLOW_OWNER} repos only
- search_issues: Should filter out non-owner issues, show ${ALLOW_OWNER} issues only
- search_pull_requests: Should filter out non-owner PRs, show ${ALLOW_OWNER} PRs only

**Owner's Repository (${DIFC_SCOPE})** (expected: FULL ACCESS):
- All repo-scoped calls should return complete data when it exists
- No filtering should occur within the owner's repositories

**Non-Owner's Repository (octocat/Hello-World)** (expected: ALL BLOCKED):
- All repo-scoped calls should return empty/blocked (even though it's public)
- This validates owner-scoped filtering

## Report Template (required)

Use this exact format for your final report:

${FENCE}
# GitHub Guard Owner-Only Mode Test Results

## Test Configuration
- **Mode**: owner-only
- **Policy**: ${ALLOW_ONLY_OWNER_POLICY}
- **Allowed Owner**: ${ALLOW_OWNER}
- **Owner Repository**: ${DIFC_SCOPE}
- **Non-Owner Repository**: octocat/Hello-World

## Global/User API Results (Expected: OWNER DATA ONLY)
| Tool | Result | Status | Notes |
|------|--------|--------|-------|
| get_me | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_users | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_repositories | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_issues | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_pull_requests | [result] | ✅ PASS / ❌ FAIL | [reason] |

## Repo-Scoped API Results
| Tool | Owner Repo (${DIFC_SCOPE}) | Non-Owner Repo (octocat/Hello-World) | Status |
|------|-------------------------------------------|---------------------------------------|--------|
| list_issues | [result] | [result] | ✅ PASS / ❌ FAIL |
| issue_read | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_pull_requests | [result] | [result] | ✅ PASS / ❌ FAIL |
| pull_request_read | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_commits | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_commit | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_file_contents | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_branches | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_tags | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_tag | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_releases | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_latest_release | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_release_by_tag | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_label | [result] | [result] | ✅ PASS / ❌ FAIL |
| search_code | [result] | [result] | ✅ PASS / ❌ FAIL |

## Summary
- **Global APIs Working with Owner Data**: [X/5] ✅ / ❌
- **Owner Repository Access**: [X/15] ✅ / ❌  
- **Non-Owner Repository Blocked**: [X/15] ✅ / ❌
- **Overall Result**: ✅ PASS / ❌ FAIL

## Final Assessment
[Detailed explanation of whether owner-only mode correctly enforces owner-scoped access while blocking non-owner data]
${FENCE}
- private data from owners other than ${ALLOW_OWNER} must not be exposed
- search_repositories/search_code/search_issues/search_pull_requests must not leak out-of-scope private content

For integrity behavior:
- results below approved integrity should be blocked/filtered in owner-only mode

### Part 4: Final Report (required)
Provide:
1. A checklist of every read-only tool above with status for owner-scoped and outside-owner calls.
2. Any skips and exact reason (e.g., no releases/tags in that repo).
3. A final PASS/FAIL for "owner-only enforces owner-scoped private access while blocking out-of-scope private data".
EOF
    ;;
  repo-only)
  cat > "$GENERATED_PROMPT_FILE" <<EOF
You are testing GitHub Guard in repo-only mode through the MCP Gateway.

In repo-only mode, only the single repo and all objects within it are accessible (min-integrity == none).

## Test Configuration
- **Mode**: repo-only
- **DIFC Mode**: filter
- **AllowOnly Policy**: ${ALLOW_ONLY_REPO_POLICY}

## Expected Behavior
**REPO-ONLY MODE**: Only repos that match ${DIFC_SCOPE} and all objects within that repo are accessible (min-integrity == none).

- ✅ **Scoped Repository (${DIFC_SCOPE})**: Full access to ALL data when it exists
- ❌ **All Other Repositories**: No access (including public repositories like octocat/Hello-World)
- ❌ **Global/User APIs**: No access (get_me, search_* APIs)

## Test Plan

Execute EVERY read-only GitHub MCP call currently available in this environment.

### Part 1: Global/User Read-Only Calls (run once)
Run each of these once:
1. get_me
2. search_users (query: "octocat", perPage: 5)
3. search_repositories (query: "github-guard", perPage: 10)
4. search_issues (query: "bug", perPage: 5)
5. search_pull_requests (query: "is:open", perPage: 5)

### Part 2: Repo-Scoped Read-Only Calls (run for BOTH repos)
For each tool below, execute it twice:
- scoped private target: owner=lpcox repo=github-guard
- public target: owner=octocat repo=Hello-World

Repo-scoped read-only tools to test:
1. list_issues (perPage: 5)
2. issue_read (discover issue_number from list_issues first; skip only if repo has no issues)
3. list_pull_requests (perPage: 5)
4. pull_request_read (discover pullNumber from list_pull_requests first; skip only if repo has no PRs)
5. list_commits (perPage: 5)
6. get_commit (discover sha from list_commits first; skip only if no commits)
7. get_file_contents (path: "README.md")
8. list_branches (perPage: 10)
9. list_tags (perPage: 10)
10. get_tag (use strategy below)
11. list_releases (perPage: 5)
12. get_latest_release (skip only if repo has no releases)
13. get_release_by_tag (discover tag from list_releases first; skip only if no releases)
14. get_label (name: "bug")
15. search_code (query: "repo:<owner>/<repo> README", perPage: 5)

### get_tag Practical Strategy
- Use list_tags to pick up to 3 candidate tags per repo.
- Call get_tag on each candidate until one succeeds.
- If all attempts return 404 Not Found, mark get_tag as expected skip (lightweight tags), not failure.
- Only mark get_tag failed for non-404 errors (auth, permission, transport, etc.).

### Part 3: Expected Behavior Validation
**Global/User APIs** (expected: ALL BLOCKED):
- get_me: Should return empty/blocked
- search_users: Should return empty/blocked 
- search_repositories: Should return empty/blocked
- search_issues: Should return empty/blocked
- search_pull_requests: Should return empty/blocked

**Scoped Repository (${DIFC_SCOPE})** (expected: FULL ACCESS):
- All repo-scoped calls should return complete data when it exists
- No filtering should occur within the scoped repository

**Out-of-Scope Repository (octocat/Hello-World)** (expected: ALL BLOCKED):
- All repo-scoped calls should return empty/blocked (even though it's public)
- This validates repo-only scope enforcement

## Report Template (required)

Use this exact format for your final report:

${FENCE}
# GitHub Guard Repo-Only Mode Test Results

## Test Configuration
- **Mode**: repo-only
- **Policy**: ${ALLOW_ONLY_REPO_POLICY}
- **Scoped Repository**: ${DIFC_SCOPE}
- **Test Repository**: octocat/Hello-World

## Global/User API Results (Expected: ALL BLOCKED)
| Tool | Result | Status | Notes |
|------|--------|--------|-------|
| get_me | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_users | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_repositories | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_issues | [result] | ✅ PASS / ❌ FAIL | [reason] |
| search_pull_requests | [result] | ✅ PASS / ❌ FAIL | [reason] |

## Repo-Scoped API Results
| Tool | Scoped Repo (${DIFC_SCOPE}) | Out-of-Scope Repo (octocat/Hello-World) | Status |
|------|------------------------------|------------------------------------------|--------|
| list_issues | [result] | [result] | ✅ PASS / ❌ FAIL |
| issue_read | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_pull_requests | [result] | [result] | ✅ PASS / ❌ FAIL |
| pull_request_read | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_commits | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_commit | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_file_contents | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_branches | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_tags | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_tag | [result] | [result] | ✅ PASS / ❌ FAIL |
| list_releases | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_latest_release | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_release_by_tag | [result] | [result] | ✅ PASS / ❌ FAIL |
| get_label | [result] | [result] | ✅ PASS / ❌ FAIL |
| search_code | [result] | [result] | ✅ PASS / ❌ FAIL |

## Summary
- **Global APIs Blocked**: [X/5] ✅ / ❌
- **Scoped Repository Access**: [X/15] ✅ / ❌  
- **Out-of-Scope Repository Blocked**: [X/15] ✅ / ❌
- **Overall Result**: ✅ PASS / ❌ FAIL

## Final Assessment
[Detailed explanation of whether repo-only mode correctly enforces the expected behavior]
${FENCE}
EOF
  ;;
    prefix-only)
    cat > "$GENERATED_PROMPT_FILE" <<EOF
  You are testing GitHub Guard in prefix-only mode through the MCP Gateway.

  In prefix-only mode, only repos that match the prefix and all objects within those repos are accessible (min-integrity == none).

  ## Test Configuration
  - **Mode**: prefix-only
  - **DIFC Mode**: filter
  - **AllowOnly Policy**: ${ALLOW_ONLY_PREFIX_POLICY}

  ## Expected Behavior
  **PREFIX-ONLY MODE**: Only repos that match the prefix "lpcox/git-*" and all objects within those repos are accessible (min-integrity == none).

  - ✅ **Matching Prefix Repositories (lpcox/git-*)**: Full access to ALL data when it exists
  - ❌ **Non-Matching Repositories (e.g., lpcox/github-guard, octocat/Hello-World)**: No access
  - ✅ **Global/User APIs**: Should work but filter out non-prefix data

  ## Test Plan

  Execute EVERY read-only GitHub MCP call currently available in this environment.

  ### Part 1: Global/User Read-Only Calls (run once)
  Run each of these once:
  1. get_me
  2. search_users (query: "octocat", perPage: 5)
  3. search_repositories (query: "git", perPage: 10)
  4. search_issues (query: "bug", perPage: 5)
  5. search_pull_requests (query: "is:open", perPage: 5)

  ### Part 2: Repo-Scoped Read-Only Calls
  Run the repo-scoped tools for:
  - one lpcox repo where repo name starts with "git-" (if available)
  - out-of-prefix private target: owner=lpcox repo=github-guard
  - out-of-owner public target: owner=octocat repo=Hello-World

  Repo-scoped read-only tools to test:
  1. list_issues (perPage: 5)
  2. issue_read (discover issue_number from list_issues first; skip only if repo has no issues)
  3. list_pull_requests (perPage: 5)
  4. pull_request_read (discover pullNumber from list_pull_requests first; skip only if repo has no PRs)
  5. list_commits (perPage: 5)
  6. get_commit (discover sha from list_commits first; skip only if no commits)
  7. get_file_contents (path: "README.md")
  8. list_branches (perPage: 10)
  9. list_tags (perPage: 10)
  10. get_tag (use strategy below)
  11. list_releases (perPage: 5)
  12. get_latest_release (skip only if repo has no releases)
  13. get_release_by_tag (discover tag from list_releases first; skip only if no releases)
  14. get_label (name: "bug")
  15. search_code (query: "repo:<owner>/<repo> README", perPage: 5)

  ### get_tag Practical Strategy
  - Use list_tags to pick up to 3 candidate tags per repo.
  - Call get_tag on each candidate until one succeeds.
  - If all attempts return 404 Not Found, mark get_tag as expected skip (lightweight tags), not failure.
  - Only mark get_tag failed for non-404 errors (auth, permission, transport, etc.).

  ### Part 3: Expected Behavior Validation
  **Global/User APIs** (expected: PREFIX DATA ONLY):
  - get_me: Should work (user data is generally not repo-scoped)
  - search_users: Should work (user data is generally not repo-scoped)
  - search_repositories: Should filter out non-prefix repos, show lpcox/git-* repos only
  - search_issues: Should filter out non-prefix issues, show lpcox/git-* issues only
  - search_pull_requests: Should filter out non-prefix PRs, show lpcox/git-* PRs only

  **Matching Prefix Repository (lpcox/git-*)** (expected: FULL ACCESS):
  - All repo-scoped calls should return complete data when it exists
  - No filtering should occur within matching repositories

  **Non-Matching Repositories (lpcox/github-guard, octocat/Hello-World)** (expected: ALL BLOCKED):
  - All repo-scoped calls should return empty/blocked
  - This validates prefix-based filtering

  ## Report Template (required)

  Use this exact format for your final report:

  ${FENCE}
  # GitHub Guard Prefix-Only Mode Test Results

  ## Test Configuration
  - **Mode**: prefix-only
  - **Policy**: ${ALLOW_ONLY_PREFIX_POLICY}
  - **Allowed Prefix**: lpcox/git-*
  - **Matching Repository**: [name if found, or "NONE FOUND"]
  - **Non-Matching Repositories**: lpcox/github-guard, octocat/Hello-World

  ## Global/User API Results (Expected: PREFIX DATA ONLY)
  | Tool | Result | Status | Notes |
  |------|--------|--------|-------|
  | get_me | [result] | ✅ PASS / ❌ FAIL | [reason] |
  | search_users | [result] | ✅ PASS / ❌ FAIL | [reason] |
  | search_repositories | [result] | ✅ PASS / ❌ FAIL | [reason] |
  | search_issues | [result] | ✅ PASS / ❌ FAIL | [reason] |
  | search_pull_requests | [result] | ✅ PASS / ❌ FAIL | [reason] |

  ## Repo-Scoped API Results
  | Tool | Matching Repo (lpcox/git-*) | Non-Matching Repo 1 (lpcox/github-guard) | Non-Matching Repo 2 (octocat/Hello-World) | Status |
  |------|------------------------------|-------------------------------------------|-------------------------------------------|--------|
  | list_issues | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | issue_read | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | list_pull_requests | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | pull_request_read | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | list_commits | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_commit | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_file_contents | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | list_branches | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | list_tags | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_tag | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | list_releases | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_latest_release | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_release_by_tag | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_label | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |
  | search_code | [result] | [result] | [result] | ✅ PASS / ❌ FAIL |

  ## Summary
  - **Global APIs Working with Prefix Data**: [X/5] ✅ / ❌
  - **Matching Prefix Repository Access**: [X/15] ✅ / ❌  
  - **Non-Matching Repositories Blocked**: [X/30] ✅ / ❌
  - **Overall Result**: ✅ PASS / ❌ FAIL

  ## Final Assessment
  [Detailed explanation of whether prefix-only mode correctly enforces prefix-based access while blocking non-prefix data]
  ${FENCE}
EOF
    ;;
  multi-only)
  cat > "$GENERATED_PROMPT_FILE" <<EOF
  You are testing GitHub Guard in multi-only mode through the MCP Gateway.

  In multi-only mode, only repos that match the prefixes and other matching criteria and only merged objects within those repos (min-integrity == merged).

  ## Test Configuration
  - **Mode**: multi-only
  - **DIFC Mode**: filter
  - **AllowOnly Policy**: ${ALLOW_ONLY_MULTI_POLICY}

  ## Expected Behavior
  **MULTI-ONLY MODE**: Only repos that match the prefixes and other matching criteria and only merged objects within those repos are accessible (min-integrity == merged).

  - ✅ **Matching Repositories**: Access to merged-level integrity data only within matching repositories
  - ❌ **Non-Matching Repositories**: No access
  - ✅ **Global/User APIs**: Should work but filter out non-matching data
  - ⚠️ **Integrity Filtering**: Even within matching repos, only merged-level data is accessible

  ## Test Plan

  Execute EVERY read-only GitHub MCP call currently available in this environment.

  ### Part 1: Global/User Read-Only Calls (run once)
  Run each of these once:
  1. get_me
  2. search_users (query: "octocat", perPage: 5)
  3. search_repositories (query: "git", perPage: 10)
  4. search_issues (query: "bug", perPage: 5)
  5. search_pull_requests (query: "is:open", perPage: 5)

  ### Part 2: Repo-Scoped Read-Only Calls
  Run repo-scoped tools against:
  - explicit in-scope target: owner=lpcox repo=github-guard
  - out-of-scope owner target: owner=octocat repo=Hello-World

  Repo-scoped read-only tools to test:
  1. list_issues (perPage: 5)
  2. issue_read (discover issue_number from list_issues first; skip only if repo has no issues)
  3. list_pull_requests (perPage: 5)
  4. pull_request_read (discover pullNumber from list_pull_requests first; skip only if repo has no PRs)
  5. list_commits (perPage: 5)
  6. get_commit (discover sha from list_commits first; skip only if no commits)
  7. get_file_contents (path: "README.md")
  8. list_branches (perPage: 10)
  9. list_tags (perPage: 10)
  10. get_tag (use strategy below)
  11. list_releases (perPage: 5)
  12. get_latest_release (skip only if repo has no releases)
  13. get_release_by_tag (discover tag from list_releases first; skip only if no releases)
  14. get_label (name: "bug")
  15. search_code (query: "repo:<owner>/<repo> README", perPage: 5)

  ### get_tag Practical Strategy
  - Use list_tags to pick up to 3 candidate tags per repo.
  - Call get_tag on each candidate until one succeeds.
  - If all attempts return 404 Not Found, mark get_tag as expected skip (lightweight tags), not failure.
  - Only mark get_tag failed for non-404 errors (auth, permission, transport, etc.).

  ### Part 3: Expected Behavior Validation
  **Global/User APIs** (expected: MERGED DATA ONLY):
  - get_me: Should work (user data is generally not integrity-scoped)
  - search_users: Should work (user data is generally not integrity-scoped)
  - search_repositories: Should filter to matching repos with merged-level integrity only
  - search_issues: Should filter to matching repos with merged-level integrity only
  - search_pull_requests: Should filter to matching repos with merged-level integrity only

  **Matching Repository (${DIFC_SCOPE})** (expected: MERGED DATA ONLY):
  - All repo-scoped calls should return merged-level integrity data only when it exists
  - Lower integrity data (unapproved, approved) should be filtered out
  - Only commits on main branch, merged PRs, etc. should be accessible

  **Non-Matching Repository (octocat/Hello-World)** (expected: ALL BLOCKED):
  - All repo-scoped calls should return empty/blocked
  - This validates repo matching criteria

  ## Report Template (required)

  Use this exact format for your final report:

  ${FENCE}
  # GitHub Guard Multi-Only Mode Test Results

  ## Test Configuration
  - **Mode**: multi-only
  - **Policy**: ${ALLOW_ONLY_MULTI_POLICY}
  - **Matching Repository**: ${DIFC_SCOPE}
  - **Non-Matching Repository**: octocat/Hello-World
  - **Integrity Requirement**: merged

  ## Global/User API Results (Expected: MERGED DATA ONLY)
  | Tool | Result | Status | Notes |
  |------|--------|--------|-------|
  | get_me | [result] | ✅ PASS / ❌ FAIL | [reason] |
  | search_users | [result] | ✅ PASS / ❌ FAIL | [reason] |
  | search_repositories | [result] | ✅ PASS / ❌ FAIL | [reason] |
  | search_issues | [result] | ✅ PASS / ❌ FAIL | [reason] |
  | search_pull_requests | [result] | ✅ PASS / ❌ FAIL | [reason] |

  ## Repo-Scoped API Results
  | Tool | Matching Repo (${DIFC_SCOPE}) | Non-Matching Repo (octocat/Hello-World) | Status |
  |------|-------------------------------------|------------------------------------------|--------|
  | list_issues | [result] | [result] | ✅ PASS / ❌ FAIL |
  | issue_read | [result] | [result] | ✅ PASS / ❌ FAIL |
  | list_pull_requests | [result] | [result] | ✅ PASS / ❌ FAIL |
  | pull_request_read | [result] | [result] | ✅ PASS / ❌ FAIL |
  | list_commits | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_commit | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_file_contents | [result] | [result] | ✅ PASS / ❌ FAIL |
  | list_branches | [result] | [result] | ✅ PASS / ❌ FAIL |
  | list_tags | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_tag | [result] | [result] | ✅ PASS / ❌ FAIL |
  | list_releases | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_latest_release | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_release_by_tag | [result] | [result] | ✅ PASS / ❌ FAIL |
  | get_label | [result] | [result] | ✅ PASS / ❌ FAIL |
  | search_code | [result] | [result] | ✅ PASS / ❌ FAIL |

  ## Summary
  - **Global APIs Working with Merged Data**: [X/5] ✅ / ❌
  - **Matching Repository Merged Data Access**: [X/15] ✅ / ❌  
  - **Non-Matching Repository Blocked**: [X/15] ✅ / ❌
  - **Overall Result**: ✅ PASS / ❌ FAIL

  ## Final Assessment
  [Detailed explanation of whether multi-only mode correctly enforces matching criteria with merged integrity requirements while blocking non-matching data]
  ${FENCE}
EOF
  ;;
esac

PROMPT_FILE="${COPILOT_PROMPT_FILE:-$GENERATED_PROMPT_FILE}"
echo -e "${GREEN}✓${NC} Using prompt file: $PROMPT_FILE"

# Create conversation file if not exists
CONVERSATION_FILE="$PROJECT_ROOT/conversation.md"
if [ ! -f "$CONVERSATION_FILE" ]; then
    cat > "$CONVERSATION_FILE" <<EOF
# GitHub Guard DIFC Test

Testing DIFC enforcement with strict mode.

## Agent Configuration
- **Managed Repo**: ${DIFC_SCOPE}
- **Integrity Tags**: approved:${DIFC_SCOPE}, unapproved:${DIFC_SCOPE}
- **Secrecy Tags**: private:${DIFC_SCOPE}

## Test Cases

### ✅ Expected to SUCCEED (managed repo)
- list_issues owner:${DIFC_SCOPE%%/*} repo:${DIFC_SCOPE##*/}
- list_pull_requests owner:${DIFC_SCOPE%%/*} repo:${DIFC_SCOPE##*/}
- get_issue_comments (if issues exist)
- list_discussions (if enabled)

### ❌ Expected to be BLOCKED (unmanaged repo)
- list_issues owner:octocat repo:Hello-World
- list_pull_requests owner:octocat repo:Hello-World
- search_repositories (no repo context = empty integrity)

## Run the Tests
Please execute the test plan from copilot-prompt.txt
EOF
    echo -e "${GREEN}✓${NC} Created conversation file: $CONVERSATION_FILE"
fi

echo ""
echo "=========================================="
echo "Starting Copilot with MCP Gateway"
echo "=========================================="
echo ""
echo "MCP Config: $MCP_CONFIG_FILE"
echo "Prompt: $PROMPT_FILE"
if [ -n "$ACTIVE_ALLOW_ONLY_POLICY" ]; then
  echo "AllowOnly policy: $ACTIVE_ALLOW_ONLY_POLICY"
fi
echo "Logs: $COPILOT_WORK_DIR/logs/"
echo ""
echo "Press Ctrl+C to exit when done."
echo ""

# Cleanup function
cleanup() {
    echo ""
    echo "=========================================="
    echo "Cleaning up..."
    kill $GATEWAY_PID 2>/dev/null || true
    docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
    
    # Save gateway log to project directory
    if [ -f "$COPILOT_WORK_DIR/gateway.log" ]; then
        cp "$COPILOT_WORK_DIR/gateway.log" "$PROJECT_ROOT/gateway.log"
        echo "Gateway logs saved to: $PROJECT_ROOT/gateway.log"
    fi
    
    echo -e "${GREEN}✓${NC} Cleanup complete"
}
trap cleanup EXIT

# Run Copilot
cd "$PROJECT_ROOT"

# Default to Claude model if not specified
COPILOT_MODEL="${MODEL_AGENT_COPILOT:-claude-sonnet-4}"

# Build env var assignments for BYOK / offline mode.
# These are only forwarded when a provider URL is configured so that normal
# (GitHub-authenticated) runs are unaffected.
COPILOT_ENV_VARS=()
if [ -n "$COPILOT_PROVIDER_BASE_URL" ]; then
    COPILOT_ENV_VARS+=(
        COPILOT_OFFLINE=true
        COPILOT_PROVIDER_BASE_URL="$COPILOT_PROVIDER_BASE_URL"
    )
    [ -n "$COPILOT_PROVIDER_TYPE" ]    && COPILOT_ENV_VARS+=(COPILOT_PROVIDER_TYPE="$COPILOT_PROVIDER_TYPE")
    [ -n "$COPILOT_PROVIDER_API_KEY" ] && COPILOT_ENV_VARS+=(COPILOT_PROVIDER_API_KEY="$COPILOT_PROVIDER_API_KEY")
fi

env ${COPILOT_ENV_VARS[@]+"${COPILOT_ENV_VARS[@]}"} copilot \
    --add-dir "$COPILOT_WORK_DIR" \
    --log-level all \
    --log-dir "$COPILOT_WORK_DIR/logs/" \
    --add-dir "${PWD}" \
    --disable-builtin-mcps \
    --additional-mcp-config "@$MCP_CONFIG_FILE" \
    --allow-all-tools \
    --allow-all-paths \
    --log-level debug \
    --share "$CONVERSATION_FILE" \
    --prompt "$(cat "$PROMPT_FILE")" \
    --model "$COPILOT_MODEL"
