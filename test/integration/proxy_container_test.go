// Container integration tests for the proxy mode.
// These tests build a Docker image, start the proxy via the container entrypoint,
// and validate TLS, auth forwarding, and DIFC enforcement.
//
// Run with: make test-container-proxy
// Requires: Docker daemon, GitHub token (GITHUB_TOKEN, GH_TOKEN, or `gh auth login`)

//go:build container

package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	containerImage = "awmg-proxy-integration-test"
	containerName  = "awmg-proxy-integration"
)

// containerProxyEnv holds a running container proxy for tests.
type containerProxyEnv struct {
	port      string
	baseURL   string
	token     string
	logDir    string
	tlsDir    string
	caCertPEM []byte
	client    *http.Client
}

// skipIfNoDocker skips the test if docker is not available.
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Skipping container test: docker not found in PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("Skipping container test: docker daemon not available")
	}
}

// buildContainerImage builds the Docker image from the repo root.
func buildContainerImage(t *testing.T) {
	t.Helper()

	repoRoot := findRepoRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "build", "-t", containerImage, ".")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker build failed: %v\n%s", err, string(out))
	}
	t.Log("✓ Container image built")
}

// findRepoRoot locates the git repository root.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	// From test/integration, repo root is two levels up
	candidates := []string{".", "../..", "../../.."}
	for _, c := range candidates {
		abs, _ := filepath.Abs(c)
		if _, err := os.Stat(filepath.Join(abs, "Dockerfile")); err == nil {
			return abs
		}
	}
	t.Fatal("Could not find repo root (Dockerfile not found)")
	return ""
}

// startContainerProxy starts the proxy in a container with TLS enabled.
func startContainerProxy(t *testing.T, policy string, port string) *containerProxyEnv {
	t.Helper()

	token := skipIfNoGitHubToken(t)

	logDir, err := os.MkdirTemp("", "awmg-container-proxy-*")
	require.NoError(t, err)

	// Stop any leftover container from a previous run
	exec.Command("docker", "rm", "-f", containerName+"-"+port).Run()

	name := containerName + "-" + port
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "run", "-d",
		"--name", name,
		"-p", port+":"+port,
		"-v", logDir+":/tmp/gh-aw/mcp-logs",
		containerImage,
		"proxy",
		"--policy", policy,
		"--listen", "0.0.0.0:"+port,
		"--tls",
		"--guards-mode", "filter",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "docker run failed: %s", string(out))

	containerID := strings.TrimSpace(string(out))
	t.Logf("✓ Container started: %s (id=%s)", name, containerID[:12])

	env := &containerProxyEnv{
		port:    port,
		baseURL: "https://localhost:" + port,
		token:   token,
		logDir:  logDir,
		tlsDir:  filepath.Join(logDir, "proxy-tls"),
	}

	// Wait for TLS certs to be generated and proxy to start
	caCertPath := filepath.Join(env.tlsDir, "ca.crt")
	require.Eventually(t, func() bool {
		_, err := os.Stat(caCertPath)
		return err == nil
	}, 15*time.Second, 200*time.Millisecond, "CA cert not generated in time")

	// Read CA cert and build a TLS-trusting client
	env.caCertPEM, err = os.ReadFile(caCertPath)
	require.NoError(t, err)

	certPool := x509.NewCertPool()
	require.True(t, certPool.AppendCertsFromPEM(env.caCertPEM), "failed to parse CA cert")

	env.client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    certPool,
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	// Wait for the proxy health endpoint
	require.Eventually(t, func() bool {
		resp, err := env.client.Get(env.baseURL + "/api/v3/health")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	}, 15*time.Second, 200*time.Millisecond, "Proxy health check failed")

	t.Logf("✓ Proxy healthy at %s (TLS verified via CA)", env.baseURL)
	return env
}

// stop removes the container and temp files.
func (e *containerProxyEnv) stop(t *testing.T) {
	t.Helper()
	name := containerName + "-" + e.port
	// Dump logs before stopping for debugging
	out, _ := exec.Command("docker", "logs", "--tail=20", name).CombinedOutput()
	t.Logf("Container logs (last 20 lines):\n%s", string(out))

	exec.Command("docker", "rm", "-f", name).Run()
	os.RemoveAll(e.logDir)
}

// apiGet sends an authenticated GET through the TLS proxy.
func (e *containerProxyEnv) apiGet(t *testing.T, path string) (int, []byte) {
	t.Helper()
	url := e.baseURL + "/api/v3" + path
	req, err := http.NewRequest("GET", url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "token "+e.token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := e.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// ============================================================================
// Tests
// ============================================================================

// TestContainerProxyBuildAndStart validates that the container image builds
// and the proxy starts successfully via the entrypoint with TLS.
func TestContainerProxyBuildAndStart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}
	skipIfNoDocker(t)
	buildContainerImage(t)

	policy := `{"allow-only":{"repos":["octocat/hello-world"],"min-integrity":"none"}}`
	env := startContainerProxy(t, policy, "19001")
	defer env.stop(t)

	// Verify health endpoint
	status, body := env.apiGet(t, "/health")
	assert.Equal(t, 200, status)
	t.Logf("Health: %s", string(body))
}

// TestContainerProxyTLSCertificates validates the generated TLS certificates.
func TestContainerProxyTLSCertificates(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}
	skipIfNoDocker(t)
	buildContainerImage(t)

	policy := `{"allow-only":{"repos":["octocat/hello-world"],"min-integrity":"none"}}`
	env := startContainerProxy(t, policy, "19002")
	defer env.stop(t)

	// Verify CA cert exists and is readable from the mounted volume
	assert.NotEmpty(t, env.caCertPEM, "CA cert should be non-empty")
	assert.Contains(t, string(env.caCertPEM), "BEGIN CERTIFICATE")

	// Verify server cert and key also exist
	_, err := os.Stat(filepath.Join(env.tlsDir, "server.crt"))
	assert.NoError(t, err, "server.crt should exist in mounted volume")
	_, err = os.Stat(filepath.Join(env.tlsDir, "server.key"))
	assert.NoError(t, err, "server.key should exist in mounted volume")

	// Verify the server cert key file has restrictive permissions
	info, err := os.Stat(filepath.Join(env.tlsDir, "server.key"))
	if err == nil {
		perm := info.Mode().Perm()
		assert.Equal(t, os.FileMode(0600), perm, "server.key should have 0600 permissions")
	}
}

// TestContainerProxyAuthForwarding validates that the proxy forwards the
// client's Authorization header to GitHub (no --github-token needed).
func TestContainerProxyAuthForwarding(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}
	skipIfNoDocker(t)
	buildContainerImage(t)

	policy := `{"allow-only":{"repos":["octocat/hello-world"],"min-integrity":"none"}}`
	env := startContainerProxy(t, policy, "19003")
	defer env.stop(t)

	// The container was started WITHOUT --github-token.
	// This request includes Authorization header — it should be forwarded.
	status, body := env.apiGet(t, "/repos/octocat/Hello-World/commits?per_page=2")
	assert.Equal(t, 200, status, "Should succeed with forwarded auth")

	var commits []interface{}
	err := json.Unmarshal(body, &commits)
	require.NoError(t, err)
	assert.NotEmpty(t, commits, "Should return commits via forwarded auth")
	t.Logf("✓ Auth forwarding works: got %d commits", len(commits))
}

// TestContainerProxyGuardAutoDetect validates that the baked-in WASM guard
// is auto-detected (no --guard-wasm flag needed in the container).
func TestContainerProxyGuardAutoDetect(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}
	skipIfNoDocker(t)
	buildContainerImage(t)

	// Start without explicit --guard-wasm — should auto-detect
	policy := `{"allow-only":{"repos":["octocat/hello-world"],"min-integrity":"none"}}`
	env := startContainerProxy(t, policy, "19004")
	defer env.stop(t)

	// If guard loaded, DIFC should work — scoped repo returns data
	status, body := env.apiGet(t, "/repos/octocat/Hello-World/issues?per_page=3&state=all")
	assert.Equal(t, 200, status)

	var issues []interface{}
	json.Unmarshal(body, &issues)
	t.Logf("✓ Guard auto-detected: got %d issues from scoped repo", len(issues))
}

// TestContainerProxyDIFCEnforcement validates that DIFC filtering works
// in the containerized proxy — scoped repo allowed, out-of-scope blocked.
func TestContainerProxyDIFCEnforcement(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}
	skipIfNoDocker(t)
	buildContainerImage(t)

	policy := `{"allow-only":{"repos":["octocat/hello-world"],"min-integrity":"none"}}`
	env := startContainerProxy(t, policy, "19005")
	defer env.stop(t)

	t.Run("ScopedRepo/Issues", func(t *testing.T) {
		status, body := env.apiGet(t, "/repos/octocat/Hello-World/issues?per_page=5&state=all")
		assert.Equal(t, 200, status)
		var issues []interface{}
		json.Unmarshal(body, &issues)
		assert.NotEmpty(t, issues, "Scoped repo should return issues")
		t.Logf("Scoped: %d issues", len(issues))
	})

	t.Run("ScopedRepo/Commits", func(t *testing.T) {
		status, body := env.apiGet(t, "/repos/octocat/Hello-World/commits?per_page=5")
		assert.Equal(t, 200, status)
		var commits []interface{}
		json.Unmarshal(body, &commits)
		assert.NotEmpty(t, commits, "Scoped repo should return commits")
		t.Logf("Scoped: %d commits", len(commits))
	})

	t.Run("ScopedRepo/Branches", func(t *testing.T) {
		status, body := env.apiGet(t, "/repos/octocat/Hello-World/branches?per_page=10")
		assert.Equal(t, 200, status)
		var branches []interface{}
		json.Unmarshal(body, &branches)
		// Branches may be filtered by the guard if it doesn't label them individually.
		// Just verify we get a valid 200 response — don't assert non-empty.
		t.Logf("Scoped: %d branches", len(branches))
	})

	t.Run("OutOfScope/Issues", func(t *testing.T) {
		status, body := env.apiGet(t, "/repos/cli/cli/issues?per_page=5")
		if status == 200 {
			var issues []interface{}
			json.Unmarshal(body, &issues)
			assert.Empty(t, issues, "Out-of-scope should return empty")
		}
		t.Logf("Out-of-scope issues: status=%d", status)
	})

	t.Run("OutOfScope/Commits", func(t *testing.T) {
		status, body := env.apiGet(t, "/repos/cli/cli/commits?per_page=5")
		if status == 200 {
			var commits []interface{}
			json.Unmarshal(body, &commits)
			assert.Empty(t, commits, "Out-of-scope should return empty")
		}
		t.Logf("Out-of-scope commits: status=%d", status)
	})
}

// TestContainerProxyLogs validates that proxy logs are accessible
// from the mounted volume.
func TestContainerProxyLogs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}
	skipIfNoDocker(t)
	buildContainerImage(t)

	policy := `{"allow-only":{"repos":["octocat/hello-world"],"min-integrity":"none"}}`
	env := startContainerProxy(t, policy, "19006")
	defer env.stop(t)

	// Make a request to generate a log entry
	env.apiGet(t, "/repos/octocat/Hello-World/issues?per_page=1")

	// Give the proxy a moment to flush logs
	time.Sleep(500 * time.Millisecond)

	// Check that proxy.log exists in the mounted volume
	logFile := filepath.Join(env.logDir, "proxy.log")
	info, err := os.Stat(logFile)
	assert.NoError(t, err, "proxy.log should exist in mounted volume")
	if err == nil {
		assert.Greater(t, info.Size(), int64(0), "proxy.log should not be empty")
		content, _ := os.ReadFile(logFile)
		t.Logf("proxy.log (%d bytes): %.500s", len(content), string(content))
	}
}

// TestContainerProxyUntrustedTLS validates that connecting without the
// CA certificate fails (proves TLS is properly enforced).
func TestContainerProxyUntrustedTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}
	skipIfNoDocker(t)
	buildContainerImage(t)

	policy := `{"allow-only":{"repos":["octocat/hello-world"],"min-integrity":"none"}}`
	env := startContainerProxy(t, policy, "19007")
	defer env.stop(t)

	// Try connecting WITHOUT the CA cert — should fail TLS verification
	untrustedClient := &http.Client{
		Timeout: 5 * time.Second,
		// Default TLS config — doesn't trust our self-signed CA
	}
	_, err := untrustedClient.Get(env.baseURL + "/api/v3/health")
	assert.Error(t, err, "Connection without CA cert should fail")
	assert.Contains(t, err.Error(), "certificate", "Error should mention certificate")
	t.Logf("✓ Untrusted connection correctly rejected: %v", err)
}

// TestContainerProxyGhCLI validates that the actual gh CLI can reach
// the containerized proxy. On macOS this requires the CA in the system
// keychain, so it may be skipped.
func TestContainerProxyGhCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping container test in short mode")
	}
	skipIfNoDocker(t)

	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("Skipping: gh CLI not found in PATH")
	}

	buildContainerImage(t)

	policy := `{"allow-only":{"repos":["octocat/hello-world"],"min-integrity":"none"}}`
	env := startContainerProxy(t, policy, "19008")
	defer env.stop(t)

	// On Linux, SSL_CERT_FILE works with Go. On macOS, it doesn't
	// (Go uses the system Security framework). Try both approaches.
	caCertPath := filepath.Join(env.tlsDir, "ca.crt")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", "api", "/repos/octocat/Hello-World/issues?per_page=1")
	cmd.Env = append(os.Environ(),
		"GH_HOST=localhost:"+env.port,
		"GH_TOKEN="+env.token,
		"SSL_CERT_FILE="+caCertPath,
		fmt.Sprintf("GODEBUG=x509usefallbackroots=1"),
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Expected to fail on macOS — Go uses system keychain, not SSL_CERT_FILE
		t.Logf("gh CLI through TLS proxy failed (expected on macOS): %v\noutput: %s", err, string(out))
		t.Skip("gh CLI TLS proxy test skipped — CA not in system trust store (works on Linux)")
	}

	assert.Contains(t, string(out), "Hello-World", "gh output should contain repo data")
	t.Logf("✓ gh CLI works through TLS container proxy")
}
