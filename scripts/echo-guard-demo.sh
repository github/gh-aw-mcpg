#!/bin/bash
# Echo Guard Demo Script
# Demonstrates the echo guard's output when processing MCP tool calls

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Get script directory and project root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ECHO_GUARD_DIR="$PROJECT_ROOT/examples/guards/echo-guard"
WASM_FILE="$ECHO_GUARD_DIR/guard.wasm"
DEMO_CONFIG="$ECHO_GUARD_DIR/demo-config.toml"
CODEX_CONFIG="$ECHO_GUARD_DIR/codex.config.toml"
GATEWAY_BINARY="$PROJECT_ROOT/awmg"
GATEWAY_PID=""

print_header() {
    echo ""
    echo -e "${BOLD}${BLUE}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "${BOLD}${BLUE}  $1${NC}"
    echo -e "${BOLD}${BLUE}═══════════════════════════════════════════════════════════════${NC}"
    echo ""
}

print_step() {
    echo -e "${CYAN}▶ $1${NC}"
}

print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

cleanup() {
    if [ -n "$GATEWAY_PID" ] && kill -0 "$GATEWAY_PID" 2>/dev/null; then
        print_step "Stopping gateway (PID: $GATEWAY_PID)..."
        kill "$GATEWAY_PID" 2>/dev/null || true
        wait "$GATEWAY_PID" 2>/dev/null || true
        print_success "Gateway stopped"
    fi
}

trap cleanup EXIT

# Check for TinyGo
check_tinygo() {
    if ! command -v tinygo &> /dev/null; then
        print_error "TinyGo is not installed"
        echo ""
        echo "Install TinyGo from: https://tinygo.org/getting-started/install/"
        echo ""
        echo "On macOS:"
        echo "  brew install tinygo"
        echo ""
        echo "On Ubuntu/Debian:"
        echo "  wget https://github.com/tinygo-org/tinygo/releases/download/v0.34.0/tinygo_0.34.0_amd64.deb"
        echo "  sudo dpkg -i tinygo_0.34.0_amd64.deb"
        exit 1
    fi
    print_success "TinyGo found: $(tinygo version)"
}

# Build the gateway binary
build_gateway() {
    if [ ! -f "$GATEWAY_BINARY" ]; then
        print_step "Building gateway binary..."
        cd "$PROJECT_ROOT"
        make build
        print_success "Gateway binary built"
    else
        print_success "Gateway binary already exists"
    fi
}

# Build the echo guard
build_guard() {
    print_header "Building Echo Guard"
    
    print_step "Checking TinyGo installation..."
    check_tinygo
    echo ""
    
    print_step "Building guard.wasm..."
    cd "$ECHO_GUARD_DIR"
    
    # Try to find Go 1.23 for TinyGo compatibility
    GO123=""
    for cmd in go1.23 go1.23.4 go1.23.9; do
        if command -v "$cmd" &> /dev/null; then
            GO123="$cmd"
            break
        fi
    done
    
    if [ -n "$GO123" ]; then
        print_step "Using Go 1.23 for TinyGo compatibility: $GO123"
        GOROOT=$("$GO123" env GOROOT) tinygo build -o guard.wasm -target=wasi main.go
    else
        print_warning "Go 1.23 not found, using default Go (may have compatibility issues)"
        tinygo build -o guard.wasm -target=wasi main.go
    fi
    
    if [ -f "$WASM_FILE" ]; then
        SIZE=$(ls -lh "$WASM_FILE" | awk '{print $5}')
        print_success "Built guard.wasm ($SIZE)"
    else
        print_error "Failed to build guard.wasm"
        exit 1
    fi
    
    cd "$PROJECT_ROOT"
}

# Run the demo using the Go test (quick mode)
run_test_demo() {
    print_header "Running Echo Guard Test Demo"
    
    echo -e "${BOLD}The echo guard prints all inputs it receives from the gateway.${NC}"
    echo -e "${BOLD}This is useful for debugging guard implementations.${NC}"
    echo ""
    
    print_step "Running label_resource test (tool call interception)..."
    echo ""
    echo -e "${YELLOW}--- Expected output: Guard receives tool name, args, and capabilities ---${NC}"
    echo ""
    
    # Run specific tests that show output
    cd "$PROJECT_ROOT"
    go test -v -run "TestEchoGuardLabelResourceOutput" ./test/integration/... 2>&1 | \
        sed -n '/Echo guard output:/,/=============================$/p' | \
        sed 's/^    //'
    
    echo ""
    print_step "Running label_response test (response interception)..."
    echo ""
    echo -e "${YELLOW}--- Expected output: Guard receives tool result data ---${NC}"
    echo ""
    
    go test -v -run "TestEchoGuardLabelResponseOutput" ./test/integration/... 2>&1 | \
        sed -n '/Echo guard output:/,/=============================$/p' | \
        sed 's/^    //'
    
    echo ""
    print_success "Test demo complete!"
}

# Run the gateway with echo guard (end-to-end mode)
run_gateway() {
    print_header "Starting Gateway with Echo Guard"
    
    # Build gateway if needed
    build_gateway
    
    # Check GitHub token
    if [ -z "$GITHUB_PERSONAL_ACCESS_TOKEN" ]; then
        print_warning "GITHUB_PERSONAL_ACCESS_TOKEN is not set"
        echo "  The GitHub server will not work without it."
        echo "  You can still test with the 'fetch' server."
        echo ""
    else
        print_success "GITHUB_PERSONAL_ACCESS_TOKEN is set"
    fi
    
    print_step "Starting gateway on http://127.0.0.1:8000..."
    echo ""
    
    cd "$PROJECT_ROOT"
    "$GATEWAY_BINARY" --config "$DEMO_CONFIG"
}

# Run gateway in background and show instructions for Codex
run_gateway_with_codex() {
    print_header "Echo Guard End-to-End Demo with Codex"
    
    # Build gateway if needed
    build_gateway
    
    # Check GitHub token
    if [ -z "$GITHUB_PERSONAL_ACCESS_TOKEN" ]; then
        print_warning "GITHUB_PERSONAL_ACCESS_TOKEN is not set"
        echo "  Export it before running: export GITHUB_PERSONAL_ACCESS_TOKEN=ghp_..."
        echo ""
    else
        print_success "GITHUB_PERSONAL_ACCESS_TOKEN is set"
    fi
    
    print_step "Starting gateway in foreground on http://127.0.0.1:8000..."
    echo ""
    echo -e "${BOLD}${YELLOW}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "${BOLD}${YELLOW}  INSTRUCTIONS${NC}"
    echo -e "${BOLD}${YELLOW}═══════════════════════════════════════════════════════════════${NC}"
    echo ""
    echo "1. The gateway will start below with the echo guard attached to GitHub."
    echo ""
    echo "2. In another terminal, start Codex with the demo config:"
    echo ""
    echo -e "   ${CYAN}export AGENT_ID=demo-key-12345${NC}"
    echo -e "   ${CYAN}codex --mcp-config $CODEX_CONFIG${NC}"
    echo ""
    echo "3. Ask Codex to use GitHub tools, for example:"
    echo "   - 'List the issues in octocat/Hello-World'"
    echo "   - 'What are the recent commits in github/docs?'"
    echo ""
    echo "4. Watch the gateway output below - you'll see the echo guard printing"
    echo "   the tool calls and responses as they flow through."
    echo ""
    echo "5. Press Ctrl-C to stop the gateway when done."
    echo ""
    echo -e "${BOLD}${YELLOW}═══════════════════════════════════════════════════════════════${NC}"
    echo ""
    
    cd "$PROJECT_ROOT"
    "$GATEWAY_BINARY" --config "$DEMO_CONFIG"
}

# Interactive mode with tmux - shows gateway + instructions side by side
run_tmux_demo() {
    print_header "Interactive Echo Guard Demo (tmux)"
    
    if ! command -v tmux &> /dev/null; then
        print_error "tmux is not installed"
        echo "Install with: brew install tmux (macOS) or apt install tmux (Linux)"
        exit 1
    fi
    
    # Build gateway if needed
    build_gateway
    
    SESSION="echo-guard-demo"
    
    # Kill existing session if it exists
    tmux kill-session -t "$SESSION" 2>/dev/null || true
    
    # Create new session with gateway running
    tmux new-session -d -s "$SESSION" -n "gateway"
    
    # Main pane: Gateway output
    tmux send-keys -t "$SESSION:0.0" "cd $PROJECT_ROOT && echo '=== GATEWAY OUTPUT ===' && echo 'Starting gateway with echo guard...' && echo '' && ./awmg --config $DEMO_CONFIG 2>&1" Enter
    
    # Split horizontally for instructions
    tmux split-window -h -t "$SESSION"
    tmux send-keys -t "$SESSION:0.1" "clear && echo '
${BOLD}${CYAN}═══════════════════════════════════════════════════════════════${NC}
${BOLD}${CYAN}  ECHO GUARD DEMO - INSTRUCTIONS${NC}
${BOLD}${CYAN}═══════════════════════════════════════════════════════════════${NC}

The gateway is running on the left pane with the echo guard.

${BOLD}TO CONNECT CODEX:${NC}

  1. Open a new terminal

  2. Set the API key:
     ${CYAN}export AGENT_ID=demo-key-12345${NC}

  3. Start Codex:
     ${CYAN}codex --mcp-config $CODEX_CONFIG${NC}

  4. Ask Codex to use GitHub:
     \"List issues in octocat/Hello-World\"
     \"Show recent PRs in github/docs\"

${BOLD}WHAT TO WATCH:${NC}

  Look at the left pane - the echo guard will print:
  - Tool Name (e.g., list_issues, get_repository)
  - Tool Args (the parameters passed)
  - Tool Result (the response from GitHub)

${BOLD}KEYBOARD:${NC}
  Ctrl-B + Arrow  - Switch panes
  Ctrl-B + D      - Detach from tmux
  Ctrl-C          - Stop gateway (left pane)

' && bash" Enter
    
    # Make the gateway pane wider
    tmux resize-pane -t "$SESSION:0.0" -x 80
    
    # Attach to session
    echo ""
    print_step "Starting tmux session..."
    echo "Press Ctrl-B then D to detach"
    echo ""
    
    tmux attach-session -t "$SESSION"
}

# Show usage
usage() {
    echo "Echo Guard Demo"
    echo ""
    echo "Usage: $0 [command]"
    echo ""
    echo "Commands:"
    echo "  build       Build the echo guard WASM file"
    echo "  test        Run quick test demo (unit tests with output)"
    echo "  gateway     Start gateway with echo guard (foreground)"
    echo "  codex       Start gateway with Codex connection instructions"
    echo "  tmux        Interactive demo with tmux (gateway + instructions)"
    echo "  all         Build and run test demo (default)"
    echo ""
    echo "End-to-End Demo:"
    echo "  1. Build the guard:     $0 build"
    echo "  2. Start gateway:       $0 codex"
    echo "  3. In another terminal: export AGENT_ID=demo-key-12345"
    echo "  4. Run Codex:           codex --mcp-config examples/guards/echo-guard/codex.config.toml"
    echo ""
    echo "Examples:"
    echo "  $0              # Run quick test demo"
    echo "  $0 codex        # Start gateway for Codex integration"
    echo "  $0 tmux         # Interactive tmux demo"
}

# Main
main() {
    case "${1:-all}" in
        build)
            build_guard
            ;;
        test|run)
            build_guard
            run_test_demo
            ;;
        gateway)
            build_guard
            run_gateway
            ;;
        codex)
            build_guard
            run_gateway_with_codex
            ;;
        tmux|interactive)
            build_guard
            run_tmux_demo
            ;;
        all)
            build_guard
            run_test_demo
            ;;
        help|--help|-h)
            usage
            ;;
        *)
            print_error "Unknown command: $1"
            usage
            exit 1
            ;;
    esac
}

main "$@"
