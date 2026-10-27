package launcher

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
)

func TestGetOrLaunchWithConfig_ClosedLauncher(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.ServerConfig
		call func(l *Launcher, cfg *config.ServerConfig) error
	}{
		{
			name: "getOrLaunchWithConfig http backend",
			cfg:  &config.ServerConfig{Type: "http", URL: "http://127.0.0.1:1/mcp"},
			call: func(l *Launcher, cfg *config.ServerConfig) error {
				conn, err := getOrLaunchWithConfig(l, "backend", cfg)
				assert.Nil(t, conn)
				return err
			},
		},
		{
			name: "getOrLaunchWithConfig stdio backend",
			cfg:  &config.ServerConfig{Type: "stdio", Command: "docker", Args: []string{"run", "img"}},
			call: func(l *Launcher, cfg *config.ServerConfig) error {
				conn, err := getOrLaunchWithConfig(l, "backend", cfg)
				assert.Nil(t, conn)
				return err
			},
		},
		{
			name: "GetOrLaunch",
			cfg:  &config.ServerConfig{Type: "http", URL: "http://127.0.0.1:1/mcp"},
			call: func(l *Launcher, _ *config.ServerConfig) error {
				conn, err := GetOrLaunch(l, "backend")
				assert.Nil(t, conn)
				return err
			},
		},
		{
			name: "GetOrLaunchForSession stdio backend",
			cfg:  &config.ServerConfig{Type: "stdio", Command: "docker", Args: []string{"run", "img"}},
			call: func(l *Launcher, _ *config.ServerConfig) error {
				conn, err := GetOrLaunchForSession(l, "backend", "session-1")
				assert.Nil(t, conn)
				return err
			},
		},
		{
			name: "GetOrLaunchForSession http backend",
			cfg:  &config.ServerConfig{Type: "http", URL: "http://127.0.0.1:1/mcp"},
			call: func(l *Launcher, _ *config.ServerConfig) error {
				conn, err := GetOrLaunchForSession(l, "backend", "session-1")
				assert.Nil(t, conn)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := New(context.Background(), newTestConfig(map[string]*config.ServerConfig{"backend": tt.cfg}))
			l.Close()

			err := tt.call(l, tt.cfg)

			require.ErrorIs(t, err, ErrLauncherClosed)
			assert.Empty(t, l.connections)
		})
	}
}
