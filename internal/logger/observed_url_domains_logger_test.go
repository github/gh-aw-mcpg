package logger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetObservedURLDomainsLogger tears down and clears the global observed URL domains logger.
// Call this in t.Cleanup to guarantee the global is nil after each test so that
// residual state cannot affect subsequent tests.
func resetObservedURLDomainsLogger(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = CloseAllLoggers()
	})
}

// readObservedURLDomainsFile reads and parses the observed-url-domains.json file from logDir.
func readObservedURLDomainsFile(t *testing.T, logDir, fileName string) map[string][]string {
	t.Helper()
	filePath := filepath.Join(logDir, fileName)
	data, err := os.ReadFile(filePath)
	require.NoError(t, err, "failed to read observed URL domains file")

	var result map[string][]string
	err = json.Unmarshal(data, &result)
	require.NoError(t, err, "failed to parse observed URL domains JSON")
	return result
}

// ---- SetURLDomainAuditEnabled / URLDomainAuditEnabled ----

func TestURLDomainAuditEnabled_DefaultFalse(t *testing.T) {
	// The default state is false (atomic.Bool zero value).
	// Reset after so other tests are unaffected.
	t.Cleanup(func() { SetURLDomainAuditEnabled(false) })

	assert.False(t, URLDomainAuditEnabled(), "audit should be disabled by default")
}

func TestURLDomainAuditEnabled_Toggle(t *testing.T) {
	t.Cleanup(func() { SetURLDomainAuditEnabled(false) })

	SetURLDomainAuditEnabled(true)
	assert.True(t, URLDomainAuditEnabled(), "audit should be enabled after SetURLDomainAuditEnabled(true)")

	SetURLDomainAuditEnabled(false)
	assert.False(t, URLDomainAuditEnabled(), "audit should be disabled after SetURLDomainAuditEnabled(false)")
}

// ---- InitObservedURLDomainsLogger / CloseAllLoggers ----

func TestInitObservedURLDomainsLogger_Success(t *testing.T) {
	tmpDir := t.TempDir()
	resetObservedURLDomainsLogger(t)

	err := InitObservedURLDomainsLogger(tmpDir, observedURLDomainsFileName)
	require.NoError(t, err, "InitObservedURLDomainsLogger should succeed")

	// Verify the global logger was initialized.
	globalObservedURLDomainsMu.RLock()
	assert.NotNil(t, globalObservedURLDomainsLogger, "global observed URL domains logger should be set")
	globalObservedURLDomainsMu.RUnlock()

	// The setup function writes an initial empty JSON file.
	filePath := filepath.Join(tmpDir, observedURLDomainsFileName)
	_, err = os.Stat(filePath)
	assert.NoError(t, err, "initial observed-url-domains.json should exist after init")
}

func TestInitObservedURLDomainsLogger_FallbackOnBadDir(t *testing.T) {
	resetObservedURLDomainsLogger(t)

	// Providing a path that cannot be created (a file used as a directory) should
	// trigger the fallback path. Like ToolsLogger, ObservedURLDomainsLogger uses a
	// silent fallback: it returns nil error and a degraded logger with useFallback=true.
	tmpFile := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(tmpFile, []byte("x"), 0600))

	err := InitObservedURLDomainsLogger(filepath.Join(tmpFile, "subdir"), observedURLDomainsFileName)
	// No error is returned — the fallback handler absorbs it.
	require.NoError(t, err, "InitObservedURLDomainsLogger uses silent fallback, should not return error")

	// The global logger should be a fallback instance (not nil).
	globalObservedURLDomainsMu.RLock()
	assert.NotNil(t, globalObservedURLDomainsLogger, "fallback logger should still be set")
	assert.True(t, globalObservedURLDomainsLogger.useFallback, "logger should be in fallback mode")
	globalObservedURLDomainsMu.RUnlock()
}

func TestCloseAllLoggers_ObservedURLDomains_NilGlobal(t *testing.T) {
	// Ensure the global is nil before the test.
	_ = CloseAllLoggers()

	// Closing when there is no global logger should return nil without panicking.
	err := CloseAllLoggers()
	assert.NoError(t, err, "CloseAllLoggers on nil logger should be a no-op")
}

func TestCloseAllLoggers_ObservedURLDomains_ClearsGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, InitObservedURLDomainsLogger(tmpDir, observedURLDomainsFileName))

	err := CloseAllLoggers()
	require.NoError(t, err, "CloseAllLoggers should succeed")

	globalObservedURLDomainsMu.RLock()
	assert.Nil(t, globalObservedURLDomainsLogger, "global logger should be nil after close")
	globalObservedURLDomainsMu.RUnlock()
}

// ---- LogDomains (direct instance method) ----

// newTestObservedURLDomainsLogger initialises a fresh ObservedURLDomainsLogger writing
// to tmpDir/fileName and registers cleanup.
func newTestObservedURLDomainsLogger(t *testing.T) (*ObservedURLDomainsLogger, string) {
	t.Helper()
	tmpDir := t.TempDir()
	l, err := initLogger(tmpDir, observedURLDomainsFileName, os.O_TRUNC, observedURLDomainsLoggerFactory)
	require.NoError(t, err, "failed to create test ObservedURLDomainsLogger")
	t.Cleanup(func() { _ = l.Close() })
	return l, tmpDir
}

func TestLogDomains_EmptyServerID(t *testing.T) {
	l, _ := newTestObservedURLDomainsLogger(t)

	// Empty serverID should be a no-op and return nil.
	err := l.LogDomains("", []string{"example.com"})
	require.NoError(t, err, "empty serverID should return nil")
	assert.Empty(t, l.data, "data should remain empty after empty serverID call")
}

func TestLogDomains_EmptyDomains(t *testing.T) {
	l, _ := newTestObservedURLDomainsLogger(t)

	err := l.LogDomains("github", []string{})
	require.NoError(t, err, "empty domains slice should return nil")
	assert.Empty(t, l.data, "data should remain empty after empty domains call")
}

func TestLogDomains_NilDomains(t *testing.T) {
	l, _ := newTestObservedURLDomainsLogger(t)

	err := l.LogDomains("github", nil)
	require.NoError(t, err, "nil domains should return nil")
	assert.Empty(t, l.data, "data should remain empty after nil domains call")
}

func TestLogDomains_FallbackMode_ReturnsNil(t *testing.T) {
	l := &ObservedURLDomainsLogger{
		data:         make(map[string]map[string]struct{}),
		jsonFileSink: jsonFileSink{useFallback: true},
	}

	// In fallback mode LogDomains should silently succeed without writing.
	err := l.LogDomains("github", []string{"example.com"})
	require.NoError(t, err, "fallback mode should return nil")
	assert.Empty(t, l.data, "data should not be updated in fallback mode")
}

func TestLogDomains_AddsSingleDomain(t *testing.T) {
	l, tmpDir := newTestObservedURLDomainsLogger(t)

	err := l.LogDomains("github", []string{"example.com"})
	require.NoError(t, err)

	domains := readObservedURLDomainsFile(t, tmpDir, observedURLDomainsFileName)
	require.Contains(t, domains, "github")
	assert.Equal(t, []string{"example.com"}, domains["github"])
}

func TestLogDomains_DeduplicatesWithinCall(t *testing.T) {
	l, tmpDir := newTestObservedURLDomainsLogger(t)

	// Passing the same domain twice in one call should store it only once.
	err := l.LogDomains("github", []string{"api.github.com", "api.github.com"})
	require.NoError(t, err)

	domains := readObservedURLDomainsFile(t, tmpDir, observedURLDomainsFileName)
	require.Contains(t, domains, "github")
	assert.Equal(t, []string{"api.github.com"}, domains["github"])
}

func TestLogDomains_DeduplicatesAcrossCalls(t *testing.T) {
	l, tmpDir := newTestObservedURLDomainsLogger(t)

	require.NoError(t, l.LogDomains("github", []string{"api.github.com"}))
	// Second call with the same domain should produce no change.
	require.NoError(t, l.LogDomains("github", []string{"api.github.com"}))

	domains := readObservedURLDomainsFile(t, tmpDir, observedURLDomainsFileName)
	assert.Equal(t, []string{"api.github.com"}, domains["github"])
}

func TestLogDomains_AccumulatesNewDomains(t *testing.T) {
	l, tmpDir := newTestObservedURLDomainsLogger(t)

	require.NoError(t, l.LogDomains("github", []string{"api.github.com"}))
	require.NoError(t, l.LogDomains("github", []string{"uploads.github.com"}))

	domains := readObservedURLDomainsFile(t, tmpDir, observedURLDomainsFileName)
	require.Contains(t, domains, "github")

	// Results are sorted by util.SortedSetKeys.
	got := domains["github"]
	sort.Strings(got)
	assert.Equal(t, []string{"api.github.com", "uploads.github.com"}, got)
}

func TestLogDomains_SkipsEmptyDomainStrings(t *testing.T) {
	l, tmpDir := newTestObservedURLDomainsLogger(t)

	// A mix of valid and empty domain strings; only non-empty ones should persist.
	err := l.LogDomains("github", []string{"api.github.com", "", "uploads.github.com", ""})
	require.NoError(t, err)

	domains := readObservedURLDomainsFile(t, tmpDir, observedURLDomainsFileName)
	require.Contains(t, domains, "github")
	got := domains["github"]
	sort.Strings(got)
	assert.Equal(t, []string{"api.github.com", "uploads.github.com"}, got)
	assert.NotContains(t, got, "", "empty domain strings must be skipped")
}

func TestLogDomains_NoWriteWhenNothingNew(t *testing.T) {
	l, tmpDir := newTestObservedURLDomainsLogger(t)

	// Seed with one domain.
	require.NoError(t, l.LogDomains("github", []string{"api.github.com"}))

	filePath := filepath.Join(tmpDir, observedURLDomainsFileName)
	infoFirst, err := os.Stat(filePath)
	require.NoError(t, err)

	// Repeat the same domain — no new write should occur.
	// We can verify this indirectly: the internal data set size must be unchanged.
	require.NoError(t, l.LogDomains("github", []string{"api.github.com"}))

	infoSecond, err := os.Stat(filePath)
	require.NoError(t, err)

	// Modification time and size should be identical when nothing changed.
	assert.Equal(t, infoFirst.ModTime(), infoSecond.ModTime(),
		"file should not be rewritten when no new domain was added")
}

func TestLogDomains_MultipleServerIDs(t *testing.T) {
	l, tmpDir := newTestObservedURLDomainsLogger(t)

	require.NoError(t, l.LogDomains("github", []string{"api.github.com"}))
	require.NoError(t, l.LogDomains("slack", []string{"slack.com", "files.slack.com"}))

	domains := readObservedURLDomainsFile(t, tmpDir, observedURLDomainsFileName)
	assert.Contains(t, domains, "github", "github server should be present")
	assert.Contains(t, domains, "slack", "slack server should be present")
	assert.Equal(t, []string{"api.github.com"}, domains["github"])

	slackDomains := domains["slack"]
	sort.Strings(slackDomains)
	assert.Equal(t, []string{"files.slack.com", "slack.com"}, slackDomains)
}

func TestLogDomains_SortedOutputInFile(t *testing.T) {
	l, tmpDir := newTestObservedURLDomainsLogger(t)

	// Insert domains in reverse alphabetical order; the file should store them sorted.
	require.NoError(t, l.LogDomains("github", []string{"z.example.com", "a.example.com", "m.example.com"}))

	domains := readObservedURLDomainsFile(t, tmpDir, observedURLDomainsFileName)
	require.Contains(t, domains, "github")
	assert.Equal(t, []string{"a.example.com", "m.example.com", "z.example.com"}, domains["github"],
		"domains should be sorted in the output file")
}

// ---- LogObservedURLDomains (global helper) ----

func TestLogObservedURLDomains_NoGlobalLogger_NoPanic(t *testing.T) {
	// Ensure no global logger is set.
	_ = CloseAllLoggers()
	resetObservedURLDomainsLogger(t)

	// Calling the global helper with no logger initialised must not panic.
	assert.NotPanics(t, func() {
		LogObservedURLDomains("github", []string{"api.github.com"})
	})
}

func TestLogObservedURLDomains_DelegatesToGlobalLogger(t *testing.T) {
	tmpDir := t.TempDir()
	resetObservedURLDomainsLogger(t)

	require.NoError(t, InitObservedURLDomainsLogger(tmpDir, observedURLDomainsFileName))

	LogObservedURLDomains("github", []string{"api.github.com"})

	domains := readObservedURLDomainsFile(t, tmpDir, observedURLDomainsFileName)
	require.Contains(t, domains, "github")
	assert.Equal(t, []string{"api.github.com"}, domains["github"])
}

func TestLogObservedURLDomains_EmptyServerID_NoPanic(t *testing.T) {
	tmpDir := t.TempDir()
	resetObservedURLDomainsLogger(t)
	require.NoError(t, InitObservedURLDomainsLogger(tmpDir, observedURLDomainsFileName))

	assert.NotPanics(t, func() {
		LogObservedURLDomains("", []string{"example.com"})
	})
}

func TestLogObservedURLDomains_FallbackMode_NoPanic(t *testing.T) {
	// Directly override the global with a fallback instance.
	globalObservedURLDomainsMu.Lock()
	prev := globalObservedURLDomainsLogger
	globalObservedURLDomainsLogger = &ObservedURLDomainsLogger{
		data:         make(map[string]map[string]struct{}),
		jsonFileSink: jsonFileSink{useFallback: true},
	}
	globalObservedURLDomainsMu.Unlock()
	t.Cleanup(func() {
		globalObservedURLDomainsMu.Lock()
		globalObservedURLDomainsLogger = prev
		globalObservedURLDomainsMu.Unlock()
	})

	assert.NotPanics(t, func() {
		LogObservedURLDomains("github", []string{"api.github.com"})
	})
}

func TestLogObservedURLDomains_WriteError_WarningLogged(t *testing.T) {
	// Arrange: initialise the logger in a real temp directory, then remove the
	// directory so that writeToFile fails the next time a new domain is logged.
	tmpDir := t.TempDir()
	resetObservedURLDomainsLogger(t)
	require.NoError(t, InitObservedURLDomainsLogger(tmpDir, observedURLDomainsFileName))

	// Destroy the directory so the atomic write will fail.
	require.NoError(t, os.RemoveAll(tmpDir))

	// The error path in LogObservedURLDomains (warning log) must not panic.
	assert.NotPanics(t, func() {
		LogObservedURLDomains("github", []string{"api.github.com"})
	})
}

// ---- setupObservedURLDomainsLogger ----

// TestSetupObservedURLDomainsLogger_WriteToFileFails exercises the return nil, err
// branch inside setupObservedURLDomainsLogger.
//
// The branch is not reachable through the normal InitObservedURLDomainsLogger path:
// when the log directory is unwritable, initLogFile fails first and the fallback
// handler (handleObservedURLDomainsLoggerError) is called instead of setup.
//
// To cover the branch we call setupObservedURLDomainsLogger directly with a logDir
// where the target file name already exists as a directory. util.AtomicWriteFile can
// still create the temp file (filePath+".tmp") in the parent dir, but os.Rename will
// return EISDIR on Linux, causing writeToFile to fail.
func TestObservedURLDomainsLoggerFactory_Setup_WriteToFileFails(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a directory at the target file path so util.AtomicWriteFile's Rename fails.
	targetDir := filepath.Join(tmpDir, observedURLDomainsFileName)
	require.NoError(t, os.MkdirAll(targetDir, 0755))

	l, err := observedURLDomainsLoggerFactory.setup(nil, tmpDir, observedURLDomainsFileName)

	require.Error(t, err, "observedURLDomainsLoggerFactory.setup should return an error when writeToFile fails")
	assert.Nil(t, l, "logger should be nil on setup failure")
	assert.Contains(t, err.Error(), "failed to rename temp file",
		"error should originate from the atomic rename step")
}

// ---- ObservedURLDomainsLogger.Close ----

func TestObservedURLDomainsLogger_Close_ReturnsNil(t *testing.T) {
	l, _ := newTestObservedURLDomainsLogger(t)
	assert.NoError(t, l.Close(), "Close should always return nil")
}
