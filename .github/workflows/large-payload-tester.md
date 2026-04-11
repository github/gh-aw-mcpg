---
name: Large Payload Tester
description: Test the MCP Gateway's ability to handle large payloads
on:
  roles: [admin, maintainer, write]
  schedule: daily
  workflow_dispatch:

permissions:
  contents: read
  issues: read
  pull-requests: read


network:
  allowed:
    - defaults
    - go
    - containers

tools:
  github:
    toolsets: [default]
    allowed-repos: ["github/gh-aw-mcpg"]
    min-integrity: unapproved
  bash: ["*"]

mcp-servers:
  filesystem:
    type: stdio
    container: "mcp/filesystem"
    entrypointArgs: ["/workspace"]
    env:
      ALLOWED_PATHS: "/workspace"
    mounts:
      - "/tmp/mcp-test-fs:/workspace:ro"

  github:
    type: stdio
    container: "ghcr.io/github/github-mcp-server:v0.30.2"
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "${{ secrets.GITHUB_TOKEN }}"

sandbox:
  mcp:
    container: "ghcr.io/github/gh-aw-mcpg"

safe-outputs:
  threat-detection:
    enabled: false
  create-issue:
    title-prefix: "[large-payload-test] "
    labels: [mcp-gateway, test, automation]
    max: 10
    close-older-issues: true

steps:
  - name: Setup Test Environment
    run: |
      TEST_FS="/tmp/mcp-test-fs"
      SECRET_FILE="secret.txt"
      LARGE_PAYLOAD_FILE="large-test-file.json"
      # Create test data directory (payload directory will be created by gateway on-demand)
      mkdir -p $TEST_FS
      
      # Generate a unique secret for this test run
      # Use uuidgen if available, otherwise use timestamp with nanoseconds for better entropy
      if command -v uuidgen >/dev/null 2>&1; then
        TEST_SECRET="test-secret-$(uuidgen)"
      else
        TEST_SECRET="test-secret-$(date +%s%N)-$$"
      fi
      echo $TEST_SECRET > $TEST_FS/$SECRET_FILE
      # Create a large test file (~500KB) with the secret embedded in JSON
      # This file will be read by the filesystem MCP server, causing a large payload
      cat > $TEST_FS/$LARGE_PAYLOAD_FILE <<'EOF'
      {
        "test_run_id": "PLACEHOLDER_RUN_ID",
        "test_timestamp": "PLACEHOLDER_TIMESTAMP",
        "purpose": "Testing large MCP payload storage and retrieval",
        "data": {
          "large_array": [],
          "metadata": {
            "generated_by": "large-payload-tester workflow",
            "repository": "PLACEHOLDER_REPO",
            "workflow_run_url": "PLACEHOLDER_URL"
          }
        },
        "padding": ""
      }
      EOF
      
      # Use jq to properly populate the JSON with dynamic values and generate large array
      # Generating 2000 items + 400KB padding to create ~500KB file
      # Secret is only included once in the middle item (index 1000)
      jq --arg secret "$TEST_SECRET" \
         --arg run_id "${{ github.run_id }}" \
         --arg timestamp "$(date -Iseconds)" \
         --arg repo "${{ github.repository }}" \
         --arg url "${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}" \
         '.test_run_id = $run_id | 
          .test_timestamp = $timestamp | 
          .data.metadata.repository = $repo | 
          .data.metadata.workflow_run_url = $url | 
          .data.large_array = [range(2000) | . as $i | if $i == 1000 then {id: $i, value: ("item-" + tostring), secret_reference: $secret, extra_data: ("data-" + tostring + "-" * 50)} else {id: $i, value: ("item-" + tostring), random_data: ("rand-" + ($i * 17 % 9973 | tostring) + "-" + ($i * 31 % 8191 | tostring)), extra_data: ("data-" + tostring + "-" * 50)} end] |
          .padding = ("X" * 400000)' \
         $TEST_FS/$LARGE_PAYLOAD_FILE > $TEST_FS/$LARGE_PAYLOAD_FILE.tmp
      
      mv $TEST_FS/$LARGE_PAYLOAD_FILE.tmp $TEST_FS/$LARGE_PAYLOAD_FILE
      
      # Verify file was created and is large enough
      FILE_SIZE=$(wc -c < $TEST_FS/$LARGE_PAYLOAD_FILE)
      echo "Created $LARGE_PAYLOAD_FILE with size: $FILE_SIZE bytes (~$(($FILE_SIZE / 1024))KB)"
      if [ "$FILE_SIZE" -lt 512000 ]; then
        echo "WARNING: Test file is smaller than expected ($FILE_SIZE bytes < 500KB)"
        echo "Continuing with test anyway..."
      fi
      
      echo "Test environment setup complete"
      echo "Large file stored in: $TEST_FS/$LARGE_PAYLOAD_FILE"
      grep -H $TEST_SECRET $TEST_FS/$LARGE_PAYLOAD_FILE
      echo "Secret stored in $TEST_FS/$SECRET_FILE"
      grep -H $TEST_SECRET $TEST_FS/$SECRET_FILE

post-steps:
  - name: Upload Test Results
    if: always()
    uses: actions/upload-artifact@v4
    with:
      name: mcp-stress-test-results
      path: |
        /tmp/mcp-stress-results/
        /tmp/mcp-stress-test/logs/
      retention-days: 30


timeout-minutes: 10
strict: false
---

<!-- Edit the file linked below to modify the agent without recompilation. Feel free to move the entire markdown body to that file. -->
@./agentics/large-payload-tester.md