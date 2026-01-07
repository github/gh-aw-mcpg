package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/githubnext/gh-aw-mcpg/internal/config"
)

func TestMiddleware_Integration(t *testing.T) {
	// Create a temporary log file
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "integration.jsonl")

	// Create config with middleware enabled
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
		Middleware: &config.MiddlewareConfig{
			JSONLLog: &config.JSONLLogConfig{
				Enabled:  true,
				FilePath: logPath,
			},
		},
	}

	// Create unified server
	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create unified server: %v", err)
	}
	defer us.Close()

	// Verify log file was created
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("Log file was not created")
	}
}

func TestMiddleware_Disabled(t *testing.T) {
	// Create config without middleware
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create unified server: %v", err)
	}
	defer us.Close()

	// Should not crash, just no middleware configured
}

func TestMiddleware_InvalidLogPath(t *testing.T) {
	// Create config with invalid log path (directory doesn't exist)
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
		Middleware: &config.MiddlewareConfig{
			JSONLLog: &config.JSONLLogConfig{
				Enabled:  true,
				FilePath: "/nonexistent/directory/test.jsonl",
			},
		},
	}

	ctx := context.Background()
	_, err := NewUnified(ctx, cfg)
	if err == nil {
		t.Error("Expected error for invalid log path, got nil")
	}
}
