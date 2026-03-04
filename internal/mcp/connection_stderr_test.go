package mcp

import (
"context"
"encoding/json"
"testing"

"github.com/stretchr/testify/assert"
"github.com/stretchr/testify/require"
)

// TestConnection_ServerIDField verifies that the Connection struct stores the serverID
// used to attribute stderr log lines to specific backend servers.
// Before this feature, stderr from parallel backends was interleaved without attribution.
// After this feature, each log line includes the serverID: "[server1 stderr] message".
func TestConnection_ServerIDField(t *testing.T) {
conn := &Connection{serverID: "github"}
assert.Equal(t, "github", conn.serverID)

conn2 := &Connection{serverID: ""}
assert.Equal(t, "", conn2.serverID)
}

// TestNormalizeInputSchema tests the NormalizeInputSchema function.
func TestNormalizeInputSchema(t *testing.T) {
tests := []struct {
name     string
schema   map[string]interface{}
toolName string
wantType string
wantProps bool
}{
{
name:      "nil schema returns default empty object schema",
schema:    nil,
toolName:  "my_tool",
wantType:  "object",
wantProps: true,
},
{
name: "object schema with properties is returned unchanged",
schema: map[string]interface{}{
"type": "object",
"properties": map[string]interface{}{
"param1": map[string]interface{}{"type": "string"},
},
},
toolName:  "my_tool",
wantType:  "object",
wantProps: true,
},
{
name: "object schema without properties gets empty properties added",
schema: map[string]interface{}{
"type": "object",
},
toolName:  "my_tool",
wantType:  "object",
wantProps: true,
},
{
name: "schema with properties but no type gets type added",
schema: map[string]interface{}{
"properties": map[string]interface{}{
"key": map[string]interface{}{"type": "string"},
},
},
toolName:  "my_tool",
wantType:  "object",
wantProps: true,
},
{
name:      "schema without type and without properties returns default empty object",
schema:    map[string]interface{}{},
toolName:  "my_tool",
wantType:  "object",
wantProps: true,
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
result := NormalizeInputSchema(tt.schema, tt.toolName)

require.NotNil(t, result)
assert.Equal(t, tt.wantType, result["type"])
if tt.wantProps {
_, hasProps := result["properties"]
assert.True(t, hasProps, "Expected 'properties' field in normalized schema")
}
})
}
}

// TestNormalizeInputSchema_NonObjectTypes verifies non-object type schemas are returned as-is.
func TestNormalizeInputSchema_NonObjectTypes(t *testing.T) {
tests := []struct {
name   string
schema map[string]interface{}
}{
{
name:   "string type",
schema: map[string]interface{}{"type": "string"},
},
{
name:   "array type",
schema: map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
},
{
name:   "integer type",
schema: map[string]interface{}{"type": "integer"},
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
result := NormalizeInputSchema(tt.schema, "tool")
assert.Equal(t, tt.schema["type"], result["type"])
_, hasProps := result["properties"]
assert.False(t, hasProps, "Non-object schema should not have properties added")
})
}
}

// TestNormalizeInputSchema_AdditionalProperties verifies additionalProperties prevents empty-props injection.
func TestNormalizeInputSchema_AdditionalProperties(t *testing.T) {
schema := map[string]interface{}{
"type":                 "object",
"additionalProperties": true,
}
result := NormalizeInputSchema(schema, "tool")

assert.Equal(t, "object", result["type"])
assert.Equal(t, true, result["additionalProperties"])
_, hasProps := result["properties"]
assert.False(t, hasProps, "additionalProperties schema should not get empty properties added")
}

// TestNormalizeInputSchema_DoesNotMutateOriginal verifies the input schema is not modified.
func TestNormalizeInputSchema_DoesNotMutateOriginal(t *testing.T) {
original := map[string]interface{}{
"type": "object",
}
_, originalHadProps := original["properties"]

NormalizeInputSchema(original, "tool")

_, nowHasProps := original["properties"]
assert.Equal(t, originalHadProps, nowHasProps, "NormalizeInputSchema must not mutate the original schema")
}

// TestNormalizeInputSchema_NilSchemaReturnsIndependentMaps verifies each nil call returns a fresh map.
func TestNormalizeInputSchema_NilSchemaReturnsIndependentMaps(t *testing.T) {
result1 := NormalizeInputSchema(nil, "tool1")
result2 := NormalizeInputSchema(nil, "tool2")

require.NotNil(t, result1)
require.NotNil(t, result2)

result1["extra"] = "value"
_, hasExtra := result2["extra"]
assert.False(t, hasExtra, "Mutations to one result should not affect another")
}

// TestMarshalToResponse tests the marshalToResponse helper function.
func TestMarshalToResponse(t *testing.T) {
t.Run("marshals a simple map to response", func(t *testing.T) {
input := map[string]interface{}{
"tools": []string{"tool1", "tool2"},
}

resp, err := marshalToResponse(input)
require.NoError(t, err)
require.NotNil(t, resp)
assert.Equal(t, "2.0", resp.JSONRPC)
assert.NotNil(t, resp.Result)

var decoded map[string]interface{}
require.NoError(t, json.Unmarshal(resp.Result, &decoded))
tools, ok := decoded["tools"].([]interface{})
require.True(t, ok)
assert.Len(t, tools, 2)
})

t.Run("marshals nil to response", func(t *testing.T) {
resp, err := marshalToResponse(nil)
require.NoError(t, err)
require.NotNil(t, resp)
assert.Equal(t, "2.0", resp.JSONRPC)
})

t.Run("marshals struct to response", func(t *testing.T) {
type testResult struct {
Name  string `json:"name"`
Count int    `json:"count"`
}
input := testResult{Name: "my-tool", Count: 5}

resp, err := marshalToResponse(input)
require.NoError(t, err)
require.NotNil(t, resp)

var decoded testResult
require.NoError(t, json.Unmarshal(resp.Result, &decoded))
assert.Equal(t, "my-tool", decoded.Name)
assert.Equal(t, 5, decoded.Count)
})
}

// TestRequireSession tests the requireSession helper on a Connection.
func TestRequireSession(t *testing.T) {
t.Run("returns error when session is nil", func(t *testing.T) {
conn := &Connection{session: nil}
err := conn.requireSession()
require.Error(t, err)
assert.Contains(t, err.Error(), "SDK session not available")
})
}

// TestCallSDKMethod_UnsupportedMethod tests that callSDKMethod returns an error for unknown methods.
func TestCallSDKMethod_UnsupportedMethod(t *testing.T) {
conn := &Connection{serverID: "test-server"}

resp, err := conn.callSDKMethod("unknown/method", nil)
require.Error(t, err)
assert.Nil(t, resp)
assert.Contains(t, err.Error(), "unsupported method")
assert.Contains(t, err.Error(), "unknown/method")
}

// TestCallSDKMethod_SessionRequiredMethods verifies that methods needing a session
// return a session-not-available error when session is nil.
func TestCallSDKMethod_SessionRequiredMethods(t *testing.T) {
methods := []struct {
method string
params interface{}
}{
{"tools/list", nil},
{"tools/call", map[string]interface{}{"name": "test", "arguments": map[string]interface{}{}}},
{"resources/list", nil},
{"resources/read", map[string]interface{}{"uri": "test://resource"}},
{"prompts/list", nil},
{"prompts/get", map[string]interface{}{"name": "test"}},
}

for _, tt := range methods {
t.Run(tt.method, func(t *testing.T) {
conn := &Connection{
serverID: "test-server",
session:  nil,
ctx:      context.Background(),
}

resp, err := conn.callSDKMethod(tt.method, tt.params)
require.Error(t, err)
assert.Nil(t, resp)
assert.Contains(t, err.Error(), "SDK session not available",
"Method %s should require a session", tt.method)
})
}
}
