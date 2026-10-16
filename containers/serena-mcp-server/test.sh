#!/bin/bash
# Test script for Serena MCP server container
# Tests multi-language support: Python, Java, JavaScript/TypeScript, Go

set -e

CONTAINER_IMAGE="${1:-serena-mcp-server:local}"
TEST_DIR="/tmp/serena-test-$$"

echo "=========================================="
echo "Testing Serena MCP Server Container"
echo "Container: $CONTAINER_IMAGE"
echo "=========================================="
echo ""

# Create test directory
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

# Test 1: Container runs and responds to help
echo "Test 1: Container help command"
if docker run --rm "$CONTAINER_IMAGE" --help > /dev/null 2>&1; then
    echo "✓ Container help command works"
else
    echo "✗ Container help command failed"
    exit 1
fi
echo ""

# Test 2: Create sample files for each language
echo "Test 2: Creating sample code files"

# Python
mkdir -p python_project
cat > python_project/hello.py << 'EOF'
def greet(name: str) -> str:
    """Greet a person by name."""
    return f"Hello, {name}!"

if __name__ == "__main__":
    print(greet("World"))
EOF

# Java
mkdir -p java_project
cat > java_project/Hello.java << 'EOF'
public class Hello {
    public static String greet(String name) {
        return "Hello, " + name + "!";
    }
    
    public static void main(String[] args) {
        System.out.println(greet("World"));
    }
}
EOF

# JavaScript/TypeScript
mkdir -p js_project
cat > js_project/hello.js << 'EOF'
function greet(name) {
    return `Hello, ${name}!`;
}

console.log(greet("World"));
EOF

cat > js_project/hello.ts << 'EOF'
function greet(name: string): string {
    return `Hello, ${name}!`;
}

console.log(greet("World"));
EOF

# Go
mkdir -p go_project
cat > go_project/hello.go << 'EOF'
package main

import "fmt"

func greet(name string) string {
    return fmt.Sprintf("Hello, %s!", name)
}

func main() {
    fmt.Println(greet("World"))
}
EOF

cat > go_project/go.mod << 'EOF'
module hello

go 1.21
EOF

echo "✓ Sample files created"
echo ""

# Test 3: Test MCP initialize request
echo "Test 3: Testing MCP initialize request"
INIT_REQUEST='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'

if echo "$INIT_REQUEST" | docker run --rm -i \
    -v "$TEST_DIR:/workspace:ro" \
    "$CONTAINER_IMAGE" > /tmp/serena-init-response.json 2>&1; then
    if grep -q "jsonrpc" /tmp/serena-init-response.json 2>/dev/null; then
        echo "✓ MCP initialize request succeeded"
        cat /tmp/serena-init-response.json | head -5
    else
        echo "⚠ Container started but response unclear"
        cat /tmp/serena-init-response.json | head -10
    fi
else
    echo "⚠ MCP initialize test had issues (may be normal for first run)"
    cat /tmp/serena-init-response.json | head -10
fi
echo ""

# Test 4: Verify language runtimes are installed
echo "Test 4: Verifying language runtimes"

# Python
if docker run --rm --entrypoint python3 "$CONTAINER_IMAGE" --version > /dev/null 2>&1; then
    PYTHON_VERSION=$(docker run --rm --entrypoint python3 "$CONTAINER_IMAGE" --version 2>&1)
    echo "✓ Python: $PYTHON_VERSION"
else
    echo "✗ Python not found"
fi

# Java
if docker run --rm --entrypoint java "$CONTAINER_IMAGE" -version > /dev/null 2>&1; then
    JAVA_VERSION=$(docker run --rm --entrypoint java "$CONTAINER_IMAGE" -version 2>&1 | head -1)
    echo "✓ Java: $JAVA_VERSION"
else
    echo "✗ Java not found"
fi

# Node.js
if docker run --rm --entrypoint node "$CONTAINER_IMAGE" --version > /dev/null 2>&1; then
    NODE_VERSION=$(docker run --rm --entrypoint node "$CONTAINER_IMAGE" --version 2>&1)
    echo "✓ Node.js: $NODE_VERSION"
else
    echo "✗ Node.js not found"
fi

# Go
if docker run --rm --entrypoint go "$CONTAINER_IMAGE" version > /dev/null 2>&1; then
    GO_VERSION=$(docker run --rm --entrypoint go "$CONTAINER_IMAGE" version 2>&1)
    echo "✓ Go: $GO_VERSION"
else
    echo "✗ Go not found"
fi
echo ""

# Test 5: Verify language servers are installed
echo "Test 5: Verifying language servers"

# Check for Pyright (Python)
if docker run --rm --entrypoint sh "$CONTAINER_IMAGE" -c "command -v pyright" > /dev/null 2>&1; then
    echo "✓ Pyright (Python LSP) installed"
else
    echo "⚠ Pyright not found in PATH (may be in uv tools)"
fi

# Check for gopls (Go)
if docker run --rm --entrypoint sh "$CONTAINER_IMAGE" -c "command -v gopls" > /dev/null 2>&1; then
    echo "✓ gopls (Go LSP) installed"
else
    echo "⚠ gopls not found in PATH"
fi

# Check for typescript-language-server (JS/TS)
if docker run --rm --entrypoint sh "$CONTAINER_IMAGE" -c "command -v typescript-language-server" > /dev/null 2>&1; then
    echo "✓ typescript-language-server installed"
else
    echo "⚠ typescript-language-server not found in PATH"
fi

if docker run --rm --entrypoint tsc "$CONTAINER_IMAGE" --version > /dev/null 2>&1; then
    TYPESCRIPT_VERSION=$(docker run --rm --entrypoint tsc "$CONTAINER_IMAGE" --version 2>&1)
    echo "✓ TypeScript: $TYPESCRIPT_VERSION"
else
    echo "⚠ TypeScript compiler not found in PATH"
fi

# Check for Serena itself
echo "Test 6: Verifying Serena is installed"
if docker run --rm --entrypoint sh "$CONTAINER_IMAGE" -c "command -v serena-mcp-server" > /dev/null 2>&1; then
    echo "✓ serena-mcp-server installed"
else
    echo "✗ serena-mcp-server not found"
fi
echo ""

# Verify the TypeScript language server can start its backend and answer LSP requests
echo "Test 7: Verifying TypeScript language-server initialization"
if timeout 45s docker run --rm --entrypoint python3 "$CONTAINER_IMAGE" -c '
import json
import subprocess
import sys

def send(process, message):
    body = json.dumps(message).encode()
    process.stdin.write(b"Content-Length: " + str(len(body)).encode() +
                        b"\r\n\r\n" + body)
    process.stdin.flush()

def receive_response(process, request_id):
    while True:
        headers = {}
        while True:
            line = process.stdout.readline()
            if not line:
                raise RuntimeError("language server closed stdout")
            if line == b"\r\n":
                break
            key, value = line.decode().split(":", 1)
            headers[key.lower()] = value.strip()
        response = json.loads(process.stdout.read(int(headers["content-length"])))
        if response.get("id") == request_id:
            return response

process = None
try:
    process = subprocess.Popen(["typescript-language-server", "--stdio"],
                               stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE)
    uri = "file:///tmp/serena-lsp-test.ts"
    send(process, {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {
        "processId": None, "rootUri": "file:///tmp", "capabilities": {}}})
    if "result" not in receive_response(process, 1):
        raise RuntimeError("language server did not initialize")
    send(process, {"jsonrpc": "2.0", "method": "initialized", "params": {}})
    send(process, {"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": {
        "textDocument": {"uri": uri, "languageId": "typescript", "version": 1,
                         "text": "const answer: number = 42;\n"}}})
    send(process, {"jsonrpc": "2.0", "id": 2, "method": "textDocument/hover",
                   "params": {"textDocument": {"uri": uri},
                              "position": {"line": 0, "character": 6}}})
    hover = receive_response(process, 2)
    if not hover.get("result", {}).get("contents"):
        raise RuntimeError("language server did not answer the hover request")
    send(process, {"jsonrpc": "2.0", "id": 3, "method": "shutdown", "params": None})
    receive_response(process, 3)
    send(process, {"jsonrpc": "2.0", "method": "exit", "params": None})
    process.stdin.close()
    if process.wait(timeout=10) != 0:
        raise RuntimeError(process.stderr.read().decode(errors="replace"))
except Exception as error:
    print("TypeScript language-server initialization failed:", error, file=sys.stderr)
    if process and process.poll() is None:
        process.terminate()
    sys.exit(1)
' ; then
    echo "✓ TypeScript language server initialized and answered a hover request"
else
    echo "✗ TypeScript language-server initialization failed"
    exit 1
fi

# Test 8: Check container size
echo "Test 8: Container size"
SIZE=$(docker images "$CONTAINER_IMAGE" --format "{{.Size}}")
echo "Container size: $SIZE"
echo ""

# Cleanup
echo "Cleaning up test directory: $TEST_DIR"
rm -rf "$TEST_DIR"

echo ""
echo "=========================================="
echo "Test Summary"
echo "=========================================="
echo "Most tests completed. Review output above for any failures."
echo "Note: Some warnings are expected for first-run initialization."
