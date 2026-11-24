package logger

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoggerNamespacesMatchFileConventions(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller should resolve this test file path")

	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	internalRoot := filepath.Join(repoRoot, "internal")

	exceptionNamespaces := map[string][]string{
		// The following files use intentionally shorter or semantically clearer namespaces
		// instead of the full file-name-derived form. These are preserved for backward
		// compatibility with existing DEBUG filter configurations.
		"internal/config/config_core.go":       {"config:config"},
		"internal/config/config_feature.go":    {"config:feature"},
		"internal/envutil/expand_env_args.go":  {"envutil:expand"},
		"internal/guard/wasm_lifecycle.go":     {"guard:wasm"},
		"internal/guard/write_sink.go":         {"guard:write-sink"},
		"internal/launcher/connection_pool.go": {"launcher:pool"},
		"internal/launcher/health_monitor.go":  {"launcher:health"},

		// http_server.go defines two loggers: the primary one for the HTTP server itself
		// (auto-derived via ForFile as "server:http_server") and a second one with the custom
		// namespace "server:transport" for transport-layer events, allowing independent filtering.
		"internal/server/http_server.go":  {"server:transport"},
		"internal/server/http_helpers.go": {"server:helpers"},

		"internal/server/middleware_auth.go":   {"server:auth"},
		"internal/server/sdk_logging.go":       {"server:sdk-frontend"},
		"internal/server/session_auto_init.go": {"server:auto-init"},

		// testutil/mcptest/server.go uses "testutil:mcptest" (package/feature oriented) rather
		// than the file-derived "mcptest:server". testutil/mcptest/validator.go similarly uses
		// "testutil:validator" to group all testutil helpers under a common DEBUG prefix.
		"internal/testutil/mcptest/server.go":    {"testutil:mcptest"},
		"internal/testutil/mcptest/validator.go": {"testutil:validator"},
	}

	seenExceptions := map[string]bool{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, walkErr error) error {
		require.NoError(t, walkErr)
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err)

		actual := loggerNamespaces(file)
		if len(actual) == 0 {
			return nil
		}

		relPath, err := filepath.Rel(repoRoot, path)
		require.NoError(t, err)
		relPath = filepath.ToSlash(relPath)

		expected := []string{
			filepath.Base(filepath.Dir(path)) + ":" + strings.TrimSuffix(filepath.Base(path), ".go"),
		}
		if overrides, ok := exceptionNamespaces[relPath]; ok {
			expected = overrides
			seenExceptions[relPath] = true
		}

		require.ElementsMatch(t, expected, actual, "unexpected logger namespaces in %s", relPath)
		return nil
	})
	require.NoError(t, err)

	var unusedExceptions []string
	for relPath := range exceptionNamespaces {
		if !seenExceptions[relPath] {
			unusedExceptions = append(unusedExceptions, relPath)
		}
	}
	slices.Sort(unusedExceptions)
	require.Empty(t, unusedExceptions, "logger namespace exception list contains unused entries")
}

func loggerNamespaces(file *ast.File) []string {
	var namespaces []string

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok || ident.Name != "logger" || selector.Sel.Name != "New" {
			return true
		}
		if len(call.Args) != 1 {
			return true
		}

		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			namespaces = append(namespaces, "<non-literal logger namespace>")
			return true
		}

		namespace, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}

		namespaces = append(namespaces, namespace)
		return true
	})

	return namespaces
}
