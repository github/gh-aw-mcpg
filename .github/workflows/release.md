---
name: Release
description: Build, test, and release MCP Gateway binary and Docker image, then generate and prepend release highlights
on:
  roles:
    - admin
    - maintainer
  push:
    tags:
      - 'v*.*.*'
  workflow_dispatch:
    inputs:
      release_type:
        description: 'Type of release (patch, minor, or major)'
        required: true
        type: choice
        options:
          - patch
          - minor
          - major
permissions:
  contents: read
  pull-requests: read
  actions: read
  issues: read
engine: copilot
timeout-minutes: 30
network:
  allowed:
    - defaults
    - node
    - containers
sandbox:
  agent: awf  # Firewall enabled (migrated from network.firewall)
tools:
  bash:
    - "*"
  edit:
safe-outputs:
  threat-detection:
    enabled: false
  update-release:
jobs:
  # Pre-tag validation: Run tests BEFORE creating the tag (workflow_dispatch only)
  # This prevents tag creation if build or tests fail
  # Note: release job also runs tests for direct tag pushes and as defense-in-depth
  test:
    if: github.event_name == 'workflow_dispatch'
    needs: ["activation"]
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v5
        with:
          fetch-depth: 0
          
      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: false

      - name: Download Go modules
        run: go mod download

      - name: Run unit tests
        run: |
          echo "Running unit tests (excluding integration tests)..."
          make test-unit
          echo "✓ Unit tests passed"

      - name: Build binary
        run: |
          echo "Building binary for integration tests..."
          make build
          echo "✓ Binary built successfully"

      - name: Run integration tests
        run: |
          echo "Running integration tests with built binary..."
          make test-integration
          echo "✓ Integration tests passed"

  create-tag:
    if: github.event_name == 'workflow_dispatch'
    needs: ["activation", "test"]
    runs-on: ubuntu-latest
    permissions:
      contents: write
    outputs:
      new_tag: ${{ steps.create_tag.outputs.new_tag }}
    steps:
      - name: Checkout
        uses: actions/checkout@v5
        with:
          fetch-depth: 0
          persist-credentials: true
          
      - name: Create and push tag
        id: create_tag
        env:
          RELEASE_TYPE: ${{ inputs.release_type }}
        run: |
          # Get the latest tag
          LATEST_TAG=$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' | sort -V | tail -1)
          
          if [ -z "$LATEST_TAG" ]; then
            echo "No existing tags found, starting from v0.0.0"
            LATEST_TAG="v0.0.0"
          else
            echo "Latest tag: $LATEST_TAG"
          fi
          
          # Parse version components
          VERSION_NUM=$(echo $LATEST_TAG | sed 's/^v//')
          MAJOR=$(echo $VERSION_NUM | cut -d. -f1)
          MINOR=$(echo $VERSION_NUM | cut -d. -f2)
          PATCH=$(echo $VERSION_NUM | cut -d. -f3)
          
          # Increment based on release type
          if [ "$RELEASE_TYPE" = "major" ]; then
            MAJOR=$((MAJOR + 1))
            MINOR=0
            PATCH=0
          elif [ "$RELEASE_TYPE" = "minor" ]; then
            MINOR=$((MINOR + 1))
            PATCH=0
          elif [ "$RELEASE_TYPE" = "patch" ]; then
            PATCH=$((PATCH + 1))
          fi
          
          NEW_TAG="v$MAJOR.$MINOR.$PATCH"
          echo "Creating new tag: $NEW_TAG"
          
          # Create and push the tag
          git config user.name "github-actions[bot]"
          git config user.email "github-actions[bot]@users.noreply.github.com"
          git tag -a "$NEW_TAG" -m "Release $NEW_TAG"
          git push origin "$NEW_TAG"
          
          echo "new_tag=$NEW_TAG" >> "$GITHUB_OUTPUT"
          echo "✓ Tag $NEW_TAG created and pushed"

  release:
    needs: ["activation", "create-tag"]
    if: always() && needs.activation.result == 'success' && (needs.create-tag.result == 'success' || needs.create-tag.result == 'skipped')
    runs-on: ubuntu-latest
    permissions:
      contents: write
      packages: write
      id-token: write
      attestations: write
    outputs:
      release_id: ${{ steps.get_release.outputs.release_id }}
      release_tag: ${{ steps.get_release.outputs.release_tag }}
    steps:
      - name: Checkout
        uses: actions/checkout@v5
        with:
          fetch-depth: 0
          persist-credentials: false
          ref: ${{ needs.create-tag.outputs.new_tag || github.ref }}
          
      - name: Set release tag
        id: set_tag
        run: |
          if [ "${{ github.event_name }}" = "workflow_dispatch" ]; then
            RELEASE_TAG="${{ needs.create-tag.outputs.new_tag }}"
          else
            RELEASE_TAG="${GITHUB_REF#refs/tags/}"
          fi
          
          # Sanity check: ensure release tag is set
          if [ -z "$RELEASE_TAG" ]; then
            echo "Error: RELEASE_TAG is not set"
            exit 1
          fi
          
          # Sanity check: validate format is v<major>.<minor>.<patch>
          if ! echo "$RELEASE_TAG" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
            echo "Error: RELEASE_TAG '$RELEASE_TAG' does not match required format v<major>.<minor>.<patch>"
            echo "Example valid format: v1.2.3"
            exit 1
          fi
          
          echo "RELEASE_TAG=$RELEASE_TAG" >> "$GITHUB_ENV"
          echo "release_tag=$RELEASE_TAG" >> "$GITHUB_OUTPUT"
          echo "✓ Using release tag: $RELEASE_TAG"
          
      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: false  # Disabled for release security - prevent cache poisoning attacks

      - name: Download Go modules
        run: go mod download

      - name: Run unit tests
        run: |
          echo "Running unit tests (excluding integration tests)..."
          make test-unit
          echo "✓ Unit tests passed"

      - name: Build binary
        run: |
          echo "Building binary for integration tests..."
          echo "Release tag: $RELEASE_TAG"
          make build
          echo "✓ Binary built successfully"

      - name: Run integration tests
        run: |
          echo "Running integration tests with built binary..."
          make test-integration
          echo "✓ Integration tests passed"

      - name: Build release binaries
        run: |
          echo "Building multi-platform binaries for: $RELEASE_TAG"
          chmod +x scripts/build-release.sh
          ./scripts/build-release.sh "$RELEASE_TAG"

      - name: Upload binaries to release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          echo "Creating release for tag: $RELEASE_TAG"
          
          # Create release with all binaries and checksums
          gh release create "$RELEASE_TAG" \
            --title "$RELEASE_TAG" \
            --generate-notes \
            dist/*
          
          echo "✓ Release created with all platform binaries and checksums"

      - name: Get release ID
        id: get_release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          echo "Getting release ID for tag: $RELEASE_TAG"
          RELEASE_ID=$(gh release view "$RELEASE_TAG" --json databaseId --jq '.databaseId')
          echo "release_id=$RELEASE_ID" >> "$GITHUB_OUTPUT"
          echo "release_tag=$RELEASE_TAG" >> "$GITHUB_OUTPUT"
          echo "✓ Release ID: $RELEASE_ID"
          echo "✓ Release Tag: $RELEASE_TAG"

  docker:
    needs: ["release"]
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      # Enables emulation so the amd64 runner can build arm64 too
      - name: Set up QEMU
        uses: docker/setup-qemu-action@v3

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Extract tag version
        id: tag_version
        run: |
          RELEASE_TAG="${{ needs.release.outputs.release_tag }}"
          echo "version=$RELEASE_TAG" >> "$GITHUB_OUTPUT"
          echo "✓ Version: $RELEASE_TAG"

      - name: Set up Rust
        uses: actions-rust-lang/setup-rust-toolchain@a0b538fa0b742a6aa35d6e2c169b4bd06d225a98 # v1.15.3
        with:
          target: wasm32-wasip1

      - name: Build baked WASM guard
        run: |
          make -C guards/github-guard build
          test -f guards/github-guard/github-guard-rust.wasm

      - name: Build and push (multi-arch)
        uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          platforms: linux/amd64,linux/arm64
          build-args: |
            VERSION=${{ steps.tag_version.outputs.version }}
          tags: |
            ghcr.io/${{ github.repository }}:latest
            ghcr.io/${{ github.repository }}:${{ steps.tag_version.outputs.version }}
            ghcr.io/${{ github.repository }}:${{ github.sha }}
          cache-from: type=gha
          cache-to: type=gha,mode=max

  generate-sbom:
    needs: ["release"]
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - name: Checkout repository
        uses: actions/checkout@v5

      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: false  # Disabled for release security - prevent cache poisoning attacks

      - name: Download Go modules
        run: go mod download

      - name: Generate SBOM (SPDX format)
        uses: anchore/sbom-action@v0.20.10
        with:
          artifact-name: sbom.spdx.json
          output-file: sbom.spdx.json
          format: spdx-json

      - name: Generate SBOM (CycloneDX format)
        uses: anchore/sbom-action@v0.20.10
        with:
          artifact-name: sbom.cdx.json
          output-file: sbom.cdx.json
          format: cyclonedx-json

      - name: Audit SBOM files for secrets
        run: |
          echo "Auditing SBOM files for potential secrets..."
          if grep -rE "GITHUB_TOKEN|SECRET|PASSWORD|API_KEY|PRIVATE_KEY" sbom.*.json; then
            echo "Error: Potential secrets found in SBOM files"
            exit 1
          fi
          echo "✓ No secrets detected in SBOM files"

      - name: Upload SBOM artifacts
        uses: actions/upload-artifact@v5
        with:
          name: sbom-artifacts
          path: |
            sbom.spdx.json
            sbom.cdx.json
          retention-days: 7  # Minimize exposure window

      - name: Attach SBOM to release
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          RELEASE_TAG: ${{ needs.release.outputs.release_tag }}
        run: |
          echo "Attaching SBOM files to release: $RELEASE_TAG"
          gh release upload "$RELEASE_TAG" sbom.spdx.json sbom.cdx.json --clobber
          echo "✓ SBOM files attached to release"
steps:
  - name: Setup environment and fetch release data
    env:
      RELEASE_ID: ${{ needs.release.outputs.release_id }}
      RELEASE_TAG: ${{ needs.release.outputs.release_tag }}
      GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
    run: |
      set -e
      mkdir -p /tmp/gh-aw-mcpg/release-data
      
      # Use the release ID and tag from the release job
      echo "Release ID from release job: $RELEASE_ID"
      echo "Release tag from release job: $RELEASE_TAG"
      
      echo "Processing release: $RELEASE_TAG"
      
      echo "RELEASE_TAG=$RELEASE_TAG" >> "$GITHUB_ENV"
      
      # Get the current release information
      gh release view "$RELEASE_TAG" --json name,tagName,createdAt,publishedAt,url,body > /tmp/gh-aw-mcpg/release-data/current_release.json
      echo "✓ Fetched current release information"
      
      # Get the previous release to determine the range
      PREV_RELEASE_TAG=$(gh release list --limit 2 --json tagName --jq '.[1].tagName // empty')
      
      if [ -z "$PREV_RELEASE_TAG" ]; then
        echo "No previous release found. This appears to be the first release."
        echo "PREV_RELEASE_TAG=" >> "$GITHUB_ENV"
        touch /tmp/gh-aw-mcpg/release-data/pull_requests.json
        echo "[]" > /tmp/gh-aw-mcpg/release-data/pull_requests.json
      else
        echo "Previous release: $PREV_RELEASE_TAG"
        echo "PREV_RELEASE_TAG=$PREV_RELEASE_TAG" >> "$GITHUB_ENV"
        
        # Get commits between releases
        echo "Fetching commits between $PREV_RELEASE_TAG and $RELEASE_TAG..."
        git fetch --unshallow 2>/dev/null || git fetch --depth=1000
        
        # Get all merged PRs between the two releases
        echo "Fetching pull requests merged between releases..."
        PREV_PUBLISHED_AT=$(gh release view "$PREV_RELEASE_TAG" --json publishedAt --jq .publishedAt)
        CURR_PUBLISHED_AT=$(gh release view "$RELEASE_TAG" --json publishedAt --jq .publishedAt)
        gh pr list \
          --state merged \
          --limit 1000 \
          --json number,title,author,labels,mergedAt,url,body \
          --jq "[.[] | select(.mergedAt >= \"$PREV_PUBLISHED_AT\" and .mergedAt <= \"$CURR_PUBLISHED_AT\")]" \
          > /tmp/gh-aw-mcpg/release-data/pull_requests.json
        
        PR_COUNT=$(jq length "/tmp/gh-aw-mcpg/release-data/pull_requests.json")
        echo "✓ Fetched $PR_COUNT pull requests"
      fi
      
      # Get the README.md content for context about the project
      if [ -f "README.md" ]; then
        cp README.md /tmp/gh-aw-mcpg/release-data/README.md
        echo "✓ Copied README.md for reference"
      fi
      
      # List documentation files for linking
      find docs -type f -name "*.md" 2>/dev/null > /tmp/gh-aw-mcpg/release-data/docs_files.txt || echo "No docs directory found"
      
      echo "✓ Setup complete. Data available in /tmp/gh-aw-mcpg/release-data/"
---

# Release Highlights Generator

Generate an engaging release highlights summary for **${{ github.repository }}** (MCP Gateway) release `${RELEASE_TAG}`.

**Release ID**: ${{ needs.release.outputs.release_id }}

## Data Available

All data is pre-fetched in `/tmp/gh-aw-mcpg/release-data/`:
- `current_release.json` - Release metadata (tag, name, dates, existing body)
- `pull_requests.json` - PRs merged between `${PREV_RELEASE_TAG}` and `${RELEASE_TAG}` (empty array if first release)
- `README.md` - Project overview for context (if exists)
- `docs_files.txt` - Available documentation files for linking

## Project Context

**MCP Gateway** is a Go-based proxy server for Model Context Protocol (MCP) servers. Key features:
- Routes requests to multiple MCP backend servers (routed and unified modes)
- Launches MCP servers as Docker containers
- Handles JSON-RPC 2.0 over stdio communication
- Provides security through guards and DIFC labeling

## Output Requirements

Create a **"🌟 Release Highlights"** section that:
- Is concise and scannable (users grasp key changes in 30 seconds)
- Uses professional, enthusiastic tone (not overly casual)
- Categorizes changes logically (features, fixes, docs, breaking changes)
- Links to relevant documentation where helpful (GitHub repo docs/ directory)
- Focuses on user impact (why changes matter, not just what changed)
- Mentions Docker image availability with version tag

## Workflow

### 1. Load Data

```bash
# View release metadata
cat /tmp/gh-aw-mcpg/release-data/current_release.json | jq

# List PRs (empty if first release)
cat /tmp/gh-aw-mcpg/release-data/pull_requests.json | jq -r '.[] | "- #\(.number): \(.title) by @\(.author.login)"'

# Check README context
head -100 /tmp/gh-aw-mcpg/release-data/README.md 2>/dev/null || echo "No README"

# View available docs
cat /tmp/gh-aw-mcpg/release-data/docs_files.txt
```

### 2. Categorize & Prioritize

Group PRs by category (omit categories with no items):
- **✨ New Features** - User-facing capabilities (routing, server management, etc.)
- **🐛 Bug Fixes** - Issue resolutions
- **⚡ Performance** - Speed/efficiency improvements
- **📚 Documentation** - Guide/reference updates
- **⚠️ Breaking Changes** - Requires user action (ALWAYS list first if present)
- **🔧 Internal** - Refactoring, dependencies (usually omit from highlights)

### 3. Write Highlights

Structure:
```markdown
## 🌟 Release Highlights

[1-2 sentence summary of the release theme/focus]

### ⚠️ Breaking Changes
[If any - list FIRST with migration guidance]

### ✨ What's New
[Top 3-5 features with user benefit, link docs when relevant]

### 🐛 Bug Fixes & Improvements
[Notable fixes - focus on user impact]

### 📚 Documentation
[Only if significant doc additions/improvements]

### 🐳 Docker Image

The Docker image for this release is available at:

\`\`\`bash
docker pull ghcr.io/github/gh-aw-mcpg:${RELEASE_TAG}
# or
docker pull ghcr.io/github/gh-aw-mcpg:latest
\`\`\`

Supported platforms: `linux/amd64`, `linux/arm64`

---
For complete details, see the [full release notes](${{ github.server_url }}/${{ github.repository }}/releases/tag/${RELEASE_TAG}).
```

**Writing Guidelines:**
- Lead with benefits: "MCP Gateway now supports remote mode" not "Added remote mode"
- Be specific: "Reduced server startup time by 40%" not "Faster startup"
- Skip internal changes unless they have user impact
- Use docs links: `[Configuration Guide](https://github.com/github/gh-aw-mcpg/blob/main/docs/config.md)`
- Keep breaking changes prominent with action items
- Mention Docker image availability prominently

### 4. Handle Special Cases

**First Release** (no `${PREV_RELEASE_TAG}`):
```markdown
## 🎉 First Release

Welcome to the inaugural release of MCP Gateway! This Go-based proxy server enables seamless integration with multiple Model Context Protocol (MCP) servers.

### Key Features
- **Multi-server routing**: Route requests to different MCP backends via `/mcp/{serverID}`
- **Unified endpoint**: Single `/mcp` endpoint with intelligent routing
- **Docker integration**: Launch and manage MCP servers as containers
- **JSON-RPC 2.0**: Full support for MCP protocol over stdio
- **Security**: Built-in guards and DIFC labeling support

### 🐳 Docker Image

The Docker image for this release is available at:

\`\`\`bash
docker pull ghcr.io/github/gh-aw-mcpg:${RELEASE_TAG}
# or
docker pull ghcr.io/github/gh-aw-mcpg:latest
\`\`\`

Supported platforms: `linux/amd64`, `linux/arm64`

### Getting Started
1. Build: `make build`
2. Configure: Edit `config.toml` with your MCP servers
3. Run: `./awmg --config config.toml`

See the [README](https://github.com/github/gh-aw-mcpg#readme) for complete setup instructions.
```

**Maintenance Release** (no user-facing changes):
```markdown
## 🔧 Maintenance Release

Dependency updates and internal improvements to keep MCP Gateway running smoothly.

### 🐳 Docker Image

The Docker image for this release is available at:

\`\`\`bash
docker pull ghcr.io/github/gh-aw-mcpg:${RELEASE_TAG}
# or
docker pull ghcr.io/github/gh-aw-mcpg:latest
\`\`\`

Supported platforms: `linux/amd64`, `linux/arm64`
```

## Output Format

**CRITICAL**: You MUST call the `update_release` tool to update the release with the generated highlights:

```javascript
update_release({
  tag: "${RELEASE_TAG}",
  operation: "prepend",
  body: "## 🌟 Release Highlights\n\n[Your complete markdown highlights here]"
})
```

**Required Parameters:**
- `tag` - Release tag from `${RELEASE_TAG}` environment variable (e.g., "v0.1.0")
- `operation` - Must be `"prepend"` to add before existing notes
- `body` - Complete markdown content (include all formatting, emojis, links)

**WARNING**: If you don't call the `update_release` tool, the release notes will NOT be updated!

**Documentation Base URL:**
- Repository docs: `https://github.com/github/gh-aw-mcpg/blob/main/docs/`
- Repository README: `https://github.com/github/gh-aw-mcpg#readme`

Verify paths exist in `docs_files.txt` before linking.
