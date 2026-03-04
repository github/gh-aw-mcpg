package guard

import (
	"context"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logNoop = logger.New("guard:noop")

// NoopGuard is the default guard that performs no DIFC labeling
// It allows all operations by returning empty labels (no restrictions)
type NoopGuard struct{}

// NewNoopGuard creates a new noop guard
func NewNoopGuard() *NoopGuard {
	logNoop.Print("Creating new noop guard (no DIFC restrictions)")
	return &NoopGuard{}
}

// Name returns the identifier for this guard
func (g *NoopGuard) Name() string {
	return "noop"
}

// LabelResource returns an empty resource with no label requirements
// Conservatively assumes all operations could be writes
func (g *NoopGuard) LabelResource(ctx context.Context, toolName string, args interface{}, backend BackendCaller, caps *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	logNoop.Printf("Labeling resource: tool=%s, operation=write (conservative)", toolName)

	// Empty resource = no label requirements = all operations allowed
	resource := &difc.LabeledResource{
		Description: "noop resource (no restrictions)",
		Secrecy:     *difc.NewSecrecyLabel(),
		Integrity:   *difc.NewIntegrityLabel(),
		Structure:   nil, // No fine-grained labeling
	}

	logNoop.Printf("Resource labeled with no restrictions: tool=%s", toolName)

	// Conservatively treat as write to be safe
	// (writes are more restrictive than reads in DIFC)
	return resource, difc.OperationWrite, nil
}

// LabelResponse returns nil, indicating no fine-grained labeling
// The reference monitor will use the resource labels for the entire response
func (g *NoopGuard) LabelResponse(ctx context.Context, toolName string, result interface{}, backend BackendCaller, caps *difc.Capabilities) (difc.LabeledData, error) {
	logNoop.Printf("Labeling response: tool=%s, using resource labels (no fine-grained labeling)", toolName)

	// No fine-grained labeling - return nil
	// Reference monitor will use LabelResource result for entire response
	return nil, nil
}
