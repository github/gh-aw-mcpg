package config

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJSONSchemaErrorKindsCanary pins the upstream error kinds and instance paths
// used by our formatter, using real validation failures rather than synthetic errors.
func TestJSONSchemaErrorKindsCanary(t *testing.T) {
	tests := []struct {
		name     string
		schema   string
		instance string
		wantKind jsonschema.ErrorKind
		detail   string
	}{
		{
			name: "additional properties", schema: `{"additionalProperties": false}`,
			instance: `{"extra": true}`, wantKind: &kind.AdditionalProperties{},
			detail: `Details: Unexpected field(s): "extra"`,
		},
		{
			name: "type", schema: `{"type": "integer"}`,
			instance: `"wrong"`, wantKind: &kind.Type{},
			detail: "Details: Type mismatch - expected integer, got string",
		},
		{
			name: "enum", schema: `{"enum": ["stdio", "http"]}`,
			instance: `"other"`, wantKind: &kind.Enum{},
			detail: "Details: Invalid value - allowed values: stdio, http",
		},
		{
			name: "required", schema: `{"required": ["container"]}`,
			instance: `{}`, wantKind: &kind.Required{},
			detail: "Details: Missing required field(s): container",
		},
		{
			name: "pattern", schema: `{"pattern": "^[a-z]+$"}`,
			instance: `"INVALID"`, wantKind: &kind.Pattern{},
			detail: "Details: Value format is incorrect",
		},
		{
			name: "format", schema: `{"format": "uri"}`,
			instance: `"not a URI"`, wantKind: &kind.Format{},
			detail: "Details: Value does not match the required format",
		},
		{
			name: "minimum", schema: `{"minimum": 1}`,
			instance: `0`, wantKind: &kind.Minimum{},
			detail: "Details: Value is outside the allowed range",
		},
		{
			name: "maximum", schema: `{"maximum": 65535}`,
			instance: `65536`, wantKind: &kind.Maximum{},
			detail: "Details: Value is outside the allowed range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const schemaURL = "https://example.com/canary.schema.json"
			doc, err := jsonschema.UnmarshalJSON(strings.NewReader(
				`{"type": "object", "properties": {"value": ` + tt.schema + `}}`))
			require.NoError(t, err)
			compiler := newCompiler()
			require.NoError(t, compiler.AddResource(schemaURL, doc))
			schema, err := compiler.Compile(schemaURL)
			require.NoError(t, err)
			instance, err := jsonschema.UnmarshalJSON(strings.NewReader(
				`{"value": ` + tt.instance + `}`))
			require.NoError(t, err)

			err = schema.Validate(instance)
			var ve *jsonschema.ValidationError
			require.ErrorAs(t, err, &ve)
			for len(ve.Causes) > 0 {
				require.Len(t, ve.Causes, 1)
				ve = ve.Causes[0]
			}
			assert.IsType(t, tt.wantKind, ve.ErrorKind)
			assert.Equal(t, []string{"value"}, ve.InstanceLocation)
			formatted := formatSchemaError(err)
			require.Error(t, formatted)
			assert.Contains(t, formatted.Error(), "Location: /value\n")
			assert.Contains(t, formatted.Error(), tt.detail)
		})
	}
}
