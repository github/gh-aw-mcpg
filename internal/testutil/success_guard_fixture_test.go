package testutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tetratelabs/wazero"
)

func TestWriteSuccessGuardWasm(t *testing.T) {
	t.Run("writes a private regular file named guard.wasm", func(t *testing.T) {
		path := WriteSuccessGuardWasm(t)

		assert.Equal(t, "guard.wasm", filepath.Base(path))
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.True(t, info.Mode().IsRegular())
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		assert.NotZero(t, info.Size())
	})

	t.Run("file starts with the WASM magic header", func(t *testing.T) {
		data, err := os.ReadFile(WriteSuccessGuardWasm(t))
		require.NoError(t, err)

		require.GreaterOrEqual(t, len(data), 8)
		assert.Equal(t, []byte{0x00, 'a', 's', 'm'}, data[:4])
		assert.Equal(t, []byte{0x01, 0x00, 0x00, 0x00}, data[4:8])
	})

	t.Run("each call writes to a distinct temp directory", func(t *testing.T) {
		first := WriteSuccessGuardWasm(t)
		second := WriteSuccessGuardWasm(t)

		assert.NotEqual(t, first, second)
	})

	t.Run("module compiles and exports guard entry points", func(t *testing.T) {
		data, err := os.ReadFile(WriteSuccessGuardWasm(t))
		require.NoError(t, err)

		ctx := context.Background()
		rt := wazero.NewRuntime(ctx)
		t.Cleanup(func() { _ = rt.Close(ctx) })

		compiled, err := rt.CompileModule(ctx, data)
		require.NoError(t, err)

		exports := compiled.ExportedFunctions()
		for _, name := range []string{"label_resource", "label_response", "label_agent"} {
			assert.Contains(t, exports, name)
		}
		assert.Contains(t, compiled.ExportedMemories(), "memory")
	})
}
