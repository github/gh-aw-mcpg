package config

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileSchemaDocument(t *testing.T) {
	validDoc := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
	}

	t.Run("success compiles and validates", func(t *testing.T) {
		schema, cerr := compileSchemaDocument(newCompiler(), "https://example.test/ok.json", validDoc, "https://example.test/ok.json")
		require.Nil(t, cerr) // *schemaCompileError (not error) — Nil is intentional
		require.NotNil(t, schema)
		assert.NoError(t, schema.Validate(map[string]any{"a": 1}))
		assert.Error(t, schema.Validate("not-an-object"))
	})

	t.Run("success with multiple resource IDs", func(t *testing.T) {
		schema, cerr := compileSchemaDocument(newCompiler(), "https://example.test/a.json", validDoc,
			"https://example.test/a.json", "https://example.test/b.json")
		require.Nil(t, cerr) // *schemaCompileError (not error) — Nil is intentional
		assert.NotNil(t, schema)
	})

	t.Run("success with no resource IDs fails at compile as unresolved", func(t *testing.T) {
		schema, cerr := compileSchemaDocument(newCompiler(), "https://example.test/missing.json", validDoc)
		assert.Nil(t, schema)
		require.NotNil(t, cerr)
		assert.True(t, cerr.compile)
		assert.Empty(t, cerr.resourceID)
	})

	t.Run("AddResource failure reports resource ID", func(t *testing.T) {
		schema, cerr := compileSchemaDocument(newCompiler(), "https://example.test/x.json", validDoc, "://bad url")
		assert.Nil(t, schema)
		require.NotNil(t, cerr)
		assert.False(t, cerr.compile)
		assert.Equal(t, "://bad url", cerr.resourceID)
		assert.Error(t, cerr)
	})

	t.Run("Compile failure on invalid schema", func(t *testing.T) {
		badDoc := map[string]any{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type":    "not-a-real-type",
		}
		schema, cerr := compileSchemaDocument(newCompiler(), "https://example.test/bad.json", badDoc, "https://example.test/bad.json")
		assert.Nil(t, schema)
		require.NotNil(t, cerr)
		assert.True(t, cerr.compile)
		assert.Empty(t, cerr.resourceID)
		assert.NotEmpty(t, cerr.Error())
	})
}

func TestSchemaCompileError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("boom")
	cerr := &schemaCompileError{resourceID: "r", compile: true, err: inner}

	assert.Equal(t, "boom", cerr.Error())
	assert.Same(t, inner, cerr.Unwrap())
	assert.ErrorIs(t, cerr, inner)
}
