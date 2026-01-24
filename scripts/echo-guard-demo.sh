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

# Run the demo using the Go test
run_demo() {
    print_header "Running Echo Guard Demo"
    
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
    print_success "Demo complete!"
}

# Interactive mode with tmux
run_interactive() {
    print_header "Interactive Echo Guard Demo (tmux)"
    
    if ! command -v tmux &> /dev/null; then
        print_error "tmux is not installed"
        echo "Install with: brew install tmux (macOS) or apt install tmux (Linux)"
        exit 1
    fi
    
    SESSION="echo-guard-demo"
    
    # Kill existing session if it exists
    tmux kill-session -t "$SESSION" 2>/dev/null || true
    
    # Create new session
    tmux new-session -d -s "$SESSION" -n "demo"
    
    # Split into panes
    tmux split-window -h -t "$SESSION"
    tmux split-window -v -t "$SESSION:0.0"
    
    # Pane 0 (top-left): Guard source code
    tmux send-keys -t "$SESSION:0.0" "echo -e '${CYAN}=== Echo Guard Source ===${NC}' && cat $ECHO_GUARD_DIR/main.go | head -60" Enter
    
    # Pane 1 (bottom-left): Build and watch
    tmux send-keys -t "$SESSION:0.1" "cd $PROJECT_ROOT && echo -e '${CYAN}=== Build & Run Tests ===${NC}' && sleep 2 && make echo-guard-demo-run 2>&1 | head -100" Enter
    
    # Pane 2 (right): Test output
    tmux send-keys -t "$SESSION:0.2" "echo -e '${CYAN}=== Test Output ===${NC}' && sleep 4 && cd $PROJECT_ROOT && go test -v -run 'TestEchoGuard' ./test/integration/... 2>&1 | tail -80" Enter
    
    # Attach to session
    echo ""
    print_step "Starting tmux session..."
    echo "Press Ctrl-B then D to detach, or Ctrl-C to exit"
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
    echo "  run         Run the demo (build + test with output)"
    echo "  interactive Run interactive demo with tmux"
    echo "  all         Build and run full demo (default)"
    echo ""
    echo "Examples:"
    echo "  $0              # Run full demo"
    echo "  $0 build        # Just build the guard"
    echo "  $0 interactive  # Run with tmux panes"
}

# Main
main() {
    case "${1:-all}" in
        build)
            build_guard
            ;;
        run)
            run_demo
            ;;
        interactive)
            build_guard
            run_interactive
            ;;
        all)
            build_guard
            run_demo
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
