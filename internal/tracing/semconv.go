// Package tracing provides OpenTelemetry OTLP trace export for the MCP Gateway.
// This file is the single source of truth for go.opentelemetry.io/otel/semconv/v1.43.0
// imports in the tracing package. All callers inside and outside this package should
// reference these re-exports so that upgrading semconv only requires editing this file.
// Keep this semconv version in lockstep with the pinned otel/sdk version in go.mod to
// avoid resource schema conflicts ("conflicting Schema URL") during resource detection.
//
// Upgrading semconv:
//  1. Bump the go.opentelemetry.io/otel* modules in go.mod (all otel modules share a version).
//  2. Update the semconv import path below to the version used by that release's
//     sdk/resource detectors (otel/sdk v1.47.0 uses semconv/v1.43.0). This file must
//     remain the only direct semconv import in the repository; verify with
//     `grep -rn "otel/semconv" --include=*.go .` and route any new usage through a
//     re-export added here.
//  3. Apply any renames listed in the release's migration guide, published at
//     https://github.com/open-telemetry/opentelemetry-go/blob/main/semconv/v1.43.0/MIGRATION.md
//     (substitute the new version in the path), then update expectedSchemaURL in
//     semconv_test.go so the drift guard matches the new schema URL.
package tracing

import (
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// SchemaURL is the semconv schema URL for this semantic conventions version.
const SchemaURL = semconv.SchemaURL

// HTTP and URL semantic convention attribute keys.
const (
	// HTTPRequestMethodKey is the HTTP request method.
	HTTPRequestMethodKey = semconv.HTTPRequestMethodKey
	// HTTPRouteKey is the matched HTTP route template.
	HTTPRouteKey = semconv.HTTPRouteKey
	// HTTPResponseStatusCodeKey is the HTTP response status code.
	HTTPResponseStatusCodeKey = semconv.HTTPResponseStatusCodeKey
	// URLPathKey is the full URL path.
	URLPathKey = semconv.URLPathKey
	// ServerAddressKey is the server domain name or IP address.
	ServerAddressKey = semconv.ServerAddressKey
)

// Resource semantic convention attribute keys.
const (
	// ServiceNameKey is the logical name of the service.
	ServiceNameKey = semconv.ServiceNameKey
)

// Error semantic convention keys.
const (
	// ErrorTypeKey is the attribute key for error.type.
	ErrorTypeKey = semconv.ErrorTypeKey
)

// GenAI attribute keys used by tracing spans.
const (
	// GenAIToolName is the name of the tool utilized by the agent.
	GenAIToolName = attribute.Key("gen_ai.tool.name")
	// GenAIOperationName is the name of the operation being performed.
	GenAIOperationName = attribute.Key("gen_ai.operation.name")
	// GenAIConversationID is the unique identifier for a conversation (session).
	GenAIConversationID = attribute.Key("gen_ai.conversation.id")
	// GenAIAgentName is the human-readable name of the GenAI agent.
	GenAIAgentName = attribute.Key("gen_ai.agent.name")
	// GenAIAgentID is the unique identifier of the GenAI agent (server ID).
	GenAIAgentID = attribute.Key("gen_ai.agent.id")
)

// ErrorType returns the error.type attribute KeyValue for err.
// It wraps semconv.ErrorType so callers outside this package can use it
// without importing the semconv package directly.
func ErrorType(err error) attribute.KeyValue {
	return semconv.ErrorType(err)
}

// ServiceName returns the service.name attribute KeyValue for val.
func ServiceName(val string) attribute.KeyValue {
	return semconv.ServiceName(val)
}

// ServiceVersion returns the service.version attribute KeyValue for val.
func ServiceVersion(val string) attribute.KeyValue {
	return semconv.ServiceVersion(val)
}
