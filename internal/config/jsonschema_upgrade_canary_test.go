package config

import (
	"reflect"
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
		name         string
		schema       string
		instance     string
		draft        *jsonschema.Draft
		wantKind     jsonschema.ErrorKind
		wantLocation []string
		detail       string
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
		{
			name: "dependent required", schema: `{"dependentRequired": {"container": ["args"]}}`,
			instance: `{"container": "image"}`, wantKind: &kind.DependentRequired{},
			detail: `Details: Missing required field(s) for "container": args`,
		},
		{
			name: "property names", schema: `{"propertyNames": {"pattern": "^[a-z]+$"}}`,
			instance: `{"Bad_Name": true}`, wantKind: &kind.PropertyNames{},
			detail: `Details: Invalid property name "Bad_Name"`,
		},
		{
			name: "contains", schema: `{"contains": {"type": "integer"}}`,
			instance: `["wrong"]`, wantKind: &kind.Contains{},
			detail: "Details: Array does not satisfy the required contains constraint",
		},
		{
			name: "min contains", schema: `{"contains": {"type": "integer"}, "minContains": 2}`,
			instance: `[1, "wrong"]`, wantKind: &kind.MinContains{},
			detail: "Details: Array must contain at least 2 matching item(s), found 1",
		},
		{
			name: "unique items", schema: `{"uniqueItems": true}`,
			instance: `[{"key": 1}, {"key": 1}]`, wantKind: &kind.UniqueItems{},
			detail: "Details: Array items must be unique — duplicate values are not allowed",
		},
		{
			name:     "nested array items after prefix items",
			schema:   `{"prefixItems": [{}], "items": {"prefixItems": [{}], "items": {"type": "integer"}}}`,
			instance: `[null, [0, "wrong"]]`, wantKind: &kind.Type{},
			wantLocation: []string{"value", "1", "1"},
			detail:       "Details: Type mismatch - expected integer, got string",
		},
		{
			name:     "nested array additional items after tuple items",
			schema:   `{"items": [{}], "additionalItems": {"items": [{}], "additionalItems": {"type": "integer"}}}`,
			instance: `[null, [0, "wrong"]]`, draft: jsonschema.Draft7, wantKind: &kind.Type{},
			wantLocation: []string{"value", "1", "1"},
			detail:       "Details: Type mismatch - expected integer, got string",
		},
		{
			name: "additional items", schema: `{"items": [{}], "additionalItems": false}`,
			instance: `[1, 2]`, draft: jsonschema.Draft7, wantKind: &kind.AdditionalItems{},
			detail: "Details: Array contains additional item(s) that are not allowed by the schema",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const schemaURL = "https://example.com/canary.schema.json"
			doc, err := jsonschema.UnmarshalJSON(strings.NewReader(
				`{"type": "object", "properties": {"value": ` + tt.schema + `}}`))
			require.NoError(t, err)
			compiler := newCompiler()
			if tt.draft != nil {
				compiler.DefaultDraft(tt.draft)
			}
			require.NoError(t, compiler.AddResource(schemaURL, doc))
			schema, err := compiler.Compile(schemaURL)
			require.NoError(t, err)
			instance, err := jsonschema.UnmarshalJSON(strings.NewReader(
				`{"value": ` + tt.instance + `}`))
			require.NoError(t, err)

			err = schema.Validate(instance)
			var ve *jsonschema.ValidationError
			require.ErrorAs(t, err, &ve)
			for len(ve.Causes) > 0 && reflect.TypeOf(ve.ErrorKind) != reflect.TypeOf(tt.wantKind) {
				require.Len(t, ve.Causes, 1)
				ve = ve.Causes[0]
			}
			assert.IsType(t, tt.wantKind, ve.ErrorKind)
			wantLocation := tt.wantLocation
			if wantLocation == nil {
				wantLocation = []string{"value"}
			}
			assert.Equal(t, wantLocation, ve.InstanceLocation)
			formatted := formatSchemaError(err)
			require.Error(t, formatted)
			assert.Contains(t, formatted.Error(), "Location: /"+strings.Join(wantLocation, "/")+"\n")
			assert.Contains(t, formatted.Error(), tt.detail)
		})
	}
}
