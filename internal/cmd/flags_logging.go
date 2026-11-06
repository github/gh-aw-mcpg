package cmd

// Logging-related flags

import (
	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/spf13/cobra"
)

var logFlagsLogging = logger.ForFile()

const wasmCacheDirEnvVar = "MCP_GATEWAY_WASM_CACHE_DIR"

// Logging flag variables
var (
	logDir               string
	payloadDir           string
	payloadPathPrefix    string
	payloadSizeThreshold int
	urlDomainAudit       bool
	wasmCacheDir         string
)

func init() {
	RegisterFlag(func(cmd *cobra.Command) {
		logFlagsLogging.Print("Registering logging flags")
		cmd.Flags().StringVar(&logDir, "log-dir", envutil.GetEnvString("MCP_GATEWAY_LOG_DIR", config.DefaultLogDir), "Directory for log files (falls back to stdout if directory cannot be created)")
		cmd.Flags().StringVar(&payloadDir, "payload-dir", envutil.GetEnvString("MCP_GATEWAY_PAYLOAD_DIR", config.DefaultPayloadDir), "Directory for storing large payload files (segmented by session ID)")
		cmd.Flags().StringVar(&wasmCacheDir, "wasm-cache-dir", resolveWasmCacheDir(false, "", envutil.GetEnvString("MCP_GATEWAY_LOG_DIR", config.DefaultLogDir)), "Directory for disk-backed wazero compilation cache (default: sibling of <log-dir>, named wazero-cache)")
		cmd.Flags().StringVar(&payloadPathPrefix, "payload-path-prefix", envutil.GetEnvString("MCP_GATEWAY_PAYLOAD_PATH_PREFIX", ""), "Path prefix to use when returning payloadPath to clients (allows remapping host paths to client/agent container paths)")
		cmd.Flags().IntVar(&payloadSizeThreshold, "payload-size-threshold", envutil.GetEnvInt("MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD", config.DefaultPayloadSizeThreshold), "Size threshold (in bytes) for storing payloads to disk. Payloads larger than this are stored, smaller ones returned inline")
		cmd.Flags().BoolVar(&urlDomainAudit, "url-domain-audit", envutil.GetEnvBool("MCP_GATEWAY_URL_DOMAIN_AUDIT", false), "Observe and persist URL domains seen in tool responses and safe-output writes")
		logFlagsLogging.Printf("Logging flag defaults: log-dir=%s, payload-dir=%s, wasm-cache-dir=%s, payload-size-threshold=%d, url-domain-audit=%t", logDir, payloadDir, wasmCacheDir, payloadSizeThreshold, urlDomainAudit)
	})
}
