# Serena MCP Server Cache Optimization

## Problem

The MCP Gateway waits for all backend servers to complete startup before responding to HTTP requests. The Serena MCP server can take 10-30 seconds to launch because it needs to:

1. Initialize language servers (Python, Java, JavaScript/TypeScript, Go)
2. Index the workspace for code intelligence
3. Build symbol tables and dependency graphs

This slow startup significantly impacts the gateway's overall startup time, as it's bound by the slowest backend server.

## Solution: Persistent Cache

By mounting a persistent cache directory, Serena can reuse its workspace index and language server data across container restarts, reducing startup time from 10-30 seconds to <1 second.

### How It Works

Serena stores cached data in `/tmp/serena-cache` inside the container:
- Language server indexes
- Workspace symbol tables
- Dependency graphs
- Type information

By default, this directory is ephemeral and gets recreated each time the container starts. By mounting a persistent volume from the host, the cache survives between container restarts.

### Implementation

**TOML Configuration (`config.toml`):**
```toml
[servers.serena]
command = "docker"
args = [
  "run", "--rm", "-i",
  "-v", "${PWD}:/workspace:ro",
  "-v", "${HOME}/.serena-cache:/tmp/serena-cache",  # Persistent cache
  "-e", "NO_COLOR=1",
  "-e", "TERM=dumb",
  "ghcr.io/githubnext/serena-mcp-server:latest"
]
```

**JSON Configuration (`config.json`):**
```json
{
  "mcpServers": {
    "serena": {
      "type": "stdio",
      "container": "ghcr.io/githubnext/serena-mcp-server:latest",
      "mounts": [
        "${PWD}:/workspace:ro",
        "${HOME}/.serena-cache:/tmp/serena-cache"
      ],
      "env": {
        "NO_COLOR": "1",
        "TERM": "dumb"
      }
    }
  }
}
```

### Cache Location

The cache is mounted to `${HOME}/.serena-cache` by default, which expands to:
- **Linux/macOS**: `~/.serena-cache`
- **Windows**: `%USERPROFILE%\.serena-cache`

You can customize the location by changing the mount path:
```toml
"-v", "/custom/path/to/cache:/tmp/serena-cache"
```

### Cache Behavior

**First Launch (Cold Cache):**
- Serena initializes all language servers
- Indexes the workspace
- Writes cache to `~/.serena-cache`
- Startup time: 10-30 seconds (depending on project size)

**Subsequent Launches (Warm Cache):**
- Serena reads cached indexes
- Skips workspace indexing
- Language servers use cached data
- Startup time: <1 second

**Cache Updates:**
- Serena manages cache updates as workspace files change
- Language servers maintain their own index lifecycle
- Consider clearing cache if encountering stale data issues

### Benefits

1. **Faster Gateway Startup**: Reduces gateway startup time by 10-30 seconds
2. **Better Developer Experience**: Near-instant MCP server availability
3. **Improved CI/CD**: Faster test/build cycles when using Serena
4. **Resource Efficiency**: Less CPU/memory usage on startup
5. **Unified Cache Location**: Single cache directory efficiently stores isolated indexes for all workspaces

### Cache Isolation

The cache is workspace-aware, with each workspace maintaining its own index:
- Cache data is keyed by workspace path
- Multiple workspaces can share the same cache directory
- No conflicts between different projects
- A single cache directory stores isolated data for all Serena instances

### Disk Usage

The cache directory size depends on your codebase:
- **Small projects** (1-100 files): ~1-10 MB
- **Medium projects** (100-1000 files): ~10-50 MB
- **Large projects** (1000+ files): ~50-200 MB

Serena and its language servers manage the cache lifecycle. Monitor disk usage and clear the cache manually if it grows too large.

### Troubleshooting

**Cache not working (still slow startup):**
1. Verify the mount path exists and is writable
2. Check Docker has permission to mount the directory
3. Ensure `${HOME}` environment variable is set correctly
4. Try using an absolute path instead of `${HOME}`

**Cache growing too large:**
1. Delete the cache directory: `rm -rf ~/.serena-cache`
2. The cache will be rebuilt on next launch
3. Consider using a workspace-specific cache location

**Cache corruption:**
If you encounter errors related to cached data:
```bash
# Clear the cache
rm -rf ~/.serena-cache

# Restart the gateway
# Cache will be rebuilt automatically
```

### Alternative Cache Locations

**Per-repository cache:**
```toml
"-v", "${PWD}/.serena-cache:/tmp/serena-cache"
```

**Project-specific cache:**
```toml
"-v", "/path/to/project/.cache/serena:/tmp/serena-cache"
```

**Shared team cache (networked filesystem):**
```toml
"-v", "/shared/team/serena-cache:/tmp/serena-cache"
```

### Performance Comparison

| Scenario | Without Cache | With Cache |
|----------|--------------|------------|
| First launch | 10-30 seconds | 10-30 seconds |
| Second launch | 10-30 seconds | <1 second |
| File change | 10-30 seconds | 1-2 seconds |
| Language server query | Fast | Fast |

### References

- [Serena MCP Server Documentation](../containers/serena-mcp-server/README.md)
- [MCP Gateway Configuration](../README.md)
- [Docker Volume Mounts](https://docs.docker.com/storage/volumes/)
