---
description: Smoke test validating that the MCP Gateway proxy applies DIFC integrity filtering to actions/github-script (octokit) API calls
on:
  workflow_dispatch:
  pull_request:
    types: [labeled]
    names: ["smoke"]
  reaction: "eyes"
permissions:
  contents: read
  issues: read
  pull-requests: read
  actions: read

name: "Smoke: Proxy + github-script"
engine:
  id: copilot
strict: false
imports:
  - shared/reporting.md
network:
  allowed:
    - defaults
    - github
    - github.com
    - rust
tools:
  agentic-workflows:
  cache-memory: true
  github:
    toolsets: [repos, issues]
    allowed-repos: ["github/gh-aw-mcpg"]
    min-integrity: approved
  edit:
  bash:
    - "cat"
    - "echo"
    - "date"
    - "jq"
    - "wc"
    - "head"
    - "tail"
    - "grep"
    - "gh"
    - "curl"
sandbox:
  mcp:
    container: "ghcr.io/github/gh-aw-mcpg"
steps:
  # ── Build the gateway container image from source ──────────────────
  - name: Build MCP Gateway image
    run: |
      # Install Rust and WASM target if not present
      if ! command -v rustup &>/dev/null; then
        curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable
        source "$HOME/.cargo/env"
      fi
      rustup target add wasm32-wasip1

      # Build the Rust WASM guard (required by Dockerfile)
      cd guards/github-guard/rust-guard
      bash build.sh
      cd ../../..

      # Build Docker image
      docker build -t awmg-local:latest .

  # ── Start the DIFC proxy ──────────────────────────────────────────
  - name: Start DIFC proxy
    env:
      GH_TOKEN: ${{ secrets.GH_AW_GITHUB_MCP_SERVER_TOKEN || secrets.GH_AW_GITHUB_TOKEN || secrets.GITHUB_TOKEN }}
    run: |
      PROXY_LOG_DIR=/tmp/gh-aw/proxy-logs
      MCP_LOG_DIR=/tmp/gh-aw/mcp-logs
      RESULTS_DIR=/tmp/gh-aw/github-script-results
      mkdir -p "$PROXY_LOG_DIR" "$MCP_LOG_DIR" "$RESULTS_DIR"

      POLICY='{"allow-only":{"repos":["github/gh-aw-mcpg"],"min-integrity":"approved"}}'

      # Plain HTTP — avoids TLS cert trust issues with undici/Node.js
      docker run -d --name awmg-proxy --network host \
        -e GH_TOKEN \
        -e DEBUG='*' \
        -v "$PROXY_LOG_DIR:$PROXY_LOG_DIR" \
        -v "$MCP_LOG_DIR:$MCP_LOG_DIR" \
        awmg-local:latest proxy \
          --policy "$POLICY" \
          --listen 0.0.0.0:18443 \
          --log-dir "$MCP_LOG_DIR" \
          --guards-mode filter \
          --trusted-bots github-actions[bot],dependabot[bot],copilot

      # Wait for proxy health check
      PROXY_READY=false
      for i in $(seq 1 30); do
        if curl -sf "http://localhost:18443/health" -o /dev/null 2>/dev/null; then
          echo "DIFC proxy ready on port 18443"
          PROXY_READY=true
          break
        fi
        sleep 1
      done

      if [ "$PROXY_READY" = "false" ]; then
        echo "::error::DIFC proxy failed to start"
        docker logs awmg-proxy 2>&1 | tail -30 || true
        exit 1
      fi

      echo "RESULTS_DIR=$RESULTS_DIR" >> "$GITHUB_ENV"

  # ── Test 1: In-scope repo — list issues (REST) ────────────────────
  - name: "Test 1: In-scope list issues (REST)"
    uses: actions/github-script@v8
    with:
      base-url: "http://localhost:18443"
      script: |
        const fs = require('fs');
        const dir = process.env.RESULTS_DIR;
        try {
          const result = await github.rest.issues.listForRepo({
            owner: 'github',
            repo: 'gh-aw-mcpg',
            per_page: 5,
            state: 'open'
          });
          const summary = {
            test: 'in-scope-list-issues',
            status_code: result.status,
            item_count: result.data.length,
            items: result.data.map(i => ({
              number: i.number,
              title: i.title.substring(0, 60),
              author: i.user?.login || 'unknown',
              author_association: i.author_association || 'N/A'
            }))
          };
          fs.writeFileSync(`${dir}/test1-in-scope-issues.json`, JSON.stringify(summary, null, 2));
          console.log(`✅ In-scope list_issues: ${result.data.length} items returned`);
        } catch (err) {
          const summary = { test: 'in-scope-list-issues', error: err.message, status: err.status };
          fs.writeFileSync(`${dir}/test1-in-scope-issues.json`, JSON.stringify(summary, null, 2));
          console.log(`❌ In-scope list_issues failed: ${err.message}`);
        }

  # ── Test 2: Out-of-scope repo — list issues (REST) ────────────────
  - name: "Test 2: Out-of-scope list issues (REST)"
    uses: actions/github-script@v8
    with:
      base-url: "http://localhost:18443"
      script: |
        const fs = require('fs');
        const dir = process.env.RESULTS_DIR;
        try {
          const result = await github.rest.issues.listForRepo({
            owner: 'octocat',
            repo: 'Hello-World',
            per_page: 5
          });
          const summary = {
            test: 'out-of-scope-list-issues',
            status_code: result.status,
            item_count: result.data.length,
            items: result.data.map(i => ({
              number: i.number,
              title: i.title.substring(0, 60),
              author: i.user?.login || 'unknown'
            }))
          };
          fs.writeFileSync(`${dir}/test2-out-of-scope-issues.json`, JSON.stringify(summary, null, 2));
          console.log(`Out-of-scope list_issues: ${result.data.length} items (expected: 0 or blocked)`);
        } catch (err) {
          const summary = { test: 'out-of-scope-list-issues', error: err.message, status: err.status };
          fs.writeFileSync(`${dir}/test2-out-of-scope-issues.json`, JSON.stringify(summary, null, 2));
          console.log(`Out-of-scope list_issues error (may be expected): ${err.message}`);
        }

  # ── Test 3: In-scope repo — GraphQL query ──────────────────────────
  - name: "Test 3: In-scope GraphQL query"
    uses: actions/github-script@v8
    with:
      base-url: "http://localhost:18443"
      script: |
        const fs = require('fs');
        const dir = process.env.RESULTS_DIR;
        try {
          const result = await github.graphql(`
            query($owner: String!, $repo: String!, $count: Int!) {
              repository(owner: $owner, name: $repo) {
                issues(first: $count, states: OPEN, orderBy: {field: CREATED_AT, direction: DESC}) {
                  totalCount
                  nodes {
                    number
                    title
                    author { login }
                    authorAssociation
                  }
                }
              }
            }
          `, { owner: 'github', repo: 'gh-aw-mcpg', count: 5 });
          const issues = result.repository.issues;
          const summary = {
            test: 'in-scope-graphql-issues',
            total_count: issues.totalCount,
            item_count: issues.nodes.length,
            items: issues.nodes.map(i => ({
              number: i.number,
              title: i.title.substring(0, 60),
              author: i.author?.login || 'unknown',
              author_association: i.authorAssociation || 'N/A'
            }))
          };
          fs.writeFileSync(`${dir}/test3-in-scope-graphql.json`, JSON.stringify(summary, null, 2));
          console.log(`✅ In-scope GraphQL issues: ${issues.nodes.length} items`);
        } catch (err) {
          const summary = { test: 'in-scope-graphql-issues', error: err.message };
          fs.writeFileSync(`${dir}/test3-in-scope-graphql.json`, JSON.stringify(summary, null, 2));
          console.log(`❌ In-scope GraphQL failed: ${err.message}`);
        }

  # ── Test 4: Out-of-scope repo — GraphQL query ─────────────────────
  - name: "Test 4: Out-of-scope GraphQL query"
    uses: actions/github-script@v8
    with:
      base-url: "http://localhost:18443"
      script: |
        const fs = require('fs');
        const dir = process.env.RESULTS_DIR;
        try {
          const result = await github.graphql(`
            query($owner: String!, $repo: String!, $count: Int!) {
              repository(owner: $owner, name: $repo) {
                issues(first: $count, states: OPEN) {
                  totalCount
                  nodes { number title author { login } }
                }
              }
            }
          `, { owner: 'octocat', repo: 'Hello-World', count: 5 });
          const issues = result.repository.issues;
          const summary = {
            test: 'out-of-scope-graphql-issues',
            total_count: issues.totalCount,
            item_count: issues.nodes.length,
            items: issues.nodes.slice(0, 3).map(i => ({
              number: i.number,
              author: i.author?.login || 'unknown'
            }))
          };
          fs.writeFileSync(`${dir}/test4-out-of-scope-graphql.json`, JSON.stringify(summary, null, 2));
          console.log(`Out-of-scope GraphQL issues: ${issues.nodes.length} items (expected: 0 or blocked)`);
        } catch (err) {
          const summary = { test: 'out-of-scope-graphql-issues', error: err.message };
          fs.writeFileSync(`${dir}/test4-out-of-scope-graphql.json`, JSON.stringify(summary, null, 2));
          console.log(`Out-of-scope GraphQL error (may be expected): ${err.message}`);
        }

  # ── Test 5: In-scope search (REST) ─────────────────────────────────
  - name: "Test 5: In-scope search code (REST)"
    uses: actions/github-script@v8
    with:
      base-url: "http://localhost:18443"
      script: |
        const fs = require('fs');
        const dir = process.env.RESULTS_DIR;
        try {
          const result = await github.rest.search.code({
            q: 'repo:github/gh-aw-mcpg filename:README',
            per_page: 3
          });
          const summary = {
            test: 'in-scope-search-code',
            total_count: result.data.total_count,
            item_count: result.data.items.length,
            items: result.data.items.map(i => ({
              name: i.name,
              path: i.path,
              repo: i.repository?.full_name
            }))
          };
          fs.writeFileSync(`${dir}/test5-in-scope-search.json`, JSON.stringify(summary, null, 2));
          console.log(`✅ In-scope search_code: ${result.data.items.length} items`);
        } catch (err) {
          const summary = { test: 'in-scope-search-code', error: err.message, status: err.status };
          fs.writeFileSync(`${dir}/test5-in-scope-search.json`, JSON.stringify(summary, null, 2));
          console.log(`❌ In-scope search failed: ${err.message}`);
        }

  # ── Test 6: Integrity filtering — bot-authored issues ──────────────
  - name: "Test 6: Integrity filtering of bot-authored content"
    uses: actions/github-script@v8
    with:
      base-url: "http://localhost:18443"
      script: |
        const fs = require('fs');
        const dir = process.env.RESULTS_DIR;
        try {
          // Search for issues authored by github-actions[bot] — these should pass
          // because trusted bots get writer (approved) integrity
          const result = await github.rest.issues.listForRepo({
            owner: 'github',
            repo: 'gh-aw-mcpg',
            per_page: 20,
            state: 'all',
            creator: 'github-actions[bot]'
          });
          const summary = {
            test: 'integrity-bot-authored',
            status_code: result.status,
            bot_issue_count: result.data.length,
            items: result.data.slice(0, 5).map(i => ({
              number: i.number,
              title: i.title.substring(0, 60),
              author: i.user?.login || 'unknown',
              author_association: i.author_association || 'N/A'
            })),
            note: 'Bot-authored issues should pass integrity filter (trusted bot = approved)'
          };
          fs.writeFileSync(`${dir}/test6-bot-integrity.json`, JSON.stringify(summary, null, 2));
          console.log(`Bot-authored issues visible: ${result.data.length}`);
        } catch (err) {
          const summary = { test: 'integrity-bot-authored', error: err.message, status: err.status };
          fs.writeFileSync(`${dir}/test6-bot-integrity.json`, JSON.stringify(summary, null, 2));
          console.log(`Bot integrity test error: ${err.message}`);
        }

  # ── gh CLI tests (mirror github-script tests via direct proxy URLs) ─
  - name: "Test 7-12: gh CLI tests via proxy"
    env:
      GH_TOKEN: ${{ secrets.GH_AW_GITHUB_MCP_SERVER_TOKEN || secrets.GH_AW_GITHUB_TOKEN || secrets.GITHUB_TOKEN }}
    run: |
      RESULTS_DIR="${RESULTS_DIR:-/tmp/gh-aw/github-script-results}"
      PROXY="http://localhost:18443"

      # Test 7: In-scope list issues (gh CLI REST)
      echo "--- Test 7: In-scope list issues (gh CLI) ---"
      RESPONSE=$(gh api "$PROXY/repos/github/gh-aw-mcpg/issues?per_page=5&state=open" --jq 'length' 2>&1) || true
      COUNT=$(echo "$RESPONSE" | head -1)
      if [ "$COUNT" -gt 0 ] 2>/dev/null; then
        echo "✅ Test 7: $COUNT items"
        echo "{\"test\":\"gh-in-scope-list-issues\",\"item_count\":$COUNT}" > "$RESULTS_DIR/test7-gh-in-scope-issues.json"
      else
        echo "❌ Test 7: $RESPONSE"
        echo "{\"test\":\"gh-in-scope-list-issues\",\"error\":\"unexpected: $RESPONSE\"}" > "$RESULTS_DIR/test7-gh-in-scope-issues.json"
      fi

      # Test 8: Out-of-scope list issues (gh CLI REST)
      echo "--- Test 8: Out-of-scope list issues (gh CLI) ---"
      RESPONSE=$(gh api "$PROXY/repos/octocat/Hello-World/issues?per_page=5" --jq 'length' 2>&1) || true
      COUNT=$(echo "$RESPONSE" | head -1)
      if [ "$COUNT" = "0" ] 2>/dev/null; then
        echo "✅ Test 8: 0 items (blocked)"
        echo "{\"test\":\"gh-out-of-scope-list-issues\",\"item_count\":0}" > "$RESULTS_DIR/test8-gh-out-of-scope-issues.json"
      else
        echo "Test 8: $COUNT items (expected: 0) — raw: $RESPONSE"
        echo "{\"test\":\"gh-out-of-scope-list-issues\",\"item_count\":\"$COUNT\",\"raw\":\"$RESPONSE\"}" > "$RESULTS_DIR/test8-gh-out-of-scope-issues.json"
      fi

      # Test 9: In-scope GraphQL (gh CLI)
      echo "--- Test 9: In-scope GraphQL (gh CLI) ---"
      QUERY='query { repository(owner:"github", name:"gh-aw-mcpg") { issues(first:5, states:OPEN) { totalCount nodes { number title author { login } authorAssociation } } } }'
      RESPONSE=$(gh api "$PROXY/graphql" -f query="$QUERY" --jq '.data.repository.issues.totalCount' 2>&1) || true
      COUNT=$(echo "$RESPONSE" | head -1)
      if [ "$COUNT" -gt 0 ] 2>/dev/null; then
        echo "✅ Test 9: totalCount=$COUNT"
        echo "{\"test\":\"gh-in-scope-graphql\",\"total_count\":$COUNT}" > "$RESULTS_DIR/test9-gh-in-scope-graphql.json"
      else
        echo "❌ Test 9: $RESPONSE"
        echo "{\"test\":\"gh-in-scope-graphql\",\"error\":\"$RESPONSE\"}" > "$RESULTS_DIR/test9-gh-in-scope-graphql.json"
      fi

      # Test 10: Out-of-scope GraphQL (gh CLI)
      echo "--- Test 10: Out-of-scope GraphQL (gh CLI) ---"
      QUERY='query { repository(owner:"octocat", name:"Hello-World") { issues(first:5, states:OPEN) { totalCount nodes { number title author { login } } } } }'
      RESPONSE=$(gh api "$PROXY/graphql" -f query="$QUERY" --jq '.data.repository.issues.totalCount' 2>&1) || true
      COUNT=$(echo "$RESPONSE" | head -1)
      if [ "$COUNT" = "0" ] || [ -z "$COUNT" ] || [ "$COUNT" = "null" ]; then
        echo "✅ Test 10: blocked (count=$COUNT)"
        echo "{\"test\":\"gh-out-of-scope-graphql\",\"total_count\":0,\"raw\":\"$RESPONSE\"}" > "$RESULTS_DIR/test10-gh-out-of-scope-graphql.json"
      else
        echo "Test 10: totalCount=$COUNT (expected: 0)"
        echo "{\"test\":\"gh-out-of-scope-graphql\",\"total_count\":$COUNT}" > "$RESULTS_DIR/test10-gh-out-of-scope-graphql.json"
      fi

      # Test 11: In-scope search code (gh CLI) — uses /api/v3/ prefix to test StripGHHostPrefix
      echo "--- Test 11: In-scope search code (gh CLI, /api/v3 prefix) ---"
      RESPONSE=$(gh api "$PROXY/api/v3/search/code?q=repo:github/gh-aw-mcpg+filename:README&per_page=3" --jq '.total_count' 2>&1) || true
      COUNT=$(echo "$RESPONSE" | head -1)
      if [ "$COUNT" -gt 0 ] 2>/dev/null; then
        echo "✅ Test 11: $COUNT results (via /api/v3/ prefix)"
        echo "{\"test\":\"gh-in-scope-search\",\"total_count\":$COUNT,\"note\":\"used /api/v3/ prefix\"}" > "$RESULTS_DIR/test11-gh-in-scope-search.json"
      else
        echo "❌ Test 11: $RESPONSE"
        echo "{\"test\":\"gh-in-scope-search\",\"error\":\"$RESPONSE\"}" > "$RESULTS_DIR/test11-gh-in-scope-search.json"
      fi

      # Test 12: In-scope get file contents (gh CLI) — uses /api/v3/ prefix
      echo "--- Test 12: In-scope get file contents (gh CLI, /api/v3 prefix) ---"
      RESPONSE=$(gh api "$PROXY/api/v3/repos/github/gh-aw-mcpg/contents/README.md" --jq '.name' 2>&1) || true
      if [ "$RESPONSE" = "README.md" ]; then
        echo "✅ Test 12: $RESPONSE (via /api/v3/ prefix)"
        echo "{\"test\":\"gh-in-scope-file-contents\",\"name\":\"$RESPONSE\",\"note\":\"used /api/v3/ prefix\"}" > "$RESULTS_DIR/test12-gh-in-scope-file.json"
      else
        echo "❌ Test 12: $RESPONSE"
        echo "{\"test\":\"gh-in-scope-file-contents\",\"error\":\"$RESPONSE\"}" > "$RESULTS_DIR/test12-gh-in-scope-file.json"
      fi

      echo "--- gh CLI tests complete ---"

  # ── Test 13-47: Comprehensive proxy route coverage ─────────────────
  - name: "Test 13-47: Comprehensive proxy route coverage"
    env:
      GH_TOKEN: ${{ secrets.GH_AW_GITHUB_MCP_SERVER_TOKEN || secrets.GH_AW_GITHUB_TOKEN || secrets.GITHUB_TOKEN }}
    run: |
      RESULTS_DIR="${RESULTS_DIR:-/tmp/gh-aw/github-script-results}"
      PROXY="http://localhost:18443"
      PASS=0; FAIL=0; SKIP=0

      write_result() {
        local num="$1" name="$2" tool="$3" scope="$4" result="$5" count="$6" note="$7"
        echo "{\"test\":\"${name}\",\"tool\":\"${tool}\",\"scope\":\"${scope}\",\"result\":\"${result}\",\"item_count\":${count:-0},\"note\":\"${note}\"}" \
          > "$RESULTS_DIR/test${num}-${name}.json"
        case "$result" in
          pass) echo "✅ Test $num ($name): $note"; PASS=$((PASS+1)) ;;
          skip) echo "⏭️  Test $num ($name): $note"; SKIP=$((SKIP+1)) ;;
          *)    echo "❌ Test $num ($name): $note"; FAIL=$((FAIL+1)) ;;
        esac
      }

      # ── Discovery: extract IDs from in-scope repo ──────────────────
      echo "=== Discovering test fixtures ==="

      ISSUE_NUM=$(gh api "$PROXY/repos/github/gh-aw-mcpg/issues?per_page=1&state=all" --jq '.[0].number' 2>&1) || true
      [ "$ISSUE_NUM" -gt 0 ] 2>/dev/null || ISSUE_NUM=""
      echo "Issue: ${ISSUE_NUM:-none}"

      PR_NUM=$(gh api "$PROXY/repos/github/gh-aw-mcpg/pulls?per_page=1&state=all" --jq '.[0].number' 2>&1) || true
      [ "$PR_NUM" -gt 0 ] 2>/dev/null || PR_NUM=""
      echo "PR: ${PR_NUM:-none}"

      COMMIT_SHA=$(gh api "$PROXY/repos/github/gh-aw-mcpg/commits?per_page=1" --jq '.[0].sha' 2>&1) || true
      [ "${#COMMIT_SHA}" -ge 7 ] 2>/dev/null || COMMIT_SHA=""
      echo "Commit: ${COMMIT_SHA:-none}"

      echo ""
      echo "=== Tests 13-47: Comprehensive proxy route coverage ==="

      # ── Issues (13-16) ─────────────────────────────────────────────

      echo "--- Test 13: issue_read in-scope ---"
      if [ -n "$ISSUE_NUM" ]; then
        RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/issues/$ISSUE_NUM" --jq '.number' 2>&1) || true
        if [ "$RESP" = "$ISSUE_NUM" ]; then
          write_result 13 issue-read-inscope issue_read in-scope pass 1 "issue #$ISSUE_NUM"
        else
          write_result 13 issue-read-inscope issue_read in-scope fail 0 "unexpected response"
        fi
      else
        write_result 13 issue-read-inscope issue_read in-scope skip 0 "no issues to test"
      fi

      echo "--- Test 14: issue_read out-of-scope ---"
      if RESP=$(gh api "$PROXY/repos/octocat/Hello-World/issues/1" --jq '.number' 2>/dev/null); then
        if [ -n "$RESP" ] && [ "$RESP" != "null" ]; then
          write_result 14 issue-read-outscope issue_read out-of-scope fail 1 "returned issue #$RESP"
        else
          write_result 14 issue-read-outscope issue_read out-of-scope pass 0 "blocked"
        fi
      else
        write_result 14 issue-read-outscope issue_read out-of-scope pass 0 "blocked (error)"
      fi

      echo "--- Test 15: issue_read comments in-scope ---"
      if [ -n "$ISSUE_NUM" ]; then
        RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/issues/$ISSUE_NUM/comments?per_page=5" --jq 'length' 2>&1) || true
        if [ "$RESP" -ge 0 ] 2>/dev/null; then
          write_result 15 issue-comments-inscope issue_read in-scope pass "$RESP" "$RESP comments"
        else
          write_result 15 issue-comments-inscope issue_read in-scope fail 0 "unexpected response"
        fi
      else
        write_result 15 issue-comments-inscope issue_read in-scope skip 0 "no issues to test"
      fi

      echo "--- Test 16: issue_read labels in-scope ---"
      if [ -n "$ISSUE_NUM" ]; then
        RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/issues/$ISSUE_NUM/labels" --jq 'length' 2>&1) || true
        if [ "$RESP" -ge 0 ] 2>/dev/null; then
          write_result 16 issue-labels-inscope issue_read in-scope pass "$RESP" "$RESP labels"
        else
          write_result 16 issue-labels-inscope issue_read in-scope fail 0 "unexpected response"
        fi
      else
        write_result 16 issue-labels-inscope issue_read in-scope skip 0 "no issues to test"
      fi

      # ── Pull Requests (17-22) ──────────────────────────────────────

      echo "--- Test 17: list_pull_requests in-scope ---"
      RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/pulls?per_page=5&state=all" --jq 'length' 2>&1) || true
      if [ "$RESP" -gt 0 ] 2>/dev/null; then
        write_result 17 list-prs-inscope list_pull_requests in-scope pass "$RESP" "$RESP PRs"
      elif [ "$RESP" = "0" ]; then
        write_result 17 list-prs-inscope list_pull_requests in-scope skip 0 "no PRs found"
      else
        write_result 17 list-prs-inscope list_pull_requests in-scope fail 0 "unexpected response"
      fi

      echo "--- Test 18: list_pull_requests out-of-scope ---"
      RESP=$(gh api "$PROXY/repos/octocat/Hello-World/pulls?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" = "0" ]; then
        write_result 18 list-prs-outscope list_pull_requests out-of-scope pass 0 "blocked"
      else
        write_result 18 list-prs-outscope list_pull_requests out-of-scope fail "${RESP:-0}" "expected 0, got $RESP"
      fi

      echo "--- Test 19: pull_request_read in-scope ---"
      if [ -n "$PR_NUM" ]; then
        RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/pulls/$PR_NUM" --jq '.number' 2>&1) || true
        if [ "$RESP" = "$PR_NUM" ]; then
          write_result 19 pr-read-inscope pull_request_read in-scope pass 1 "PR #$PR_NUM"
        else
          write_result 19 pr-read-inscope pull_request_read in-scope fail 0 "unexpected response"
        fi
      else
        write_result 19 pr-read-inscope pull_request_read in-scope skip 0 "no PRs to test"
      fi

      echo "--- Test 20: pull_request_read files in-scope ---"
      if [ -n "$PR_NUM" ]; then
        RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/pulls/$PR_NUM/files?per_page=5" --jq 'length' 2>&1) || true
        if [ "$RESP" -ge 0 ] 2>/dev/null; then
          write_result 20 pr-files-inscope pull_request_read in-scope pass "$RESP" "$RESP files"
        else
          write_result 20 pr-files-inscope pull_request_read in-scope fail 0 "unexpected response"
        fi
      else
        write_result 20 pr-files-inscope pull_request_read in-scope skip 0 "no PRs to test"
      fi

      echo "--- Test 21: pull_request_read reviews in-scope ---"
      if [ -n "$PR_NUM" ]; then
        RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/pulls/$PR_NUM/reviews" --jq 'length' 2>&1) || true
        if [ "$RESP" -ge 0 ] 2>/dev/null; then
          write_result 21 pr-reviews-inscope pull_request_read in-scope pass "$RESP" "$RESP reviews"
        else
          write_result 21 pr-reviews-inscope pull_request_read in-scope fail 0 "unexpected response"
        fi
      else
        write_result 21 pr-reviews-inscope pull_request_read in-scope skip 0 "no PRs to test"
      fi

      echo "--- Test 22: pull_request_read comments in-scope ---"
      if [ -n "$PR_NUM" ]; then
        RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/pulls/$PR_NUM/comments" --jq 'length' 2>&1) || true
        if [ "$RESP" -ge 0 ] 2>/dev/null; then
          write_result 22 pr-comments-inscope pull_request_read in-scope pass "$RESP" "$RESP comments"
        else
          write_result 22 pr-comments-inscope pull_request_read in-scope fail 0 "unexpected response"
        fi
      else
        write_result 22 pr-comments-inscope pull_request_read in-scope skip 0 "no PRs to test"
      fi

      # ── Commits (23-25) ────────────────────────────────────────────

      echo "--- Test 23: list_commits in-scope ---"
      RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/commits?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" -gt 0 ] 2>/dev/null; then
        write_result 23 list-commits-inscope list_commits in-scope pass "$RESP" "$RESP commits"
      else
        write_result 23 list-commits-inscope list_commits in-scope fail 0 "unexpected: $RESP"
      fi

      echo "--- Test 24: list_commits out-of-scope ---"
      RESP=$(gh api "$PROXY/repos/octocat/Hello-World/commits?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" = "0" ]; then
        write_result 24 list-commits-outscope list_commits out-of-scope pass 0 "blocked"
      else
        write_result 24 list-commits-outscope list_commits out-of-scope fail "${RESP:-0}" "expected 0, got $RESP"
      fi

      echo "--- Test 25: get_commit in-scope ---"
      if [ -n "$COMMIT_SHA" ]; then
        RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/commits/$COMMIT_SHA" --jq '.sha' 2>&1) || true
        if [ "$RESP" = "$COMMIT_SHA" ]; then
          write_result 25 get-commit-inscope get_commit in-scope pass 1 "sha ${COMMIT_SHA:0:12}"
        else
          write_result 25 get-commit-inscope get_commit in-scope fail 0 "unexpected response"
        fi
      else
        write_result 25 get-commit-inscope get_commit in-scope skip 0 "no commits to test"
      fi

      # ── Branches & Tags (26-29) ────────────────────────────────────

      echo "--- Test 26: list_branches in-scope ---"
      RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/branches?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" -gt 0 ] 2>/dev/null; then
        write_result 26 list-branches-inscope list_branches in-scope pass "$RESP" "$RESP branches"
      else
        write_result 26 list-branches-inscope list_branches in-scope fail 0 "unexpected: $RESP"
      fi

      echo "--- Test 27: list_branches out-of-scope ---"
      RESP=$(gh api "$PROXY/repos/octocat/Hello-World/branches?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" = "0" ]; then
        write_result 27 list-branches-outscope list_branches out-of-scope pass 0 "blocked"
      else
        write_result 27 list-branches-outscope list_branches out-of-scope fail "${RESP:-0}" "expected 0, got $RESP"
      fi

      echo "--- Test 28: list_tags in-scope ---"
      RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/tags?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" -ge 0 ] 2>/dev/null; then
        write_result 28 list-tags-inscope list_tags in-scope pass "$RESP" "$RESP tags"
      else
        write_result 28 list-tags-inscope list_tags in-scope fail 0 "unexpected: $RESP"
      fi

      echo "--- Test 29: list_tags out-of-scope ---"
      RESP=$(gh api "$PROXY/repos/octocat/Hello-World/tags?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" = "0" ]; then
        write_result 29 list-tags-outscope list_tags out-of-scope pass 0 "blocked"
      else
        write_result 29 list-tags-outscope list_tags out-of-scope fail "${RESP:-0}" "expected 0, got $RESP"
      fi

      # ── Releases (30-32) ───────────────────────────────────────────

      echo "--- Test 30: list_releases in-scope ---"
      RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/releases?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" -ge 0 ] 2>/dev/null; then
        write_result 30 list-releases-inscope list_releases in-scope pass "$RESP" "$RESP releases"
      else
        write_result 30 list-releases-inscope list_releases in-scope fail 0 "unexpected: $RESP"
      fi

      echo "--- Test 31: list_releases out-of-scope ---"
      RESP=$(gh api "$PROXY/repos/octocat/Hello-World/releases?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" = "0" ]; then
        write_result 31 list-releases-outscope list_releases out-of-scope pass 0 "blocked"
      else
        write_result 31 list-releases-outscope list_releases out-of-scope fail "${RESP:-0}" "expected 0, got $RESP"
      fi

      echo "--- Test 32: get_latest_release in-scope ---"
      if RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/releases/latest" --jq '.tag_name' 2>/dev/null); then
        if [ -n "$RESP" ] && [ "$RESP" != "null" ]; then
          write_result 32 latest-release-inscope get_latest_release in-scope pass 1 "tag $RESP"
        else
          write_result 32 latest-release-inscope get_latest_release in-scope skip 0 "no releases"
        fi
      else
        write_result 32 latest-release-inscope get_latest_release in-scope skip 0 "404 no releases"
      fi

      # ── Labels (33-35) ─────────────────────────────────────────────

      echo "--- Test 33: list_labels in-scope ---"
      RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/labels?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" -ge 0 ] 2>/dev/null; then
        write_result 33 list-labels-inscope list_labels in-scope pass "$RESP" "$RESP labels (0 expected: labels lack authorship)"
      else
        write_result 33 list-labels-inscope list_labels in-scope fail 0 "unexpected: $RESP"
      fi

      echo "--- Test 34: list_labels out-of-scope ---"
      RESP=$(gh api "$PROXY/repos/octocat/Hello-World/labels?per_page=5" --jq 'length' 2>&1) || true
      if [ "$RESP" = "0" ]; then
        write_result 34 list-labels-outscope list_labels out-of-scope pass 0 "blocked"
      else
        write_result 34 list-labels-outscope list_labels out-of-scope fail "${RESP:-0}" "expected 0, got $RESP"
      fi

      echo "--- Test 35: get_label in-scope ---"
      RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/labels/bug" --jq '.name' 2>&1) || true
      if [ "$RESP" = "bug" ]; then
        write_result 35 get-label-inscope get_label in-scope pass 1 "label: bug"
      else
        if RESP2=$(gh api "$PROXY/repos/github/gh-aw-mcpg/labels/enhancement" --jq '.name' 2>/dev/null); then
          write_result 35 get-label-inscope get_label in-scope pass 1 "label: $RESP2"
        else
          write_result 35 get-label-inscope get_label in-scope skip 0 "no known label found"
        fi
      fi

      # ── Actions (36-37) ────────────────────────────────────────────

      echo "--- Test 36: actions_list workflows in-scope ---"
      RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/actions/workflows?per_page=3" --jq '.total_count' 2>&1) || true
      if [ "$RESP" -gt 0 ] 2>/dev/null; then
        write_result 36 actions-workflows-inscope actions_list in-scope pass "$RESP" "$RESP workflows"
      elif [ "$RESP" = "0" ]; then
        write_result 36 actions-workflows-inscope actions_list in-scope skip 0 "no workflows"
      else
        write_result 36 actions-workflows-inscope actions_list in-scope fail 0 "unexpected: $RESP"
      fi

      echo "--- Test 37: actions_list runs in-scope ---"
      RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/actions/runs?per_page=3" --jq '.total_count' 2>&1) || true
      if [ "$RESP" -gt 0 ] 2>/dev/null; then
        write_result 37 actions-runs-inscope actions_list in-scope pass "$RESP" "$RESP runs"
      elif [ "$RESP" = "0" ]; then
        write_result 37 actions-runs-inscope actions_list in-scope skip 0 "no runs"
      else
        write_result 37 actions-runs-inscope actions_list in-scope fail 0 "unexpected: $RESP"
      fi

      # ── User/Global (38-40) ────────────────────────────────────────

      echo "--- Test 38: get_me (should be blocked) ---"
      if RESP=$(gh api "$PROXY/user" --jq '.login' 2>/dev/null); then
        if [ -n "$RESP" ] && [ "$RESP" != "null" ]; then
          write_result 38 get-me-blocked get_me global fail 1 "returned login: $RESP"
        else
          write_result 38 get-me-blocked get_me global pass 0 "blocked"
        fi
      else
        write_result 38 get-me-blocked get_me global pass 0 "blocked (error)"
      fi

      echo "--- Test 39: search_issues in-scope ---"
      RESP=$(gh api "$PROXY/search/issues?q=repo:github/gh-aw-mcpg+is:open&per_page=3" --jq '.total_count' 2>&1) || true
      if [ "$RESP" -ge 0 ] 2>/dev/null; then
        write_result 39 search-issues-inscope search_issues in-scope pass "$RESP" "$RESP results"
      else
        write_result 39 search-issues-inscope search_issues in-scope fail 0 "unexpected: $RESP"
      fi

      echo "--- Test 40: search_repositories ---"
      RESP=$(gh api "$PROXY/search/repositories?q=gh-aw-mcpg&per_page=3" --jq '.total_count' 2>&1) || true
      if [ "$RESP" -ge 0 ] 2>/dev/null; then
        write_result 40 search-repos search_repositories global pass "$RESP" "$RESP results"
      else
        write_result 40 search-repos search_repositories global pass 0 "blocked or error"
      fi

      # ── GraphQL Expansions (41-44) ─────────────────────────────────

      echo "--- Test 41: GraphQL pull_request_read in-scope ---"
      QUERY='query { repository(owner:"github", name:"gh-aw-mcpg") { pullRequests(first:3, states:[OPEN,MERGED]) { nodes { number title } } } }'
      RESP=$(gh api "$PROXY/graphql" -f query="$QUERY" --jq '.data.repository.pullRequests.nodes | length' 2>&1) || true
      if [ "$RESP" -ge 0 ] 2>/dev/null; then
        write_result 41 graphql-prs-inscope pull_request_read in-scope pass "$RESP" "$RESP PRs via GraphQL"
      else
        write_result 41 graphql-prs-inscope pull_request_read in-scope fail 0 "unexpected: $RESP"
      fi

      echo "--- Test 42: GraphQL list_commits in-scope ---"
      QUERY='query { repository(owner:"github", name:"gh-aw-mcpg") { defaultBranchRef { target { ... on Commit { history(first:3) { nodes { oid message } } } } } } }'
      RESP=$(gh api "$PROXY/graphql" -f query="$QUERY" --jq '.data.repository.defaultBranchRef.target.history.nodes | length' 2>&1) || true
      if [ "$RESP" -gt 0 ] 2>/dev/null; then
        write_result 42 graphql-commits-inscope list_commits in-scope pass "$RESP" "$RESP commits via GraphQL"
      else
        write_result 42 graphql-commits-inscope list_commits in-scope fail 0 "unexpected: $RESP"
      fi

      echo "--- Test 43: GraphQL search in-scope ---"
      QUERY='query { search(query:"repo:github/gh-aw-mcpg is:issue", type:ISSUE, first:3) { issueCount nodes { ... on Issue { number title } } } }'
      RESP=$(gh api "$PROXY/graphql" -f query="$QUERY" --jq '.data.search.issueCount // 0' 2>&1) || true
      if [ "$RESP" -ge 0 ] 2>/dev/null; then
        write_result 43 graphql-search-inscope search_issues in-scope pass "$RESP" "$RESP issues via GraphQL"
      else
        write_result 43 graphql-search-inscope search_issues in-scope fail 0 "unexpected: $RESP"
      fi

      echo "--- Test 44: GraphQL viewer (should be blocked) ---"
      QUERY='query { viewer { login } }'
      RESP=$(gh api "$PROXY/graphql" -f query="$QUERY" --jq '.data.viewer.login // empty' 2>&1) || true
      if [ -z "$RESP" ] || [ "$RESP" = "null" ]; then
        write_result 44 graphql-viewer-blocked get_me global pass 0 "blocked"
      else
        write_result 44 graphql-viewer-blocked get_me global fail 1 "returned: $RESP"
      fi

      # ── Compare (45) ───────────────────────────────────────────────

      echo "--- Test 45: compare in-scope ---"
      if [ -n "$COMMIT_SHA" ]; then
        RESP=$(gh api "$PROXY/repos/github/gh-aw-mcpg/compare/main...$COMMIT_SHA" --jq '.status' 2>&1) || true
        if [ -n "$RESP" ] && [ "$RESP" != "null" ]; then
          write_result 45 compare-inscope pull_request_read in-scope pass 1 "status: $RESP"
        else
          write_result 45 compare-inscope pull_request_read in-scope fail 0 "unexpected response"
        fi
      else
        write_result 45 compare-inscope pull_request_read in-scope skip 0 "no commit SHA"
      fi

      # ── Out-of-scope single objects (46-47) ────────────────────────

      echo "--- Test 46: get_file_contents out-of-scope ---"
      if RESP=$(gh api "$PROXY/repos/octocat/Hello-World/contents/README" --jq '.name' 2>/dev/null); then
        if [ -n "$RESP" ] && [ "$RESP" != "null" ]; then
          write_result 46 file-contents-outscope get_file_contents out-of-scope fail 1 "returned: $RESP"
        else
          write_result 46 file-contents-outscope get_file_contents out-of-scope pass 0 "blocked"
        fi
      else
        write_result 46 file-contents-outscope get_file_contents out-of-scope pass 0 "blocked (error)"
      fi

      echo "--- Test 47: get_commit out-of-scope ---"
      OOS_SHA=$(gh api "$PROXY/repos/octocat/Hello-World/commits?per_page=1" --jq '.[0].sha' 2>&1) || true
      if [ "${#OOS_SHA}" -ge 7 ]; then
        if RESP=$(gh api "$PROXY/repos/octocat/Hello-World/commits/$OOS_SHA" --jq '.sha' 2>/dev/null); then
          if [ -n "$RESP" ] && [ "$RESP" != "null" ]; then
            write_result 47 get-commit-outscope get_commit out-of-scope fail 1 "returned SHA"
          else
            write_result 47 get-commit-outscope get_commit out-of-scope pass 0 "blocked"
          fi
        else
          write_result 47 get-commit-outscope get_commit out-of-scope pass 0 "blocked (error)"
        fi
      else
        write_result 47 get-commit-outscope get_commit out-of-scope pass 0 "list blocked (no SHA)"
      fi

      # ── Summary ────────────────────────────────────────────────────
      echo ""
      echo "=== Route Coverage Summary ==="
      echo "✅ Passed: $PASS"
      echo "❌ Failed: $FAIL"
      echo "⏭️  Skipped: $SKIP"
      echo "Total: $((PASS + FAIL + SKIP)) tests"
      echo ""
      if [ "$FAIL" -gt 0 ]; then
        echo "::warning::$FAIL test(s) failed in comprehensive route coverage"
      fi

  # ── Collect proxy logs ─────────────────────────────────────────────
  - name: Collect proxy logs and stop proxy
    if: always()
    run: |
      RESULTS_DIR="${RESULTS_DIR:-/tmp/gh-aw/github-script-results}"
      MCP_LOG_DIR=/tmp/gh-aw/mcp-logs

      # Save proxy container logs
      docker logs awmg-proxy 2>&1 > "$RESULTS_DIR/proxy-container.log" || true

      # Count DIFC events in JSONL
      if [ -f "$MCP_LOG_DIR/rpc-messages.jsonl" ]; then
        cp "$MCP_LOG_DIR/rpc-messages.jsonl" "$RESULTS_DIR/rpc-messages.jsonl"
        FILTERED=$(grep -c 'DIFC_FILTERED' "$MCP_LOG_DIR/rpc-messages.jsonl" 2>/dev/null || echo "0")
        TOTAL=$(wc -l < "$MCP_LOG_DIR/rpc-messages.jsonl" 2>/dev/null || echo "0")
        echo "{\"difc_filtered_count\": $FILTERED, \"total_rpc_messages\": $TOTAL}" > "$RESULTS_DIR/difc-summary.json"
        echo "DIFC events: $FILTERED filtered out of $TOTAL RPC messages"
      fi

      # Stop proxy
      docker rm -f awmg-proxy 2>/dev/null || true
      echo "Proxy stopped"

safe-outputs:
    threat-detection:
        enabled: false
    add-comment:
      hide-older-comments: true
      max: 2
    create-issue:
      expires: 2h
      group: true
      close-older-issues: true
    messages:
      footer: "> 🔬 *Proxy + github-script smoke test by [{workflow_name}]({run_url})*"
      run-started: "🔬 [{workflow_name}]({run_url}) is testing DIFC proxy with actions/github-script..."
      run-success: "🔬 [{workflow_name}]({run_url}) completed. See results issue. ✅"
      run-failure: "🔬 [{workflow_name}]({run_url}) reports {status}. ⚠️"
timeout-minutes: 15
---

# Smoke Test: DIFC Proxy and MCP Gateway

**Goal**: Validate that the MCP Gateway applies DIFC integrity filtering in
both **proxy mode** (intercepting `actions/github-script` and `gh` CLI calls)
and **gateway mode** (processing MCP tool calls). This covers all 25 unique
proxy tool name mappings across REST and GraphQL routes, plus the equivalent
MCP tool calls through the gateway.

## Part A: Proxy Mode Tests (Pre-Run)

Pre-agent steps ran 47 tests through `actions/github-script@v8` and `gh` CLI
with `GITHUB_API_URL` pointing to the DIFC proxy (port 18443):

### Tests 1-6: actions/github-script (octokit)

| # | Test | Tool | Expected |
|---|------|------|----------|
| 1 | In-scope REST: list issues (github/gh-aw-mcpg) | list_issues | Returns data |
| 2 | Out-of-scope REST: list issues (octocat/Hello-World) | list_issues | Empty or blocked |
| 3 | In-scope GraphQL: query issues (github/gh-aw-mcpg) | list_issues | Returns data |
| 4 | Out-of-scope GraphQL: query issues (octocat/Hello-World) | list_issues | Empty or blocked |
| 5 | In-scope REST: search code (github/gh-aw-mcpg) | search_code | Returns data |
| 6 | Integrity: bot-authored issues (github-actions[bot]) | list_issues | Visible (trusted bot) |

### Tests 7-12: gh CLI (REST + GraphQL)

| # | Test | Tool | Expected |
|---|------|------|----------|
| 7 | In-scope list issues | list_issues | Returns data |
| 8 | Out-of-scope list issues | list_issues | 0 items (blocked) |
| 9 | In-scope GraphQL issues | list_issues | Returns data |
| 10 | Out-of-scope GraphQL issues | list_issues | 0 items (blocked) |
| 11 | In-scope search code (/api/v3 prefix) | search_code | Returns data |
| 12 | In-scope get file contents (/api/v3 prefix) | get_file_contents | Returns README.md |

### Tests 13-16: Issues (issue_read)

| # | Test | Tool | Expected |
|---|------|------|----------|
| 13 | In-scope: single issue | issue_read | Returns issue object |
| 14 | Out-of-scope: single issue (octocat/Hello-World) | issue_read | Blocked |
| 15 | In-scope: issue comments | issue_read | Returns array (may be empty) |
| 16 | In-scope: issue labels | issue_read | Returns array (may be empty) |

### Tests 17-22: Pull Requests (list_pull_requests, pull_request_read)

| # | Test | Tool | Expected |
|---|------|------|----------|
| 17 | In-scope: list PRs | list_pull_requests | Returns data |
| 18 | Out-of-scope: list PRs (octocat/Hello-World) | list_pull_requests | 0 items (blocked) |
| 19 | In-scope: single PR | pull_request_read | Returns PR object |
| 20 | In-scope: PR files | pull_request_read | Returns array |
| 21 | In-scope: PR reviews | pull_request_read | Returns array (may be empty) |
| 22 | In-scope: PR comments | pull_request_read | Returns array (may be empty) |

### Tests 23-25: Commits (list_commits, get_commit)

| # | Test | Tool | Expected |
|---|------|------|----------|
| 23 | In-scope: list commits | list_commits | Returns data |
| 24 | Out-of-scope: list commits (octocat/Hello-World) | list_commits | 0 items (blocked) |
| 25 | In-scope: single commit | get_commit | Returns commit object |

### Tests 26-29: Branches & Tags (list_branches, list_tags)

| # | Test | Tool | Expected |
|---|------|------|----------|
| 26 | In-scope: list branches | list_branches | Returns data |
| 27 | Out-of-scope: list branches (octocat/Hello-World) | list_branches | 0 items (blocked) |
| 28 | In-scope: list tags | list_tags | Returns array (may be empty) |
| 29 | Out-of-scope: list tags (octocat/Hello-World) | list_tags | 0 items (blocked) |

### Tests 30-32: Releases (list_releases, get_latest_release)

| # | Test | Tool | Expected |
|---|------|------|----------|
| 30 | In-scope: list releases | list_releases | Returns array (may be empty) |
| 31 | Out-of-scope: list releases (octocat/Hello-World) | list_releases | 0 items (blocked) |
| 32 | In-scope: latest release | get_latest_release | Returns release or 404 (skip) |

### Tests 33-35: Labels (list_labels, get_label)

| # | Test | Tool | Expected |
|---|------|------|----------|
| 33 | In-scope: list labels | list_labels | 0 items (labels lack authorship → none integrity → filtered) |
| 34 | Out-of-scope: list labels (octocat/Hello-World) | list_labels | 0 items (blocked) |
| 35 | In-scope: get label (bug) | get_label | Returns label or skip |

### Tests 36-37: Actions (actions_list)

| # | Test | Tool | Expected |
|---|------|------|----------|
| 36 | In-scope: list workflows | actions_list | Returns data |
| 37 | In-scope: list runs | actions_list | Returns data |

### Tests 38-40: User/Global/Search

| # | Test | Tool | Expected |
|---|------|------|----------|
| 38 | get_me /user (blocked) | get_me | Blocked in repo-only mode |
| 39 | search_issues (in-scope query) | search_issues | Returns results |
| 40 | search_repositories | search_repositories | Returns results or blocked |

### Tests 41-44: GraphQL Expansions

| # | Test | Tool | Expected |
|---|------|------|----------|
| 41 | GraphQL: list pullRequests | pull_request_read | Returns data |
| 42 | GraphQL: commit history | list_commits | Returns data |
| 43 | GraphQL: search issues | search_issues | Returns data (or 0 if guard lacks repo scope) |
| 44 | GraphQL: viewer (blocked) | get_me | Blocked |

### Tests 45-47: Compare & Out-of-scope Singles

| # | Test | Tool | Expected |
|---|------|------|----------|
| 45 | In-scope: compare refs | pull_request_read | Returns compare object |
| 46 | Out-of-scope: file contents (octocat/Hello-World) | get_file_contents | Blocked |
| 47 | Out-of-scope: single commit (octocat/Hello-World) | get_commit | Blocked |

**Policy**: `{"allow-only":{"repos":["github/gh-aw-mcpg"],"min-integrity":"approved"}}`

## Your Task

1. **Read every test result file** in `/tmp/gh-aw/github-script-results/`:

   **Tests 1-12 (github-script + gh CLI basics):**
   - `test1-in-scope-issues.json` — REST list issues (in-scope)
   - `test2-out-of-scope-issues.json` — REST list issues (out-of-scope)
   - `test3-in-scope-graphql.json` — GraphQL issues (in-scope)
   - `test4-out-of-scope-graphql.json` — GraphQL issues (out-of-scope)
   - `test5-in-scope-search.json` — REST search code (in-scope)
   - `test6-bot-integrity.json` — bot-authored issue visibility
   - `test7-gh-in-scope-issues.json` — gh CLI list issues (in-scope)
   - `test8-gh-out-of-scope-issues.json` — gh CLI list issues (out-of-scope)
   - `test9-gh-in-scope-graphql.json` — gh CLI GraphQL (in-scope)
   - `test10-gh-out-of-scope-graphql.json` — gh CLI GraphQL (out-of-scope)
   - `test11-gh-in-scope-search.json` — gh CLI search (in-scope)
   - `test12-gh-in-scope-file.json` — gh CLI get file contents (in-scope)

   **Tests 13-16 (Issues):**
   - `test13-issue-read-inscope.json` — single issue read
   - `test14-issue-read-outscope.json` — out-of-scope single issue
   - `test15-issue-comments-inscope.json` — issue comments
   - `test16-issue-labels-inscope.json` — issue labels

   **Tests 17-22 (Pull Requests):**
   - `test17-list-prs-inscope.json` — list PRs
   - `test18-list-prs-outscope.json` — out-of-scope PRs
   - `test19-pr-read-inscope.json` — single PR
   - `test20-pr-files-inscope.json` — PR files
   - `test21-pr-reviews-inscope.json` — PR reviews
   - `test22-pr-comments-inscope.json` — PR comments

   **Tests 23-25 (Commits):**
   - `test23-list-commits-inscope.json` — list commits
   - `test24-list-commits-outscope.json` — out-of-scope commits
   - `test25-get-commit-inscope.json` — single commit

   **Tests 26-29 (Branches & Tags):**
   - `test26-list-branches-inscope.json` — list branches
   - `test27-list-branches-outscope.json` — out-of-scope branches
   - `test28-list-tags-inscope.json` — list tags
   - `test29-list-tags-outscope.json` — out-of-scope tags

   **Tests 30-32 (Releases):**
   - `test30-list-releases-inscope.json` — list releases
   - `test31-list-releases-outscope.json` — out-of-scope releases
   - `test32-latest-release-inscope.json` — latest release

   **Tests 33-35 (Labels):**
   - `test33-list-labels-inscope.json` — list labels
   - `test34-list-labels-outscope.json` — out-of-scope labels
   - `test35-get-label-inscope.json` — single label

   **Tests 36-37 (Actions):**
   - `test36-actions-workflows-inscope.json` — list workflows
   - `test37-actions-runs-inscope.json` — list runs

   **Tests 38-40 (User/Global/Search):**
   - `test38-get-me-blocked.json` — get_me blocked
   - `test39-search-issues-inscope.json` — search issues
   - `test40-search-repos.json` — search repositories

   **Tests 41-44 (GraphQL Expansions):**
   - `test41-graphql-prs-inscope.json` — GraphQL pullRequests
   - `test42-graphql-commits-inscope.json` — GraphQL commit history
   - `test43-graphql-search-inscope.json` — GraphQL search
   - `test44-graphql-viewer-blocked.json` — GraphQL viewer blocked

   **Tests 45-47 (Compare & Out-of-scope Singles):**
   - `test45-compare-inscope.json` — compare refs
   - `test46-file-contents-outscope.json` — out-of-scope file contents
   - `test47-get-commit-outscope.json` — out-of-scope single commit

   **Summary:**
   - `difc-summary.json` — DIFC filtering event counts

2. **Evaluate each test**:

   **In-scope collection tests** (1, 5, 7, 9, 11, 17, 23, 26, 33, 36, 37, 39):
   PASS if `item_count > 0` or `total_count > 0`

   **In-scope single-object tests** (12, 13, 19, 25, 35, 45):
   PASS if valid data returned (check result field)

   **In-scope sub-resource tests** (15, 16, 20, 21, 22):
   PASS if array returned (count >= 0 is valid, empty sub-resources are OK)

   **In-scope tolerant tests** (28, 30, 32):
   PASS or skip if empty (repo may lack tags/releases)

   **Out-of-scope collection tests** (2, 4, 8, 10, 18, 24, 27, 29, 31, 34):
   PASS if `item_count == 0` or blocked/error

   **Out-of-scope single-object tests** (14, 46, 47):
   PASS if error/blocked (no valid data returned)

   **Global blocked tests** (38, 44):
   PASS if blocked (repo-only mode rejects user/viewer queries)

   **Bot integrity** (6):
   PASS if `bot_issue_count > 0` (trusted bots pass integrity filter)

   **GraphQL in-scope** (3, 41, 42, 43):
   PASS if data returned

   **Search/global** (40):
   PASS if results returned or blocked (behavior may vary)

   For tests 13-47, also check the `result` field: `"pass"` = proxy worked correctly,
   `"skip"` = test skipped gracefully (no fixture data), `"fail"` = unexpected behavior.

3. **Check DIFC summary**: Note how many items were filtered and total RPC messages

## Part B: MCP Gateway Tool Call Tests (Agent-Executed)

You have access to GitHub MCP tools through the gateway. Execute the following
MCP tool calls to verify DIFC filtering works in gateway mode. The gateway is
configured with the same policy: `repos=["github/gh-aw-mcpg"], min-integrity=approved`.

### B1: In-scope repository tests (github/gh-aw-mcpg)

Execute each call and record: tool name, result count or data summary, pass/fail.

1. **list_issues** — owner=github, repo=gh-aw-mcpg, perPage=5
   PASS: returns issues with valid data

2. **get_file_contents** — owner=github, repo=gh-aw-mcpg, path=README.md
   PASS: returns file content

3. **list_pull_requests** — owner=github, repo=gh-aw-mcpg, perPage=5
   PASS: returns PR data

4. **list_commits** — owner=github, repo=gh-aw-mcpg, perPage=5
   PASS: returns commits

5. **search_code** — query="repo:github/gh-aw-mcpg filename:README", perPage=3
   PASS: returns search results

6. **list_branches** — owner=github, repo=gh-aw-mcpg, perPage=5
   PASS: returns branches

7. **search_issues** — query="repo:github/gh-aw-mcpg is:open", perPage=3
   PASS: returns search results

8. **list_tags** — owner=github, repo=gh-aw-mcpg, perPage=5
   PASS or empty (tags may not exist)

9. **list_releases** — owner=github, repo=gh-aw-mcpg, perPage=3
   PASS or empty (releases may not exist)

10. **get_me** — no arguments
    Expected: blocked or empty (repo-only mode rejects user queries)

### B2: Out-of-scope repository tests (octocat/Hello-World)

Execute each call and verify the gateway blocks access to out-of-scope repos:

11. **list_issues** — owner=octocat, repo=Hello-World, perPage=5
    PASS: 0 items returned or blocked

12. **get_file_contents** — owner=octocat, repo=Hello-World, path=README
    PASS: blocked or error (out-of-scope)

13. **list_pull_requests** — owner=octocat, repo=Hello-World, perPage=5
    PASS: 0 items returned or blocked

14. **list_commits** — owner=octocat, repo=Hello-World, perPage=5
    PASS: 0 items returned or blocked

15. **search_code** — query="repo:octocat/Hello-World README", perPage=3
    PASS: 0 results or blocked

### B3: Cross-validation with proxy results

After executing MCP calls, compare with proxy test results:
- Do in-scope calls return the same or similar data in both modes?
- Are out-of-scope calls consistently blocked in both modes?
- Note any discrepancies (they indicate bugs in either proxy or gateway mode)

### B4: Additional MCP tool calls (if issue/PR data discovered)

If in-scope tests returned issue or PR numbers, also test:

16. **issue_read** — read a specific issue from github/gh-aw-mcpg (use number from list_issues)
    PASS: returns issue details

17. **pull_request_read** — read a specific PR from github/gh-aw-mcpg (use number from list_pull_requests)
    PASS: returns PR details

18. **issue_read** — read an issue from octocat/Hello-World (use issue #1)
    PASS: blocked (out-of-scope)

19. **search_repositories** — query="github-guard", perPage=3
    Expected: blocked or empty (global search blocked in repo-only mode)

20. **search_users** — query="octocat", perPage=3
    Expected: blocked or empty (user search blocked in repo-only mode)

4. **Create an issue** with the results using this format:

```
## Proxy + github-script Smoke Test Results

**Policy**: repos=["github/gh-aw-mcpg"], min-integrity=approved
**Proxy**: awmg-local:latest (built from source), port 18443, HTTP, filter mode
**Run**: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}

### Tests 1-6: actions/github-script (octokit)
| # | Test | Items | Status |
|---|------|-------|--------|
| 1 | In-scope: list issues | [count] | ✅/❌ |
| 2 | Out-of-scope: list issues | [count] | ✅/❌ |
| 3 | In-scope: GraphQL issues | [count] | ✅/❌ |
| 4 | Out-of-scope: GraphQL issues | [count] | ✅/❌ |
| 5 | In-scope: search code | [count] | ✅/❌ |
| 6 | Bot-authored issues visible | [count] | ✅/❌ |

### Tests 7-12: gh CLI
| # | Test | Items | Status |
|---|------|-------|--------|
| 7 | In-scope list issues | [count] | ✅/❌ |
| 8 | Out-of-scope list issues | [count] | ✅/❌ |
| 9 | In-scope GraphQL | [count] | ✅/❌ |
| 10 | Out-of-scope GraphQL | [count] | ✅/❌ |
| 11 | In-scope search code | [count] | ✅/❌ |
| 12 | In-scope file contents | [name] | ✅/❌ |

### Tests 13-16: Issues (issue_read)
| # | Test | Result | Status |
|---|------|--------|--------|
| 13 | In-scope: single issue | [result] | ✅/❌/⏭️ |
| 14 | Out-of-scope: single issue | [result] | ✅/❌ |
| 15 | In-scope: issue comments | [count] | ✅/❌/⏭️ |
| 16 | In-scope: issue labels | [count] | ✅/❌/⏭️ |

### Tests 17-22: Pull Requests
| # | Test | Result | Status |
|---|------|--------|--------|
| 17 | In-scope: list PRs | [count] | ✅/❌ |
| 18 | Out-of-scope: list PRs | [count] | ✅/❌ |
| 19 | In-scope: single PR | [result] | ✅/❌/⏭️ |
| 20 | In-scope: PR files | [count] | ✅/❌/⏭️ |
| 21 | In-scope: PR reviews | [count] | ✅/❌/⏭️ |
| 22 | In-scope: PR comments | [count] | ✅/❌/⏭️ |

### Tests 23-25: Commits
| # | Test | Result | Status |
|---|------|--------|--------|
| 23 | In-scope: list commits | [count] | ✅/❌ |
| 24 | Out-of-scope: list commits | [count] | ✅/❌ |
| 25 | In-scope: single commit | [result] | ✅/❌/⏭️ |

### Tests 26-29: Branches & Tags
| # | Test | Result | Status |
|---|------|--------|--------|
| 26 | In-scope: list branches | [count] | ✅/❌ |
| 27 | Out-of-scope: list branches | [count] | ✅/❌ |
| 28 | In-scope: list tags | [count] | ✅/❌ |
| 29 | Out-of-scope: list tags | [count] | ✅/❌ |

### Tests 30-32: Releases
| # | Test | Result | Status |
|---|------|--------|--------|
| 30 | In-scope: list releases | [count] | ✅/❌ |
| 31 | Out-of-scope: list releases | [count] | ✅/❌ |
| 32 | In-scope: latest release | [result] | ✅/❌/⏭️ |

### Tests 33-35: Labels
| # | Test | Result | Status |
|---|------|--------|--------|
| 33 | In-scope: list labels | [count] | ✅/❌ |
| 34 | Out-of-scope: list labels | [count] | ✅/❌ |
| 35 | In-scope: get label | [result] | ✅/❌/⏭️ |

### Tests 36-37: Actions
| # | Test | Result | Status |
|---|------|--------|--------|
| 36 | In-scope: list workflows | [count] | ✅/❌ |
| 37 | In-scope: list runs | [count] | ✅/❌ |

### Tests 38-40: User/Global/Search
| # | Test | Result | Status |
|---|------|--------|--------|
| 38 | get_me /user (blocked) | [result] | ✅/❌ |
| 39 | search_issues (in-scope) | [count] | ✅/❌ |
| 40 | search_repositories | [count] | ✅/❌ |

### Tests 41-44: GraphQL Expansions
| # | Test | Result | Status |
|---|------|--------|--------|
| 41 | GraphQL: pullRequests | [count] | ✅/❌ |
| 42 | GraphQL: commit history | [count] | ✅/❌ |
| 43 | GraphQL: search issues | [count] | ✅/❌ |
| 44 | GraphQL: viewer (blocked) | [result] | ✅/❌ |

### Tests 45-47: Compare & Out-of-scope Singles
| # | Test | Result | Status |
|---|------|--------|--------|
| 45 | In-scope: compare refs | [result] | ✅/❌/⏭️ |
| 46 | Out-of-scope: file contents | [result] | ✅/❌ |
| 47 | Out-of-scope: single commit | [result] | ✅/❌ |

### DIFC Summary
- Filtered events: [N]
- Total RPC messages: [N]

### Route Coverage
- **Unique tool names tested**: [N] / 25
- **REST routes tested**: [N]
- **GraphQL patterns tested**: [N]

### MCP Gateway Tool Call Results

#### B1: In-scope (github/gh-aw-mcpg)
| # | Tool | Result | Status |
|---|------|--------|--------|
| B1 | list_issues | [count] | ✅/❌ |
| B2 | get_file_contents | [result] | ✅/❌ |
| B3 | list_pull_requests | [count] | ✅/❌ |
| B4 | list_commits | [count] | ✅/❌ |
| B5 | search_code | [count] | ✅/❌ |
| B6 | list_branches | [count] | ✅/❌ |
| B7 | search_issues | [count] | ✅/❌ |
| B8 | list_tags | [count] | ✅/❌/⏭️ |
| B9 | list_releases | [count] | ✅/❌/⏭️ |
| B10 | get_me | [result] | ✅/❌ |

#### B2: Out-of-scope (octocat/Hello-World)
| # | Tool | Result | Status |
|---|------|--------|--------|
| B11 | list_issues | [result] | ✅/❌ |
| B12 | get_file_contents | [result] | ✅/❌ |
| B13 | list_pull_requests | [result] | ✅/❌ |
| B14 | list_commits | [result] | ✅/❌ |
| B15 | search_code | [result] | ✅/❌ |

#### B4: Additional (if data discovered)
| # | Tool | Result | Status |
|---|------|--------|--------|
| B16 | issue_read (in-scope) | [result] | ✅/❌/⏭️ |
| B17 | pull_request_read (in-scope) | [result] | ✅/❌/⏭️ |
| B18 | issue_read (out-of-scope) | [result] | ✅/❌ |
| B19 | search_repositories | [result] | ✅/❌ |
| B20 | search_users | [result] | ✅/❌ |

### Cross-Validation: Proxy vs Gateway
[Note any discrepancies between proxy mode (tests 1-47) and MCP gateway mode (B1-B20)]

### Conclusion
- **Proxy in-scope access**: ✅/❌ (tests 1, 3, 5, 7, 9, 11-13, 15-17, 19-23, 25-26, 28, 30, 33, 35-37, 39, 41-43, 45)
- **Proxy out-of-scope blocked**: ✅/❌ (tests 2, 4, 8, 10, 14, 18, 24, 27, 29, 31, 34, 46, 47)
- **Proxy global blocked**: ✅/❌ (tests 38, 44)
- **Bot integrity preserved**: ✅/❌ (test 6)
- **github-script routing**: ✅/❌ (tests 1-6 via base-url)
- **gh CLI routing**: ✅/❌ (tests 7-47 via GH_HOST)
- **MCP gateway in-scope**: ✅/❌ (B1-B9)
- **MCP gateway out-of-scope**: ✅/❌ (B11-B15, B18)
- **MCP gateway global blocked**: ✅/❌ (B10, B19, B20)
- **Proxy/Gateway consistency**: ✅/❌ (cross-validation)
- **Overall**: ✅ PASS / ❌ FAIL

[Brief explanation of what this proves about DIFC filtering across proxy and gateway modes]
```

**Only if triggered by pull_request**: Also add a brief comment to the PR.