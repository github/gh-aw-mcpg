package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WASMGuardsDirEnvVar is the environment variable name for the root directory
// containing per-server WASM guards (<root>/<serverID>/*.wasm).
const WASMGuardsDirEnvVar = "MCP_GATEWAY_WASM_GUARDS_DIR"

// ContainerGuardWasmPath is the baked-in guard path in the container image.
const ContainerGuardWasmPath = "/guards/github/00-github-guard.wasm"

// GetWASMGuardsRootDir returns the trimmed value of MCP_GATEWAY_WASM_GUARDS_DIR.
func GetWASMGuardsRootDir() string {
	return strings.TrimSpace(os.Getenv(WASMGuardsDirEnvVar))
}

// FindServerWASMGuardFile discovers the first .wasm file for a server under
// $MCP_GATEWAY_WASM_GUARDS_DIR/<serverID>.
func FindServerWASMGuardFile(serverID string) (string, bool, error) {
	guardsRootDir := GetWASMGuardsRootDir()
	if guardsRootDir == "" {
		logWasm.Printf("Skipping WASM guard discovery: %s is not set", WASMGuardsDirEnvVar)
		return "", false, nil
	}

	serverGuardDir := filepath.Join(guardsRootDir, serverID)
	logWasm.Printf("Searching for WASM guard file: serverID=%s, dir=%s", serverID, serverGuardDir)
	entries, err := os.ReadDir(serverGuardDir)
	if err != nil {
		if os.IsNotExist(err) {
			logWasm.Printf("No WASM guard directory found for serverID=%s", serverID)
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read server guard directory %q: %w", serverGuardDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".wasm") {
			wasmPath := filepath.Join(serverGuardDir, entry.Name())
			logWasm.Printf("Found WASM guard file: serverID=%s, path=%s", serverID, wasmPath)
			return wasmPath, true, nil
		}
	}

	logWasm.Printf("No WASM guard file found in directory: serverID=%s, dir=%s", serverID, serverGuardDir)
	return "", false, nil
}

// FindGuardFile discovers the WASM guard file for a server. It checks in order:
//  1. The baked-in container guard at ContainerGuardWasmPath (only when serverID == "github").
//  2. The first .wasm file under $MCP_GATEWAY_WASM_GUARDS_DIR/<serverID>.
//
// Returns an empty string and found=false when no guard can be discovered.
func FindGuardFile(serverID string) (string, bool, error) {
	return findGuardFile(serverID, os.Stat)
}

func findGuardFile(serverID string, statFn func(string) (os.FileInfo, error)) (string, bool, error) {
	if serverID == "github" {
		// Preserve explicit opt-out behavior used by container runners:
		// when MCP_GATEWAY_WASM_GUARDS_DIR is set but blank, skip baked-in discovery.
		if guardDir, ok := os.LookupEnv(WASMGuardsDirEnvVar); ok && guardDir == "" {
			logWasm.Printf("Skipping baked-in guard discovery: %s is explicitly set but blank", WASMGuardsDirEnvVar)
			return "", false, nil
		}

		logWasm.Printf("Checking for baked-in container guard at %s", ContainerGuardWasmPath)
		if _, err := statFn(ContainerGuardWasmPath); err == nil {
			logWasm.Printf("Found baked-in container guard: %s", ContainerGuardWasmPath)
			return ContainerGuardWasmPath, true, nil
		}
	}

	return FindServerWASMGuardFile(serverID)
}
