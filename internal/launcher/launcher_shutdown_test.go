package launcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLauncher_StoreConnectionIfOpen(t *testing.T) {
	for _, closed := range []bool{false, true} {
		for _, session := range []bool{false, true} {
			t.Run(fmt.Sprintf("closed=%t/session=%t", closed, session), func(t *testing.T) {
				l := New(context.Background(), newTestConfig(nil))
				defer l.Close()
				if closed {
					l.Close()
				}
				conn := &mcp.Connection{}
				stored := false
				err := l.storeConnectionIfOpen("backend", conn, func() {
					stored = true
					unlocked := l.mu.TryLock()
					if unlocked {
						l.mu.Unlock()
					}
					assert.False(t, unlocked, "store must run under the launcher lock")
					assert.Empty(t, l.serverStartTimes, "start must be recorded after storing")
					if session {
						l.sessionPool.Set("backend", "session", conn)
					} else {
						l.connections["backend"] = conn
					}
				})

				if closed {
					require.ErrorIs(t, err, ErrLauncherClosed)
					assert.False(t, stored)
					assert.Empty(t, l.connections)
					assert.Empty(t, l.sessionPool.List())
					assert.Empty(t, l.serverStartTimes)
					return
				}
				require.NoError(t, err)
				assert.True(t, stored)
				assert.NotZero(t, l.serverStartTimes["backend"])
				if session {
					actual, ok := l.sessionPool.Get("backend", "session")
					assert.True(t, ok)
					assert.Same(t, conn, actual)
				} else {
					assert.Same(t, conn, l.connections["backend"])
				}
			})
		}
	}
}

// TestLauncherStdioServer re-executes the test binary as an MCP backend, waiting
// for the parent test to release initialization before serving the handshake.
func TestLauncherStdioServer(t *testing.T) {
	barrierURL := os.Getenv("GO_TEST_LAUNCHER_STDIO_BARRIER")
	if barrierURL == "" {
		t.Skip("subprocess helper")
	}

	resp, err := http.Get(barrierURL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	server := sdk.NewServer(&sdk.Implementation{Name: "shutdown-test", Version: "1"}, nil)
	require.NoError(t, server.Run(context.Background(), &sdk.StdioTransport{}))
	os.Exit(0)
}

func TestLauncher_CloseDuringInitialization(t *testing.T) {
	for _, mode := range []string{"http", "stdio", "session"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			var startedOnce, releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			wait := func() {
				startedOnce.Do(func() { close(started) })
				<-release
			}

			backend := sdk.NewServer(&sdk.Implementation{Name: "shutdown-test", Version: "1"}, nil)
			backend.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
				return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
					if method == "initialize" {
						wait()
					}
					return next(ctx, method, req)
				}
			})
			mux := http.NewServeMux()
			mux.Handle("/mcp", sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server {
				return backend
			}, nil))
			mux.HandleFunc("/barrier", func(w http.ResponseWriter, r *http.Request) { wait() })
			server := httptest.NewServer(mux)
			defer server.Close()
			defer unblock()

			self, err := os.Executable()
			require.NoError(t, err)
			serverCfg := &config.ServerConfig{Type: "http", URL: server.URL + "/mcp"}
			if mode != "http" {
				serverCfg = &config.ServerConfig{
					Type:    "stdio",
					Command: self,
					Args:    []string{"-test.run=^TestLauncherStdioServer$"},
					Env:     map[string]string{"GO_TEST_LAUNCHER_STDIO_BARRIER": server.URL + "/barrier"},
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			l := New(ctx, newTestConfig(map[string]*config.ServerConfig{"backend": serverCfg}))
			defer func() {
				unblock()
				cancel()
				l.Close()
			}()
			launch := func() error {
				if mode == "session" {
					conn, err := GetOrLaunchForSession(l, "backend", "")
					if err != nil {
						assert.Nil(t, conn)
					}
					return err
				}
				conn, err := GetOrLaunch(l, "backend")
				if err != nil {
					assert.Nil(t, conn)
				}
				return err
			}
			result := make(chan error, 1)
			go func() { result <- launch() }()

			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("backend initialization did not start")
			}

			closed := make(chan struct{})
			go func() {
				l.Close()
				close(closed)
			}()
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("shutdown blocked on backend initialization")
			}
			require.ErrorIs(t, launch(), ErrLauncherClosed)
			unblock()

			select {
			case err := <-result:
				require.ErrorIs(t, err, ErrLauncherClosed)
			case <-ctx.Done():
				t.Fatal("launch did not finish after initialization was released")
			}
			assert.Empty(t, l.connections)
			assert.Empty(t, l.sessionPool.List())
			assert.Equal(t, "stopped", l.GetServerState("backend").Status)
		})
	}
}
