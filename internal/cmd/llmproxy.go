package cmd

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/llmproxy"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/spf13/cobra"
)

var logLLMProxyCmd = logger.New("cmd:llmproxy")

// llm-proxy subcommand flag variables
var (
	llmListen      string
	llmUpstream    string
	llmAutoCache   bool
	llmTailTTL     string
	llmDropTools   []string
	llmStripANSI   bool
	llmTrimBashGit bool
	llmLogDir      string
)

func init() {
	rootCmd.AddCommand(newLLMProxyCmd())
}

func newLLMProxyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "llm-proxy",
		Short: "Run as a cost-optimising reverse proxy for the Anthropic LLM API",
		Long: `Run the gateway in llm-proxy mode — a local HTTP reverse proxy that
intercepts /v1/messages requests and applies a pipeline of cost-saving
optimisations before forwarding to api.anthropic.com (or a custom upstream).

Each optimisation is independent and can be toggled individually:

  --auto-cache   Inject prompt-cache breakpoints on tools (~24k tokens),
                 system (~8k tokens), and messages[0] (~5k tokens), and
                 upgrade all ephemeral TTLs from the silent 5m default to
                 1h.  This is the single biggest cost-saving optimisation
                 (up to −90% input tokens on Claude Code workloads).

  --strip-ansi   Strip ANSI escape codes from tool results and message
                 content so that terminal-coloured output caches cleanly.
                 Enabled by default.

  --drop-tools   Remove named tools from the tools array and scrub their
                 names from system-reminder blocks, shrinking every request
                 by ~100–800 tokens per dropped tool.

  --trim-bash-git
                 Truncate the Bash tool description at "# Committing
                 changes with git", dropping ~1 800 tokens per request.

Usage example (Claude Code):

  # Start the proxy
  awmg llm-proxy --auto-cache --strip-ansi \
    --drop-tools NotebookEdit,CronCreate,CronDelete

  # Point Claude Code at it
  export ANTHROPIC_BASE_URL=http://127.0.0.1:8787`,
		Example: `  # Cache-only (biggest win, zero side effects)
  awmg llm-proxy --auto-cache

  # Cache + drop rarely-used tools + strip terminal colours
  awmg llm-proxy --auto-cache --strip-ansi \
    --drop-tools NotebookEdit,CronCreate,CronDelete,CronList,Monitor,RemoteTrigger,PushNotification

  # All optimisations enabled
  awmg llm-proxy --auto-cache --strip-ansi --trim-bash-git \
    --drop-tools NotebookEdit,CronCreate,CronDelete

  # Custom upstream (e.g. a local mock)
  awmg llm-proxy --upstream http://localhost:9090`,
		SilenceUsage: true,
		RunE:         runLLMProxy,
	}

	cmd.Flags().StringVarP(&llmListen, "listen", "l", "127.0.0.1:8787", "Address to listen on")
	cmd.Flags().StringVar(&llmUpstream, "upstream", llmproxy.DefaultUpstream,
		"Upstream LLM API base URL")
	cmd.Flags().BoolVar(&llmAutoCache, "auto-cache", false,
		"Inject prompt-cache breakpoints and upgrade TTL to 1h (biggest cost saving)")
	cmd.Flags().StringVar(&llmTailTTL, "tail-ttl", "5m",
		`TTL for the rolling-tail cache breakpoint ("5m" or "1h")`)
	cmd.Flags().StringSliceVar(&llmDropTools, "drop-tools", nil,
		"Comma-separated tool names to remove from every request")
	cmd.Flags().BoolVar(&llmStripANSI, "strip-ansi", true,
		"Strip ANSI escape codes from message content and tool results (default on)")
	cmd.Flags().BoolVar(&llmTrimBashGit, "trim-bash-git", false,
		`Truncate the Bash tool description at "# Committing changes with git" (~1 800 tokens saved)`)
	cmd.Flags().StringVar(&llmLogDir, "log-dir",
		envutil.GetEnvString("MCP_GATEWAY_LOG_DIR", config.DefaultLogDir),
		"Log file directory")

	// Enum completions for tail-ttl flag
	cmd.RegisterFlagCompletionFunc("tail-ttl", cobra.FixedCompletions(
		[]string{"5m", "1h"}, cobra.ShellCompDirectiveNoFileComp))

	return cmd
}

func runLLMProxy(cmd *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Validate tail-ttl
	if llmTailTTL != "5m" && llmTailTTL != "1h" {
		return fmt.Errorf("--tail-ttl must be \"5m\" or \"1h\" (got %q)", llmTailTTL)
	}

	// Initialise loggers
	logger.InitProxyLoggers(llmLogDir)

	// Build the drop-tools set from the slice flag.
	dropSet := make(map[string]bool, len(llmDropTools))
	for _, name := range llmDropTools {
		name = strings.TrimSpace(name)
		if name != "" {
			dropSet[name] = true
		}
	}

	logLLMProxyCmd.Printf("Starting llm-proxy: listen=%s upstream=%s autoCache=%v tailTTL=%s stripANSI=%v trimBashGit=%v dropTools=%v",
		llmListen, llmUpstream, llmAutoCache, llmTailTTL, llmStripANSI, llmTrimBashGit, llmDropTools)

	logger.LogInfo("startup", "LLM proxy starting: listen=%s upstream=%s autoCache=%v tailTTL=%s",
		llmListen, llmUpstream, llmAutoCache, llmTailTTL)

	srv := llmproxy.New(llmproxy.Config{
		Upstream:    llmUpstream,
		AutoCache:   llmAutoCache,
		TailTTL:     llmTailTTL,
		DropTools:   dropSet,
		StripANSI:   llmStripANSI,
		TrimBashGit: llmTrimBashGit,
	})

	httpServer := &http.Server{
		Addr:    llmListen,
		Handler: srv.Handler(),
	}

	go func() {
		listener, err := net.Listen("tcp", llmListen)
		if err != nil {
			log.Printf("llm-proxy: failed to listen on %s: %v", llmListen, err)
			cancel()
			return
		}

		actualAddr := listener.Addr().String()
		log.Printf("LLM proxy listening on http://%s", actualAddr)
		logger.LogInfo("startup", "LLM proxy listening on http://%s", actualAddr)

		fmt.Fprintf(os.Stderr, "\nMCPG LLM Proxy\n")
		fmt.Fprintf(os.Stderr, "  Listening: http://%s\n", actualAddr)
		fmt.Fprintf(os.Stderr, "  Upstream:  %s\n", llmUpstream)
		fmt.Fprintf(os.Stderr, "  AutoCache: %v (tail-ttl: %s)\n", llmAutoCache, llmTailTTL)
		fmt.Fprintf(os.Stderr, "  StripANSI: %v\n", llmStripANSI)
		fmt.Fprintf(os.Stderr, "  TrimBashGit: %v\n", llmTrimBashGit)
		if len(dropSet) > 0 {
			fmt.Fprintf(os.Stderr, "  DropTools: %v\n", llmDropTools)
		}
		fmt.Fprintf(os.Stderr, "\nPoint your LLM client at the proxy:\n")
		fmt.Fprintf(os.Stderr, "  export ANTHROPIC_BASE_URL=http://%s\n\n", actualAddr)

		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("llm-proxy: HTTP server error: %v", err)
			cancel()
		}
	}()

	<-ctx.Done()
	log.Println("llm-proxy: shutting down...")
	logger.LogInfo("shutdown", "LLM proxy shutting down")
	return httpServer.Close()
}
